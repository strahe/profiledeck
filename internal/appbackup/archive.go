package appbackup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/strahe/profiledeck/internal/apperror"
)

const (
	manifestName          = "manifest.json"
	databaseName          = "profiledeck.db"
	maxManifestSize       = 64 * 1024
	maxBackupDatabaseSize = 64 << 30
)

type archiveManifest struct {
	FormatVersion   int    `json:"format_version"`
	BackupID        string `json:"backup_id"`
	Reason          string `json:"reason"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms"`
}

type inspectedArchive struct {
	Manifest    archiveManifest
	Fingerprint string
	SizeBytes   int64
}

type automaticBackupFile struct {
	name      string
	createdAt time.Time
}

func writeArchive(
	ctx context.Context,
	databasePath string,
	destination string,
	manifest archiveManifest,
	recipient age.Recipient,
) error {
	databaseInfo, err := os.Lstat(databasePath)
	if err != nil || !databaseInfo.Mode().IsRegular() || databaseInfo.Size() <= 0 {
		return apperror.New(apperror.BackupInvalid, "application database snapshot is invalid")
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to encode application backup manifest", err)
	}
	manifestJSON = append(manifestJSON, '\n')

	dir := filepath.Dir(destination)
	staging, err := os.CreateTemp(dir, ".profiledeck-backup-*.tmp")
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to create application backup staging file", err)
	}
	stagingPath := staging.Name()
	defer func() {
		if staging != nil {
			_ = staging.Close()
		}
		_ = os.Remove(stagingPath)
	}()
	if err := os.Chmod(stagingPath, 0o600); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to secure application backup staging file", err)
	}

	encrypted, err := age.Encrypt(staging, recipient)
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to initialize application backup encryption", err)
	}
	tw := tar.NewWriter(encrypted)
	createdAt := time.UnixMilli(manifest.CreatedAtUnixMS).UTC()
	writeErr := writeTarBytes(tw, manifestName, manifestJSON, createdAt)
	if writeErr == nil {
		writeErr = writeTarFile(ctx, tw, databaseName, databasePath, databaseInfo.Size(), createdAt)
	}
	writeErr = errors.Join(writeErr, tw.Close(), encrypted.Close())
	if writeErr == nil {
		writeErr = staging.Sync()
	}
	closeErr := staging.Close()
	staging = nil
	writeErr = errors.Join(writeErr, closeErr)
	if writeErr != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to write encrypted application backup", writeErr)
	}
	if _, err := os.Lstat(destination); err == nil {
		return apperror.New(apperror.BackupFailed, "application backup already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return apperror.New(apperror.BackupFailed, "application backup destination could not be checked")
	}
	if err := os.Rename(stagingPath, destination); err != nil {
		return apperror.New(apperror.BackupFailed, "failed to publish application backup")
	}
	if err := syncDirectory(dir); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to sync application backup directory", err)
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, content []byte, modTime time.Time) error {
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: int64(len(content)), ModTime: modTime,
		Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(content)
	return err
}

func writeTarFile(
	ctx context.Context,
	tw *tar.Writer,
	name string,
	path string,
	size int64,
	modTime time.Time,
) error {
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: size, ModTime: modTime,
		Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(tw, &contextReader{ctx: ctx, reader: file})
	if err != nil {
		return err
	}
	if written != size {
		return errors.New("database snapshot size changed while archiving")
	}
	return nil
}

