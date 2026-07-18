// Package appbackup owns encrypted, application-wide ProfileDeck backups.
package appbackup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	Extension             = ".profiledeck-backup"
	KindManual            = "manual"
	KindAutomatic         = "automatic"
	ReasonManual          = "manual"
	ReasonScheduled       = "scheduled"
	ReasonBeforeUpdate    = "before_update"
	ReasonBeforeRestore   = "before_restore"
	ReasonBeforeMigration = "before_migration"

	formatVersion          = 1
	automaticRetention     = 10
	migrationRetention     = 3
	backupTimeLayout       = "20060102-150405Z"
	maxBackupIDSequence    = 9999
	keyringService         = "ProfileDeck"
	keyringAccount         = "application-backup-recovery-key-v1"
	maxRecoveryKeyFileSize = 64 * 1024
)

type CreateRequest struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type BackupSummary struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms"`
	SizeBytes       int64  `json:"size_bytes"`
}

type BackupDetail struct {
	BackupSummary
	Reason        string `json:"reason"`
	FormatVersion int    `json:"format_version"`
}

type ListResult struct {
	Backups                  []BackupSummary `json:"backups"`
	AutomaticCleanupRequired bool            `json:"automatic_cleanup_required"`
}

type ExportRequest struct {
	BackupID   string `json:"backup_id"`
	OutputPath string `json:"output_path"`
	Overwrite  bool   `json:"overwrite"`
}

type ExportResult struct {
	Backup BackupSummary `json:"backup"`
	Path   string        `json:"path"`
}

type DeleteRequest struct {
	BackupID string `json:"backup_id"`
	Confirm  bool   `json:"confirm"`
}

type KeyStatus struct {
	Available bool   `json:"available"`
	Recipient string `json:"recipient,omitempty"`
}

type ExportKeyRequest struct {
	OutputPath string `json:"output_path"`
	Confirm    bool   `json:"confirm"`
}

type ExportKeyResult struct {
	Path      string `json:"path"`
	Recipient string `json:"recipient"`
}

type ImportKeyRequest struct {
	InputPath string `json:"input_path"`
	Replace   bool   `json:"replace"`
	Confirm   bool   `json:"confirm"`
}

type ImportKeyResult struct {
	Recipient string `json:"recipient"`
	Changed   bool   `json:"changed"`
}

type keyStore interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
}

type systemKeyStore struct{}

func (systemKeyStore) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyStore) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

type exclusiveRunner interface {
	RunExclusive(context.Context, string, func(context.Context) error) error
}

type Service struct {
	mu        sync.Mutex
	paths     runtime.Paths
	stores    store.Factory
	keys      keyStore
	exclusive exclusiveRunner
	now       func() time.Time
	retain    func(string, int) error
}

func NewService(paths runtime.Paths, stores store.Factory, lease *runtime.DataLease) *Service {
	return newService(paths, stores, systemKeyStore{}, lease)
}

func newService(paths runtime.Paths, stores store.Factory, keys keyStore, exclusive exclusiveRunner) *Service {
	return &Service{
		paths: paths, stores: stores, keys: keys, exclusive: exclusive,
		now: time.Now, retain: retainAutomaticBackups,
	}
}

func (service *Service) Create(ctx context.Context, req CreateRequest) (BackupDetail, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-create")
	if err != nil {
		return BackupDetail{}, err
	}
	defer lock.Release()
	return service.create(ctx, req)
}

