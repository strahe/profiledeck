package target

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/targetfs"
)

// FileBackend delegates atomic filesystem operations to targetfs.
type FileBackend struct{}

func (FileBackend) ID() string { return BackendFile }

func (FileBackend) Inspect(ctx context.Context, raw Spec) (Snapshot, error) {
	spec, ok := raw.(FileSpec)
	if !ok {
		return Snapshot{}, errors.New("file backend received incompatible target spec")
	}
	return ReadFile(ctx, spec)
}

func (FileBackend) Verify(ctx context.Context, raw Spec, snapshot Snapshot) error {
	spec, ok := raw.(FileSpec)
	if !ok {
		return errors.New("file backend received incompatible target spec")
	}
	return MapFilesystemError(targetfs.VerifyExpected(ctx, targetfs.ExpectedTarget{
		TargetID: spec.ID, Path: spec.Path, Exists: snapshot.Exists, SHA256: snapshot.Fingerprint,
	}))
}

func (FileBackend) Backup(ctx context.Context, raw Spec, snapshot Snapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	spec, ok := raw.(FileSpec)
	if !ok {
		return "", errors.New("file backend received incompatible target spec")
	}
	hash, err := targetfs.CopyBackupFile(ctx, spec.Path, destination)
	if err != nil {
		return "", MapFilesystemError(err)
	}
	return hash, nil
}

func (FileBackend) Apply(ctx context.Context, raw Spec, snapshot Snapshot, desired string, mode os.FileMode, useMode bool) error {
	spec, ok := raw.(FileSpec)
	if !ok {
		return errors.New("file backend received incompatible target spec")
	}
	return MapFilesystemError(targetfs.AtomicWriteContent(ctx, targetfs.AtomicWriteContentRequest{
		Expected: targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: snapshot.Exists, SHA256: snapshot.Fingerprint},
		Content:  desired, Mode: mode, UseMode: useMode,
	}))
}

func (FileBackend) Restore(ctx context.Context, raw Spec, current Snapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	spec, ok := raw.(FileSpec)
	if !ok {
		return errors.New("file backend received incompatible target spec")
	}
	return MapFilesystemError(targetfs.AtomicWriteFile(ctx, targetfs.AtomicWriteFileRequest{
		Expected:   targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: current.Exists, SHA256: current.Fingerprint},
		SourcePath: sourcePath, SourceSHA256: sourceSHA, Mode: mode, UseMode: useMode,
	}))
}

func (FileBackend) Remove(ctx context.Context, raw Spec, current Snapshot, allowMissing bool) (bool, error) {
	spec, ok := raw.(FileSpec)
	if !ok {
		return false, errors.New("file backend received incompatible target spec")
	}
	removed, err := targetfs.GuardedRemove(ctx, targetfs.GuardedRemoveRequest{
		Expected:     targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: current.Exists, SHA256: current.Fingerprint},
		AllowMissing: allowMissing,
	})
	if err != nil {
		// A strict post-remove directory sync can fail after the target is gone.
		// Preserve that partial-success fact for recovery orchestration.
		return removed, MapFilesystemError(err)
	}
	return removed, nil
}

// ReadFile captures a target without following symlinks and only retains content when a strategy needs it.
func ReadFile(ctx context.Context, spec FileSpec) (Snapshot, error) {
	info, err := os.Lstat(spec.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, apperror.Wrap(apperror.TargetReadFailed, "failed to inspect target file", err).WithDetail("path", spec.Path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Snapshot{Exists: true, IsSymlink: true, Mode: info.Mode()}, nil
	}
	if info.IsDir() {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.New(apperror.TargetReadFailed, "target path is a directory").WithDetail("path", spec.Path)
	}
	if !info.Mode().IsRegular() {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.New(apperror.TargetReadFailed, "target path is not a regular file").WithDetail("path", spec.Path)
	}
	if info.Size() > targetfs.MaxFileBytes {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.New(apperror.TargetReadFailed, "target file is too large").
			WithDetail("path", spec.Path).WithDetail("size_bytes", info.Size()).WithDetail("max_bytes", targetfs.MaxFileBytes)
	}
	file, err := os.Open(spec.Path)
	if err != nil {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.Wrap(apperror.TargetReadFailed, "failed to read target file", err).WithDetail("path", spec.Path)
	}
	defer file.Close()

	reader := io.Reader(io.LimitReader(contextReader{ctx: ctx, reader: file}, targetfs.MaxFileBytes+1))
	raw, err := io.ReadAll(reader)
	if err != nil {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.Wrap(apperror.TargetReadFailed, "failed to read target file", err).WithDetail("path", spec.Path)
	}
	if len(raw) > targetfs.MaxFileBytes {
		return Snapshot{Exists: true, Mode: info.Mode()}, apperror.New(apperror.TargetReadFailed, "target file is too large").
			WithDetail("path", spec.Path).WithDetail("max_bytes", targetfs.MaxFileBytes)
	}
	snapshot := Snapshot{
		Exists: true, Fingerprint: SHA256(raw), Mode: info.Mode(),
		Preview: profiletarget.PreviewSensitiveText(string(raw)),
	}
	if spec.NeedsContent {
		snapshot.Content = string(raw)
	}
	return snapshot, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(p []byte) (int, error) {
	if reader.ctx != nil {
		select {
		case <-reader.ctx.Done():
			return 0, reader.ctx.Err()
		default:
		}
	}
	return reader.reader.Read(p)
}

// MapFilesystemError preserves ProfileDeck's stable external error codes.
func MapFilesystemError(err error) error {
	if err == nil {
		return nil
	}
	var targetErr *targetfs.Error
	if !errors.As(err, &targetErr) {
		return err
	}
	var code apperror.Code
	switch targetErr.Kind {
	case targetfs.KindLockHeld, targetfs.KindLockFailed:
		code = apperror.LockAcquireFailed
	case targetfs.KindTargetChanged:
		code = apperror.TargetChanged
	case targetfs.KindBackupInvalid:
		code = apperror.BackupInvalid
	case targetfs.KindBackupFailed:
		code = apperror.BackupFailed
	case targetfs.KindWriteFailed:
		code = apperror.TargetWriteFailed
	case targetfs.KindUnsupported:
		code = apperror.SwitchPlanUnsupported
	default:
		code = apperror.CommandFailed
	}
	appErr := apperror.Wrap(code, targetErr.Message, err)
	for key, value := range targetErr.Details {
		appErr = appErr.WithDetail(key, value)
	}
	return appErr
}