func inspectArchive(
	ctx context.Context,
	path string,
	identity *age.X25519Identity,
	extractDatabasePath string,
) (inspectedArchive, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return inspectedArchive{}, apperror.New(apperror.BackupNotFound, "application backup not found")
	}
	if err != nil || !info.Mode().IsRegular() {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup could not be read")
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, &contextReader{ctx: ctx, reader: file}); err != nil {
		return inspectedArchive{}, safeArchiveReadError(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup could not be read")
	}
	decrypted, err := age.Decrypt(file, identity)
	if err != nil {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup could not be decrypted with the current recovery key")
	}

	tr := tar.NewReader(&contextReader{ctx: ctx, reader: decrypted})
	manifestHeader, err := tr.Next()
	if err != nil || !validTarHeader(manifestHeader, manifestName, maxManifestSize) {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	manifestContent, err := io.ReadAll(io.LimitReader(tr, maxManifestSize+1))
	if err != nil || int64(len(manifestContent)) > maxManifestSize {
		return inspectedArchive{}, safeArchiveReadError(err)
	}
	manifest, err := decodeManifest(manifestContent)
	if err != nil {
		return inspectedArchive{}, err
	}

	databaseHeader, err := tr.Next()
	if err != nil || !validTarHeader(databaseHeader, databaseName, maxBackupDatabaseSize) || databaseHeader.Size <= 0 {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup database entry is invalid")
	}
	if extractDatabasePath == "" {
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return inspectedArchive{}, safeArchiveReadError(err)
		}
	} else if err := extractDatabase(tr, databaseHeader.Size, extractDatabasePath); err != nil {
		return inspectedArchive{}, err
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup contains unexpected entries")
	}
	if trailing, err := io.Copy(io.Discard, decrypted); err != nil || trailing != 0 {
		if err != nil {
			return inspectedArchive{}, safeArchiveReadError(err)
		}
		return inspectedArchive{}, apperror.New(apperror.BackupInvalid, "application backup contains unexpected data")
	}
	return inspectedArchive{
		Manifest: manifest, Fingerprint: hex.EncodeToString(digest.Sum(nil)), SizeBytes: info.Size(),
	}, nil
}

// readArchiveManifest decrypts only the first archive entry so retention can
// classify protected backups without creating plaintext metadata sidecars.
func readArchiveManifest(
	ctx context.Context,
	path string,
	identity *age.X25519Identity,
) (archiveManifest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return archiveManifest{}, errors.New("backup archive is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return archiveManifest{}, err
	}
	defer file.Close()
	decrypted, err := age.Decrypt(file, identity)
	if err != nil {
		return archiveManifest{}, err
	}
	tr := tar.NewReader(&contextReader{ctx: ctx, reader: decrypted})
	header, err := tr.Next()
	if err != nil || !validTarHeader(header, manifestName, maxManifestSize) {
		return archiveManifest{}, errors.New("backup manifest is invalid")
	}
	content, err := io.ReadAll(io.LimitReader(tr, maxManifestSize+1))
	if err != nil || int64(len(content)) > maxManifestSize {
		return archiveManifest{}, errors.New("backup manifest is invalid")
	}
	return decodeManifest(content)
}

func validTarHeader(header *tar.Header, name string, maxSize int64) bool {
	return header != nil && header.Name == name && header.Typeflag == tar.TypeReg &&
		header.Size >= 0 && header.Size <= maxSize
}

func decodeManifest(content []byte) (archiveManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest archiveManifest
	if err := decoder.Decode(&manifest); err != nil {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	if manifest.FormatVersion != formatVersion {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup format is not supported")
	}
	id, err := normalizeBackupID(manifest.BackupID)
	if err != nil || id != manifest.BackupID || manifest.CreatedAtUnixMS <= 0 {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	kind, createdAt, ok := parseBackupID(manifest.BackupID)
	if !ok {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	if createdAt.UnixMilli() != manifest.CreatedAtUnixMS {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	_, reason, err := normalizeCreateRequest(CreateRequest{Kind: kind, Reason: manifest.Reason})
	if err != nil || reason != manifest.Reason {
		return archiveManifest{}, apperror.New(apperror.BackupInvalid, "application backup manifest is invalid")
	}
	return manifest, nil
}

func extractDatabase(source io.Reader, expectedSize int64, destination string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return apperror.New(apperror.BackupInvalid, "application backup database could not be prepared")
	}
	succeeded := false
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if !succeeded {
			_ = os.Remove(destination)
		}
	}()
	written, copyErr := io.Copy(file, source)
	syncErr := file.Sync()
	closeErr := file.Close()
	file = nil
	err = errors.Join(copyErr, syncErr, closeErr)
	if err != nil || written != expectedSize {
		return apperror.New(apperror.BackupInvalid, "application backup database is incomplete")
	}
	succeeded = true
	return nil
}

func safeArchiveReadError(err error) error {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	return apperror.New(apperror.BackupInvalid, "application backup is damaged or incomplete")
}

func retainAutomaticBackups(dir string, limit int) error {
	backups, err := listAutomaticBackupFiles(dir)
	if err != nil {
		return err
	}
	return retainBackupFiles(dir, backups, limit)
}

func listAutomaticBackupFiles(dir string) ([]automaticBackupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	backups := make([]automaticBackupFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), Extension) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), Extension)
		kind, createdAt, ok := parseBackupID(id)
		if !ok || kind != KindAutomatic {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, automaticBackupFile{name: entry.Name(), createdAt: createdAt})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].createdAt.Equal(backups[j].createdAt) {
			return backups[i].name > backups[j].name
		}
		return backups[i].createdAt.After(backups[j].createdAt)
	})
	return backups, nil
}