func (service *Service) create(ctx context.Context, req CreateRequest) (BackupDetail, error) {
	kind, reason, err := normalizeCreateRequest(req)
	if err != nil {
		return BackupDetail{}, err
	}
	identity, err := service.loadOrCreateIdentity()
	if err != nil {
		return BackupDetail{}, err
	}
	if err := os.MkdirAll(service.paths.Backups, 0o700); err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to create application backup directory", err)
	}
	if err := os.Chmod(service.paths.Backups, 0o700); err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to secure application backup directory", err)
	}

	createdAt := service.now().UTC().Truncate(time.Second)
	id, err := nextBackupID(service.paths, kind, createdAt)
	if err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to create application backup id", err)
	}
	// Keep the plaintext SQLite staging copy out of backups/: that directory is
	// reserved for encrypted, publishable application backup files.
	snapshotPath := filepath.Join(service.paths.Root, "."+id+".snapshot.db")
	defer func() { _ = os.Remove(snapshotPath) }()
	createSnapshot := service.stores.CreateSnapshot
	requireCurrentSchema := true
	if reason == ReasonBeforeMigration {
		createSnapshot = service.stores.CreateCompatibleSnapshot
		requireCurrentSchema = false
	}
	if err := createSnapshot(ctx, snapshotPath); err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to create application database snapshot", err)
	}
	if err := validateDatabase(ctx, snapshotPath, requireCurrentSchema); err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupInvalid, "application database snapshot is invalid", err)
	}

	manifest := archiveManifest{
		FormatVersion:   formatVersion,
		BackupID:        id,
		Reason:          reason,
		CreatedAtUnixMS: createdAt.UnixMilli(),
	}
	finalPath := filepath.Join(service.paths.Backups, id+Extension)
	if err := writeArchive(ctx, snapshotPath, finalPath, manifest, identity.Recipient()); err != nil {
		return BackupDetail{}, err
	}
	if kind == KindAutomatic {
		// Once the encrypted archive is durably published, retention is best-effort:
		// cleanup must not turn a successful backup into a reported failure.
		service.cleanupAutomaticBackups(ctx)
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to inspect created application backup", err)
	}
	return detailFromManifest(manifest, info.Size()), nil
}

func (service *Service) List(ctx context.Context) (ListResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-list")
	if err != nil {
		return ListResult{}, err
	}
	defer lock.Release()
	return service.list(ctx)
}

func (service *Service) list(ctx context.Context) (ListResult, error) {
	entries, err := os.ReadDir(service.paths.Backups)
	if errors.Is(err, os.ErrNotExist) {
		return ListResult{Backups: []BackupSummary{}}, nil
	}
	if err != nil {
		return ListResult{}, apperror.Wrap(apperror.BackupFailed, "failed to list application backups", err)
	}
	backups := make([]BackupSummary, 0, len(entries))
	automaticCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), Extension) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), Extension)
		kind, createdAt, ok := parseBackupID(id)
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, BackupSummary{
			ID: id, Kind: kind, CreatedAtUnixMS: createdAt.UnixMilli(), SizeBytes: info.Size(),
		})
		if kind == KindAutomatic {
			automaticCount++
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAtUnixMS == backups[j].CreatedAtUnixMS {
			return backups[i].ID > backups[j].ID
		}
		return backups[i].CreatedAtUnixMS > backups[j].CreatedAtUnixMS
	})
	cleanupRequired := automaticCount > automaticRetention
	if !cleanupRequired && automaticCount > migrationRetention {
		if identity, err := service.loadIdentity(); err == nil {
			if migrationBackups, err := listBeforeMigrationBackups(ctx, service.paths.Backups, identity); err == nil {
				cleanupRequired = len(migrationBackups) > migrationRetention
			}
		}
	}
	return ListResult{Backups: backups, AutomaticCleanupRequired: cleanupRequired}, nil
}

func (service *Service) cleanupAutomaticBackups(ctx context.Context) {
	var cleanupErr error
	identity, err := service.loadIdentity()
	if err != nil {
		cleanupErr = err
	} else {
		cleanupErr = retainBeforeMigrationBackups(ctx, service.paths.Backups, identity, migrationRetention)
	}
	retain := service.retain
	if retain == nil {
		retain = retainAutomaticBackups
	}
	cleanupErr = errors.Join(cleanupErr, retain(service.paths.Backups, automaticRetention))
	if cleanupErr != nil {
		// Do not include the raw filesystem error: runtime paths are private output.
		log.Print("profiledeck: automatic backup cleanup incomplete")
	}
}

