package targetfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxFileBytes = 16 * 1024 * 1024

type ExpectedTarget struct {
	TargetID string
	Path     string
	Exists   bool
	SHA256   string
}

type TargetState struct {
	Path      string
	Exists    bool
	IsSymlink bool
	IsDir     bool
	IsRegular bool
	SHA256    string
	Mode      os.FileMode
	Size      int64
}

type AtomicWriteContentRequest struct {
	Expected ExpectedTarget
	Content  string
	Mode     os.FileMode
	UseMode  bool
}

type AtomicWriteFileRequest struct {
	Expected     ExpectedTarget
	SourcePath   string
	SourceSHA256 string
	Mode         os.FileMode
	UseMode      bool
}

type GuardedRemoveRequest struct {
	Expected     ExpectedTarget
	AllowMissing bool
}

func Inspect(ctx context.Context, path string) (TargetState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TargetState{Path: path}, nil
		}
		return TargetState{}, WrapError(KindTargetChanged, "failed to inspect target", err).WithDetail("path", path)
	}

	state := TargetState{
		Path:      path,
		Exists:    true,
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
		IsDir:     info.IsDir(),
		IsRegular: info.Mode().IsRegular(),
		Mode:      info.Mode(),
		Size:      info.Size(),
	}
	if state.IsSymlink || state.IsDir || !state.IsRegular {
		return state, nil
	}
	if info.Size() > MaxFileBytes {
		return TargetState{}, NewError(KindTargetChanged, "target file is too large").
			WithDetail("path", path).
			WithDetail("size_bytes", info.Size()).
			WithDetail("max_bytes", MaxFileBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return TargetState{}, WrapError(KindTargetChanged, "failed to read target", err).WithDetail("path", path)
	}
	defer file.Close()

	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: file}, MaxFileBytes+1))
	if err != nil {
		return TargetState{}, WrapError(KindTargetChanged, "failed to hash target", err).WithDetail("path", path)
	}
	if read > MaxFileBytes {
		return TargetState{}, NewError(KindTargetChanged, "target file is too large").
			WithDetail("path", path).
			WithDetail("max_bytes", MaxFileBytes)
	}
	state.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return state, nil
}

func VerifyExpected(ctx context.Context, expected ExpectedTarget) error {
	current, err := Inspect(ctx, expected.Path)
	if err != nil {
		return err
	}
	if !expected.Exists {
		if current.Exists {
			return changedError(expected, "target appeared")
		}
		return nil
	}
	if !current.Exists {
		return changedError(expected, "target disappeared")
	}
	if current.IsSymlink {
		return changedError(expected, "target is a symlink")
	}
	if current.IsDir {
		return changedError(expected, "target is a directory")
	}
	if !current.IsRegular {
		return changedError(expected, "target is not a regular file")
	}
	if expected.SHA256 != "" && current.SHA256 != expected.SHA256 {
		return changedError(expected, "target content changed")
	}
	return nil
}

func AtomicWriteContent(ctx context.Context, req AtomicWriteContentRequest) error {
	if err := VerifyExpected(ctx, req.Expected); err != nil {
		return err
	}
	return atomicWrite(ctx, req.Expected, strings.NewReader(req.Content), req.Mode, req.UseMode, sourceGuard{})
}

func AtomicWriteFile(ctx context.Context, req AtomicWriteFileRequest) error {
	if err := VerifyExpected(ctx, req.Expected); err != nil {
		return err
	}
	if strings.TrimSpace(req.SourceSHA256) == "" {
		return NewError(KindBackupInvalid, "source file hash is required").WithDetail("path", req.SourcePath)
	}
	info, err := os.Lstat(req.SourcePath)
	if err != nil {
		return WrapError(KindBackupInvalid, "failed to inspect source file", err).WithDetail("path", req.SourcePath)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
		return NewError(KindBackupInvalid, "source file is not a regular file").WithDetail("path", req.SourcePath)
	}
	if info.Size() > MaxFileBytes {
		return NewError(KindBackupInvalid, "source file is too large").
			WithDetail("path", req.SourcePath).
			WithDetail("size_bytes", info.Size()).
			WithDetail("max_bytes", MaxFileBytes)
	}
	source, err := os.Open(req.SourcePath)
	if err != nil {
		return WrapError(KindBackupInvalid, "failed to open source file", err).WithDetail("path", req.SourcePath)
	}
	defer source.Close()
	return atomicWrite(ctx, req.Expected, source, req.Mode, req.UseMode, sourceGuard{
		Path:   req.SourcePath,
		SHA256: req.SourceSHA256,
	})
}

