package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/switching/target"
)

func TestCreateBackupRemovesOwnedDirectoryAfterTargetBackupFailure(t *testing.T) {
	t.Parallel()

	backupsDir := t.TempDir()
	backupCalls := 0
	backend := transactionTestBackend{
		id: "backup-test",
		backup: func(_ context.Context, _ target.Spec, snapshot target.Snapshot, destination string) (string, error) {
			backupCalls++
			if err := os.WriteFile(destination, []byte("partial backup"), 0o600); err != nil {
				t.Fatalf("write backup fixture: %v", err)
			}
			if backupCalls == 2 {
				return "", errors.New("injected backup failure")
			}
			return snapshot.Fingerprint, nil
		},
	}
	executor := New(target.MustRegistry(backend))
	operations := []Operation{
		backupTestOperation("target-a", backend.id, "before-a"),
		backupTestOperation("target-b", backend.id, "before-b"),
	}

	_, err := executor.CreateBackup(context.Background(), BackupRequest{
		BackupsDir:  backupsDir,
		OperationID: "switch-test",
		Operations:  operations,
	})
	if err == nil {
		t.Fatal("CreateBackup() error = nil, want injected failure")
	}
	if _, statErr := os.Stat(filepath.Join(backupsDir, "switch-test")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("operation backup directory remains after failure: %v", statErr)
	}
}

func TestCreateBackupDoesNotRemoveExistingOperationDirectory(t *testing.T) {
	t.Parallel()

	backupsDir := t.TempDir()
	backupPath := filepath.Join(backupsDir, "switch-existing")
	if err := os.Mkdir(backupPath, 0o700); err != nil {
		t.Fatalf("create existing backup directory: %v", err)
	}
	sentinelPath := filepath.Join(backupPath, "manifest.json")
	if err := os.WriteFile(sentinelPath, []byte("existing backup"), 0o600); err != nil {
		t.Fatalf("write existing backup sentinel: %v", err)
	}

	_, err := New(target.MustRegistry()).CreateBackup(context.Background(), BackupRequest{
		BackupsDir:  backupsDir,
		OperationID: "switch-existing",
	})
	if err == nil {
		t.Fatal("CreateBackup() error = nil, want existing-directory failure")
	}
	if raw, readErr := os.ReadFile(sentinelPath); readErr != nil || string(raw) != "existing backup" {
		t.Fatalf("existing backup was changed: content=%q error=%v", raw, readErr)
	}
}

func backupTestOperation(targetID, backendID, content string) Operation {
	fingerprint := target.SHA256String(content)
	return Operation{
		TargetID:   targetID,
		BackendID:  backendID,
		Action:     ActionUpdate,
		FileExists: true,
		Spec:       transactionTestSpec{id: targetID, backendID: backendID},
		Snapshot:   target.Snapshot{Exists: true, Fingerprint: fingerprint},
	}
}

type transactionTestSpec struct {
	id        string
	backendID string
}

func (spec transactionTestSpec) BackendID() string          { return spec.backendID }
func (spec transactionTestSpec) TargetID() string           { return spec.id }
func (spec transactionTestSpec) SafeLabel() string          { return spec.id }
func (spec transactionTestSpec) LocatorFingerprint() string { return spec.id }
func (transactionTestSpec) Sensitive() bool                 { return false }

type transactionTestBackend struct {
	id      string
	inspect func(context.Context, target.Spec) (target.Snapshot, error)
	verify  func(context.Context, target.Spec, target.Snapshot) error
	backup  func(context.Context, target.Spec, target.Snapshot, string) (string, error)
}

func (backend transactionTestBackend) ID() string { return backend.id }

func (backend transactionTestBackend) Inspect(ctx context.Context, spec target.Spec) (target.Snapshot, error) {
	if backend.inspect != nil {
		return backend.inspect(ctx, spec)
	}
	return target.Snapshot{}, nil
}

func (backend transactionTestBackend) Verify(ctx context.Context, spec target.Spec, snapshot target.Snapshot) error {
	if backend.verify != nil {
		return backend.verify(ctx, spec, snapshot)
	}
	return nil
}

func (backend transactionTestBackend) Backup(ctx context.Context, spec target.Spec, snapshot target.Snapshot, destination string) (string, error) {
	if backend.backup != nil {
		return backend.backup(ctx, spec, snapshot, destination)
	}
	return snapshot.Fingerprint, nil
}

func (transactionTestBackend) Apply(context.Context, target.Spec, target.Snapshot, string, os.FileMode, bool) error {
	return nil
}

func (transactionTestBackend) Restore(context.Context, target.Spec, target.Snapshot, string, string, os.FileMode, bool) error {
	return nil
}

func (transactionTestBackend) Remove(context.Context, target.Spec, target.Snapshot, bool) (bool, error) {
	return false, nil
}