func listBeforeMigrationBackups(
	ctx context.Context,
	dir string,
	identity *age.X25519Identity,
) ([]automaticBackupFile, error) {
	backups, err := listAutomaticBackupFiles(dir)
	if err != nil {
		return nil, err
	}
	result := make([]automaticBackupFile, 0, len(backups))
	for _, backup := range backups {
		manifest, err := readArchiveManifest(ctx, filepath.Join(dir, backup.name), identity)
		if err != nil || manifest.Reason != ReasonBeforeMigration || manifest.BackupID+Extension != backup.name {
			continue
		}
		result = append(result, backup)
	}
	return result, nil
}

func retainBeforeMigrationBackups(
	ctx context.Context,
	dir string,
	identity *age.X25519Identity,
	limit int,
) error {
	backups, err := listBeforeMigrationBackups(ctx, dir, identity)
	if err != nil {
		return err
	}
	return retainBackupFiles(dir, backups, limit)
}

func retainBackupFiles(dir string, backups []automaticBackupFile, limit int) error {
	if limit < 0 {
		limit = 0
	}
	if len(backups) <= limit {
		return nil
	}
	removed := false
	for _, backup := range backups[limit:] {
		if err := os.Remove(filepath.Join(dir, backup.name)); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(dir)
	}
	return nil
}

func copyPrivateFile(source, destination string, overwrite bool) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return apperror.New(apperror.ExportFailed, "application backup could not be read")
	}
	file, err := os.Open(source)
	if err != nil {
		return apperror.New(apperror.ExportFailed, "application backup could not be read")
	}
	defer file.Close()
	return publishPrivateFile(destination, overwrite, func(output *os.File) error {
		_, err := io.Copy(output, file)
		return err
	})
}

func writePrivateFile(destination string, content []byte, overwrite bool) error {
	return publishPrivateFile(destination, overwrite, func(output *os.File) error {
		_, err := output.Write(content)
		return err
	})
}

func publishPrivateFile(destination string, overwrite bool, write func(*os.File) error) error {
	dir := filepath.Dir(destination)
	staging, err := os.CreateTemp(dir, ".profiledeck-export-*.tmp")
	if err != nil {
		return apperror.New(apperror.ExportFailed, "output file could not be created")
	}
	stagingPath := staging.Name()
	defer func() {
		if staging != nil {
			_ = staging.Close()
		}
		_ = os.Remove(stagingPath)
	}()
	if err := os.Chmod(stagingPath, 0o600); err != nil {
		return apperror.New(apperror.ExportFailed, "output file could not be secured")
	}
	err = write(staging)
	if err == nil {
		err = staging.Sync()
	}
	closeErr := staging.Close()
	staging = nil
	err = errors.Join(err, closeErr)
	if err != nil {
		return apperror.New(apperror.ExportFailed, "output file could not be written")
	}
	if overwrite {
		if err := os.Rename(stagingPath, destination); err != nil {
			return apperror.New(apperror.ExportFailed, "output file could not be published")
		}
	} else {
		if err := os.Link(stagingPath, destination); err != nil {
			if errors.Is(err, os.ErrExist) {
				return apperror.New(apperror.ExportFailed, "output file already exists")
			}
			return apperror.New(apperror.ExportFailed, "output file could not be published")
		}
		if err := os.Remove(stagingPath); err != nil {
			return apperror.New(apperror.ExportFailed, "output file could not be published")
		}
	}
	if err := syncDirectory(dir); err != nil {
		return apperror.New(apperror.ExportFailed, "output directory could not be synced")
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