func GuardedRemove(ctx context.Context, req GuardedRemoveRequest) (bool, error) {
	return guardedRemoveWithDirectorySync(ctx, req, SyncDirectory)
}

func guardedRemoveWithDirectorySync(ctx context.Context, req GuardedRemoveRequest, syncDirectory func(string) error) (bool, error) {
	current, err := Inspect(ctx, req.Expected.Path)
	if err != nil {
		return false, err
	}
	if !current.Exists {
		if req.AllowMissing {
			return false, nil
		}
		return false, changedError(req.Expected, "target disappeared")
	}
	if current.IsSymlink {
		return false, changedError(req.Expected, "target is a symlink")
	}
	if current.IsDir {
		return false, changedError(req.Expected, "target is a directory")
	}
	if !current.IsRegular {
		return false, changedError(req.Expected, "target is not a regular file")
	}
	if req.Expected.SHA256 != "" && current.SHA256 != req.Expected.SHA256 {
		return false, changedError(req.Expected, "target content changed")
	}
	parent := filepath.Dir(req.Expected.Path)
	if err := syncDirectory(parent); err != nil {
		return false, WrapError(KindWriteFailed, "failed to prepare target directory for removal", err).WithDetail("path", parent)
	}
	// Directory synchronization can be slow. Re-check immediately before remove
	// so a concurrent replacement is never deleted under the earlier snapshot.
	if err := VerifyExpected(ctx, req.Expected); err != nil {
		return false, err
	}
	if err := os.Remove(req.Expected.Path); err != nil {
		return false, WrapError(KindWriteFailed, "failed to remove target", err).WithDetail("path", req.Expected.Path)
	}
	if err := syncDirectory(parent); err != nil {
		return true, WrapError(KindWriteFailed, "failed to finalize target removal", err).WithDetail("path", parent)
	}
	return true, nil
}

func CopyBackupFile(ctx context.Context, source, destination string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", WrapError(KindBackupFailed, "failed to inspect target for backup", err).WithDetail("path", source)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
		return "", NewError(KindBackupFailed, "target is not a regular file").WithDetail("path", source)
	}
	if info.Size() > MaxFileBytes {
		return "", NewError(KindBackupFailed, "target file is too large").
			WithDetail("path", source).
			WithDetail("size_bytes", info.Size()).
			WithDetail("max_bytes", MaxFileBytes)
	}

	input, err := os.Open(source)
	if err != nil {
		return "", WrapError(KindBackupFailed, "failed to open target for backup", err).WithDetail("path", source)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", WrapError(KindBackupFailed, "failed to create backup file", err).WithDetail("path", destination)
	}
	removeOutput := true
	defer func() {
		if output != nil {
			_ = output.Close()
		}
		if removeOutput {
			_ = os.Remove(destination)
		}
	}()

	hash := sha256.New()
	read, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(contextReader{ctx: ctx, reader: input}, MaxFileBytes+1))
	if err != nil {
		return "", WrapError(KindBackupFailed, "failed to copy backup file", err).WithDetail("path", source)
	}
	if read > MaxFileBytes {
		return "", NewError(KindBackupFailed, "target file is too large").
			WithDetail("path", source).
			WithDetail("max_bytes", MaxFileBytes)
	}
	if err := output.Sync(); err != nil {
		return "", WrapError(KindBackupFailed, "failed to sync backup file", err).WithDetail("path", destination)
	}
	if err := output.Close(); err != nil {
		return "", WrapError(KindBackupFailed, "failed to close backup file", err).WithDetail("path", destination)
	}
	output = nil
	removeOutput = false
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type sourceGuard struct {
	Path   string
	SHA256 string
}

func atomicWrite(ctx context.Context, expected ExpectedTarget, reader io.Reader, requestedMode os.FileMode, useMode bool, guard sourceGuard) error {
	return atomicWriteWithDirectorySync(ctx, expected, reader, requestedMode, useMode, guard, SyncDirectory)
}