func (service *Service) Show(ctx context.Context, rawID string) (BackupDetail, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-show")
	if err != nil {
		return BackupDetail{}, err
	}
	defer lock.Release()
	return service.show(ctx, rawID)
}

func (service *Service) show(ctx context.Context, rawID string) (BackupDetail, error) {
	id, err := normalizeBackupID(rawID)
	if err != nil {
		return BackupDetail{}, err
	}
	path := filepath.Join(service.paths.Backups, id+Extension)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return BackupDetail{}, apperror.New(apperror.BackupNotFound, "application backup not found")
	}
	if err != nil || !info.Mode().IsRegular() {
		return BackupDetail{}, apperror.New(apperror.BackupInvalid, "application backup is not a regular file")
	}
	identity, err := service.loadIdentity()
	if err != nil {
		return BackupDetail{}, err
	}
	inspected, err := inspectArchive(ctx, path, identity, "")
	if err != nil {
		return BackupDetail{}, err
	}
	if inspected.Manifest.BackupID != id {
		return BackupDetail{}, apperror.New(apperror.BackupInvalid, "application backup id does not match its file name")
	}
	return detailFromManifest(inspected.Manifest, inspected.SizeBytes), nil
}

func (service *Service) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-export")
	if err != nil {
		return ExportResult{}, err
	}
	defer lock.Release()
	detail, err := service.show(ctx, req.BackupID)
	if err != nil {
		return ExportResult{}, err
	}
	outputPath := filepath.Clean(strings.TrimSpace(req.OutputPath))
	if outputPath == "." || outputPath == "" {
		return ExportResult{}, apperror.New(apperror.ExportFailed, "application backup output file is required")
	}
	sourcePath := filepath.Join(service.paths.Backups, detail.ID+Extension)
	if err := copyPrivateFile(sourcePath, outputPath, req.Overwrite); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Backup: detail.BackupSummary, Path: outputPath}, nil
}

func (service *Service) Delete(_ context.Context, req DeleteRequest) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-delete")
	if err != nil {
		return err
	}
	defer lock.Release()
	if !req.Confirm {
		return apperror.New(apperror.ConfirmationRequired, "deleting an application backup requires confirmation")
	}
	id, err := normalizeBackupID(req.BackupID)
	if err != nil {
		return err
	}
	path := filepath.Join(service.paths.Backups, id+Extension)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return apperror.New(apperror.BackupNotFound, "application backup not found")
	}
	if err != nil || !info.Mode().IsRegular() {
		return apperror.New(apperror.BackupInvalid, "application backup is not a regular file")
	}
	if err := os.Remove(path); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to delete application backup", err)
	}
	return syncDirectory(service.paths.Backups)
}

func (service *Service) KeyStatus(_ context.Context) (KeyStatus, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-key-status")
	if err != nil {
		return KeyStatus{}, err
	}
	defer lock.Release()
	identity, err := service.loadIdentity()
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Details["reason"] == "missing" {
			return KeyStatus{}, nil
		}
		return KeyStatus{}, err
	}
	return KeyStatus{Available: true, Recipient: identity.Recipient().String()}, nil
}

func (service *Service) ExportKey(_ context.Context, req ExportKeyRequest) (ExportKeyResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-key-export")
	if err != nil {
		return ExportKeyResult{}, err
	}
	defer lock.Release()
	if !req.Confirm {
		return ExportKeyResult{}, apperror.New(apperror.ConfirmationRequired, "exporting the recovery key requires confirmation")
	}
	identity, err := service.loadIdentity()
	if err != nil {
		return ExportKeyResult{}, err
	}
	path := filepath.Clean(strings.TrimSpace(req.OutputPath))
	if path == "." || path == "" {
		return ExportKeyResult{}, apperror.New(apperror.ExportFailed, "recovery key output file is required")
	}
	content := fmt.Sprintf("# ProfileDeck application backup recovery key\n# public key: %s\n%s\n", identity.Recipient(), identity)
	if err := writePrivateFile(path, []byte(content), false); err != nil {
		return ExportKeyResult{}, err
	}
	return ExportKeyResult{Path: path, Recipient: identity.Recipient().String()}, nil
}

