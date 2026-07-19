package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching/target"
)

func TestCreateRecoveryPointRemovesOwnedDirectoryAfterTargetBackupFailure(t *testing.T) {
	t.Parallel()

	recoveryRoot := t.TempDir()
	backupCalls := 0
	backend := transactionTestBackend{
		id: "backup-test",
		backup: func(_ context.Context, _ target.Spec, snapshot target.Snapshot, destination string) (string, error) {
			backupCalls++
			if err := os.WriteFile(destination, []byte("partial backup"), 0o600); err != nil {
				t.Fatalf("write backup fixture: %v", err)
			}
			if backupCalls == 2 {
				return "", errors.New("injected backup failure at " + destination)
			}
			return snapshot.Fingerprint, nil
		},
	}
	executor := New(target.MustRegistry(backend))
	operations := []Operation{
		backupTestOperation("target-a", backend.id, "before-a"),
		backupTestOperation("target-b", backend.id, "before-b"),
	}

	_, err := executor.CreateRecoveryPoint(context.Background(), RecoveryPointRequest{
		RecoveryRoot: recoveryRoot,
		OperationID:  "switch-test",
		Operations:   operations,
	})
	if err == nil {
		t.Fatal("CreateRecoveryPoint() error = nil, want injected failure")
	}
	if strings.Contains(err.Error(), recoveryRoot) {
		t.Fatalf("operation recovery error exposes its private path: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(recoveryRoot, "switch-test")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("operation recovery directory remains after failure: %v", statErr)
	}
}

func TestCreateRecoveryPointNeverCreatesOrFollowsRecoveryRoot(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "recovery")
		_, err := New(target.MustRegistry()).CreateRecoveryPoint(context.Background(), RecoveryPointRequest{
			RecoveryRoot: root,
			OperationID:  "switch-missing-root",
		})
		if err == nil {
			t.Fatal("CreateRecoveryPoint() succeeded without a cleanup-owned root")
		}
		if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("transaction created recovery root: %v", statErr)
		}
	})

	t.Run("symlink root", func(t *testing.T) {
		parent := t.TempDir()
		outside := t.TempDir()
		root := filepath.Join(parent, "recovery")
		if err := os.Symlink(outside, root); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := New(target.MustRegistry()).CreateRecoveryPoint(context.Background(), RecoveryPointRequest{
			RecoveryRoot: root,
			OperationID:  "switch-symlink-root",
		})
		if err == nil {
			t.Fatal("CreateRecoveryPoint() followed a recovery symlink")
		}
		if _, statErr := os.Lstat(filepath.Join(outside, "switch-symlink-root")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("transaction wrote through recovery symlink: %v", statErr)
		}
	})
}

func TestRestoreDoesNotExposeRecoverySourcePath(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "target-000.recovery")
	backend := transactionTestBackend{
		id: "restore-test",
		restore: func(_ context.Context, _ target.Spec, _ target.Snapshot, sourcePath, _ string, _ os.FileMode, _ bool) error {
			return apperror.Wrap(apperror.BackupInvalid, "operation recovery file could not be read", errors.New("read "+sourcePath))
		},
	}
	spec := transactionTestSpec{id: "target-a", backendID: backend.id}
	err := New(target.MustRegistry(backend)).Restore(
		context.Background(), backend.id, spec, target.Snapshot{}, sourcePath, "hash", 0, false,
	)
	if err == nil {
		t.Fatal("Restore() error = nil, want injected failure")
	}
	if strings.Contains(err.Error(), sourcePath) {
		t.Fatalf("restore error exposes its private source path: %v", err)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.BackupInvalid || appErr.Details["target_id"] != "target-a" {
		t.Fatalf("unexpected sanitized restore error: %#v", err)
	}
}

func TestCreateRecoveryPointDoesNotRemoveExistingOperationDirectory(t *testing.T) {
	t.Parallel()

	recoveryRoot := t.TempDir()
	recoveryPath := filepath.Join(recoveryRoot, "switch-existing")
	if err := os.Mkdir(recoveryPath, 0o700); err != nil {
		t.Fatalf("create existing recovery directory: %v", err)
	}
	sentinelPath := filepath.Join(recoveryPath, "manifest.json")
	if err := os.WriteFile(sentinelPath, []byte("existing backup"), 0o600); err != nil {
		t.Fatalf("write existing backup sentinel: %v", err)
	}

	_, err := New(target.MustRegistry()).CreateRecoveryPoint(context.Background(), RecoveryPointRequest{
		RecoveryRoot: recoveryRoot,
		OperationID:  "switch-existing",
	})
	if err == nil {
		t.Fatal("CreateRecoveryPoint() error = nil, want existing-directory failure")
	}
	if raw, readErr := os.ReadFile(sentinelPath); readErr != nil || string(raw) != "existing backup" {
		t.Fatalf("existing backup was changed: content=%q error=%v", raw, readErr)
	}
}

func TestCreateRecoveryPointUsesOneFlatOperationDirectory(t *testing.T) {
	t.Parallel()

	recoveryRoot := t.TempDir()
	backend := transactionTestBackend{
		id: "backup-test",
		backup: func(_ context.Context, _ target.Spec, snapshot target.Snapshot, destination string) (string, error) {
			if err := os.WriteFile(destination, []byte("private recovery state"), 0o600); err != nil {
				return "", err
			}
			return snapshot.Fingerprint, nil
		},
	}
	point, err := New(target.MustRegistry(backend)).CreateRecoveryPoint(context.Background(), RecoveryPointRequest{
		RecoveryRoot: recoveryRoot,
		OperationID:  "switch-flat",
		Operations: []Operation{
			backupTestOperation("target-a", backend.id, "before-a"),
			backupTestOperation("target-b", backend.id, "before-b"),
		},
	})
	if err != nil {
		t.Fatalf("create recovery point: %v", err)
	}
	entries, err := os.ReadDir(point.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"manifest.json", "target-000.recovery", "target-001.recovery"}
	if len(entries) != len(want) {
		t.Fatalf("flat recovery entries = %v, want %v", recoveryEntryNames(entries), want)
	}
	for index, entry := range entries {
		if entry.Name() != want[index] || entry.IsDir() {
			t.Fatalf("flat recovery entries = %v, want %v", recoveryEntryNames(entries), want)
		}
	}
}

func TestLoadRecoveryManifestRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	recoveryPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(recoveryPath, "manifest.json"), []byte(`{
		"operation_id":"switch-test",
		"provider_id":"codex",
		"profile_id":"work",
		"plan_fingerprint":"fingerprint",
		"created_at_unix_ms":1,
		"entries":[],
		"legacy_backup_path":"ignored-before"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadRecoveryManifest(recoveryPath); err == nil {
		t.Fatal("expected unknown recovery manifest field to be rejected")
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

func recoveryEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
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
	restore func(context.Context, target.Spec, target.Snapshot, string, string, os.FileMode, bool) error
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

func (backend transactionTestBackend) Restore(ctx context.Context, spec target.Spec, snapshot target.Snapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	if backend.restore != nil {
		return backend.restore(ctx, spec, snapshot, sourcePath, sourceSHA, mode, useMode)
	}
	return nil
}

func (transactionTestBackend) Remove(context.Context, target.Spec, target.Snapshot, bool) (bool, error) {
	return false, nil
}