func atomicWriteWithDirectorySync(
	ctx context.Context,
	expected ExpectedTarget,
	reader io.Reader,
	requestedMode os.FileMode,
	useMode bool,
	guard sourceGuard,
	syncDirectory func(string) error,
) error {
	parent := filepath.Dir(expected.Path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return WrapError(KindWriteFailed, "failed to inspect target parent directory", err).WithDetail("path", parent)
	}
	if !parentInfo.IsDir() {
		return NewError(KindWriteFailed, "target parent path is not a directory").WithDetail("path", parent)
	}
	// Confirm the filesystem accepts the durability boundary before writing the
	// desired content even to a private temporary file.
	if err := syncDirectory(parent); err != nil {
		return WrapError(KindWriteFailed, "failed to prepare target directory for replacement", err).WithDetail("path", parent)
	}

	mode := os.FileMode(0o600)
	if useMode {
		mode = requestedMode.Perm()
		if mode == 0 {
			mode = 0o600
		}
	} else if expected.Exists {
		info, err := os.Lstat(expected.Path)
		if err != nil {
			return WrapError(KindTargetChanged, "failed to inspect target before write", err).WithDetail("path", expected.Path)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
			return NewError(KindTargetChanged, "target is not a regular file before write").WithDetail("path", expected.Path)
		}
		mode = info.Mode().Perm()
	} else {
		if _, err := os.Lstat(expected.Path); err == nil {
			return changedError(expected, "target appeared before write")
		} else if !errors.Is(err, os.ErrNotExist) {
			return WrapError(KindTargetChanged, "failed to inspect target before write", err).WithDetail("path", expected.Path)
		}
	}

	temp, err := os.CreateTemp(parent, ".profiledeck-*")
	if err != nil {
		return WrapError(KindWriteFailed, "failed to create temporary target file", err).WithDetail("path", parent)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(mode); err != nil {
		return WrapError(KindWriteFailed, "failed to set temporary target mode", err).WithDetail("path", tempPath)
	}
	copyReader := io.Reader(contextReader{ctx: ctx, reader: reader})
	var sourceHash hash.Hash
	if guard.SHA256 != "" {
		sourceHash = sha256.New()
		copyReader = io.LimitReader(copyReader, MaxFileBytes+1)
	}
	writer := io.Writer(temp)
	if sourceHash != nil {
		writer = io.MultiWriter(temp, sourceHash)
	}
	written, err := io.Copy(writer, copyReader)
	if err != nil {
		return WrapError(KindWriteFailed, "failed to write temporary target file", err).WithDetail("path", tempPath)
	}
	if guard.SHA256 != "" {
		if written > MaxFileBytes {
			return NewError(KindBackupInvalid, "source file is too large").
				WithDetail("path", guard.Path).
				WithDetail("max_bytes", MaxFileBytes)
		}
		if hex.EncodeToString(sourceHash.Sum(nil)) != guard.SHA256 {
			return NewError(KindBackupInvalid, "source file hash changed").
				WithDetail("path", guard.Path)
		}
	}
	if err := temp.Sync(); err != nil {
		return WrapError(KindWriteFailed, "failed to sync temporary target file", err).WithDetail("path", tempPath)
	}
	if err := temp.Close(); err != nil {
		return WrapError(KindWriteFailed, "failed to close temporary target file", err).WithDetail("path", tempPath)
	}
	temp = nil
	// Re-check just before rename so a slow temporary-file write cannot clobber
	// an external target change made after the initial hash guard.
	if err := VerifyExpected(ctx, expected); err != nil {
		return err
	}
	if err := os.Rename(tempPath, expected.Path); err != nil {
		return WrapError(KindWriteFailed, "failed to replace target file", err).WithDetail("path", expected.Path)
	}
	removeTemp = false
	if err := syncDirectory(parent); err != nil {
		return WrapError(KindWriteFailed, "failed to finalize target replacement", err).WithDetail("path", parent)
	}
	return nil
}

func changedError(expected ExpectedTarget, message string) *Error {
	return NewError(KindTargetChanged, message).
		WithDetail("target_id", expected.TargetID).
		WithDetail("path", expected.Path)
}

func syncParentDirBestEffort(parent string) {
	_ = SyncDirectory(parent)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if r.ctx != nil {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
	}
	return r.reader.Read(p)
}