func (service *Service) ImportKey(_ context.Context, req ImportKeyRequest) (ImportKeyResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-key-import")
	if err != nil {
		return ImportKeyResult{}, err
	}
	defer lock.Release()
	if !req.Confirm {
		return ImportKeyResult{}, apperror.New(apperror.ConfirmationRequired, "importing a recovery key requires confirmation")
	}
	identity, err := readIdentityFile(req.InputPath)
	if err != nil {
		return ImportKeyResult{}, err
	}
	existing, existingErr := service.loadIdentity()
	if existingErr == nil {
		if existing.String() == identity.String() {
			return ImportKeyResult{Recipient: identity.Recipient().String()}, nil
		}
		if !req.Replace {
			return ImportKeyResult{}, apperror.New(apperror.ConfirmationRequired, "replacing the current recovery key requires explicit confirmation").
				WithDetail("reason", "replace_required")
		}
	} else {
		var appErr *apperror.Error
		if !errors.As(existingErr, &appErr) || appErr.Details["reason"] != "missing" {
			return ImportKeyResult{}, existingErr
		}
	}
	if err := service.keys.Set(keyringService, keyringAccount, identity.String()); err != nil {
		return ImportKeyResult{}, apperror.New(apperror.BackupFailed, "could not save the application backup recovery key").WithDetail("reason", "credential_store_unavailable")
	}
	return ImportKeyResult{Recipient: identity.Recipient().String(), Changed: true}, nil
}

func (service *Service) loadOrCreateIdentity() (*age.X25519Identity, error) {
	identity, err := service.loadIdentity()
	if err == nil {
		return identity, nil
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Details["reason"] != "missing" {
		return nil, err
	}
	identity, err = age.GenerateX25519Identity()
	if err != nil {
		return nil, apperror.Wrap(apperror.BackupFailed, "failed to generate the application backup recovery key", err)
	}
	if err := service.keys.Set(keyringService, keyringAccount, identity.String()); err != nil {
		return nil, apperror.New(apperror.BackupFailed, "could not save the application backup recovery key").WithDetail("reason", "credential_store_unavailable")
	}
	return identity, nil
}

func (service *Service) loadIdentity() (*age.X25519Identity, error) {
	raw, err := service.keys.Get(keyringService, keyringAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, apperror.New(apperror.BackupFailed, "application backup recovery key is not available").WithDetail("reason", "missing")
	}
	if err != nil {
		return nil, apperror.New(apperror.BackupFailed, "could not read the application backup recovery key").WithDetail("reason", "credential_store_unavailable")
	}
	identity, parseErr := age.ParseX25519Identity(strings.TrimSpace(raw))
	if parseErr != nil {
		return nil, apperror.New(apperror.BackupInvalid, "saved application backup recovery key is invalid")
	}
	return identity, nil
}

func (service *Service) acquireOperationLock(owner string) (targetfs.Lock, error) {
	lock, err := targetfs.AcquireLock(service.paths.BackupLock, owner)
	if err != nil {
		return targetfs.Lock{}, apperror.New(apperror.LockAcquireFailed, "another application backup operation is in progress")
	}
	return lock, nil
}

func normalizeCreateRequest(req CreateRequest) (string, string, error) {
	kind := strings.TrimSpace(req.Kind)
	reason := strings.TrimSpace(req.Reason)
	if kind == "" {
		kind = KindManual
	}
	if reason == "" {
		if kind == KindManual {
			reason = ReasonManual
		} else {
			reason = ReasonScheduled
		}
	}
	if kind != KindManual && kind != KindAutomatic {
		return "", "", apperror.New(apperror.BackupInvalid, "application backup kind is invalid")
	}
	if kind == KindManual && reason != ReasonManual {
		return "", "", apperror.New(apperror.BackupInvalid, "manual application backup reason is invalid")
	}
	if kind == KindAutomatic && reason != ReasonScheduled && reason != ReasonBeforeUpdate && reason != ReasonBeforeRestore && reason != ReasonBeforeMigration {
		return "", "", apperror.New(apperror.BackupInvalid, "automatic application backup reason is invalid")
	}
	return kind, reason, nil
}

func nextBackupID(paths runtime.Paths, kind string, now time.Time) (string, error) {
	for sequence := 1; sequence <= maxBackupIDSequence; sequence++ {
		id := formatBackupID(kind, now, sequence)
		backupExists, err := pathExists(filepath.Join(paths.Backups, id+Extension))
		if err != nil {
			return "", err
		}
		snapshotExists, err := pathExists(filepath.Join(paths.Root, "."+id+".snapshot.db"))
		if err != nil {
			return "", err
		}
		if !backupExists && !snapshotExists {
			return id, nil
		}
	}
	return "", errors.New("application backup id sequence is exhausted")
}

func formatBackupID(kind string, now time.Time, sequence int) string {
	prefix := "manual"
	if kind == KindAutomatic {
		prefix = "auto"
	}
	id := fmt.Sprintf("%s-%s", prefix, now.UTC().Format(backupTimeLayout))
	if sequence > 1 {
		id += fmt.Sprintf("-%04d", sequence)
	}
	return id
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func normalizeBackupID(raw string) (string, error) {
	id := strings.TrimSpace(strings.TrimSuffix(raw, Extension))
	if _, ok := kindFromBackupID(id); !ok || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return "", apperror.New(apperror.BackupInvalid, "application backup id is invalid")
	}
	return id, nil
}

func kindFromBackupID(id string) (string, bool) {
	kind, _, ok := parseBackupID(id)
	return kind, ok
}

func parseBackupID(id string) (string, time.Time, bool) {
	parts := strings.Split(id, "-")
	if len(parts) != 3 && len(parts) != 4 {
		return "", time.Time{}, false
	}
	kind := KindManual
	if parts[0] == "auto" {
		kind = KindAutomatic
	} else if parts[0] != "manual" {
		return "", time.Time{}, false
	}
	if len(parts) == 4 {
		sequence, err := strconv.Atoi(parts[3])
		if err != nil || sequence < 2 || sequence > maxBackupIDSequence || fmt.Sprintf("%04d", sequence) != parts[3] {
			return "", time.Time{}, false
		}
	}
	createdAt, err := time.Parse(backupTimeLayout, parts[1]+"-"+parts[2])
	if err != nil {
		return "", time.Time{}, false
	}
	return kind, createdAt.UTC(), true
}

func detailFromManifest(manifest archiveManifest, size int64) BackupDetail {
	kind, _ := kindFromBackupID(manifest.BackupID)
	return BackupDetail{
		BackupSummary: BackupSummary{
			ID: manifest.BackupID, Kind: kind, CreatedAtUnixMS: manifest.CreatedAtUnixMS, SizeBytes: size,
		},
		Reason: manifest.Reason, FormatVersion: manifest.FormatVersion,
	}
}

func readIdentityFile(rawPath string) (*age.X25519Identity, error) {
	path := filepath.Clean(strings.TrimSpace(rawPath))
	if path == "." || path == "" {
		return nil, apperror.New(apperror.ImportInvalid, "recovery key file is required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRecoveryKeyFileSize {
		return nil, apperror.New(apperror.ImportInvalid, "recovery key file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, apperror.New(apperror.ImportInvalid, "recovery key file could not be read")
	}
	defer file.Close()
	identities, err := age.ParseIdentities(io.LimitReader(file, maxRecoveryKeyFileSize+1))
	if err != nil || len(identities) != 1 {
		return nil, apperror.New(apperror.ImportInvalid, "recovery key file must contain one ProfileDeck recovery key")
	}
	identity, ok := identities[0].(*age.X25519Identity)
	if !ok {
		return nil, apperror.New(apperror.ImportInvalid, "recovery key type is not supported")
	}
	return identity, nil
}
