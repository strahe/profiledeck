package transaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching/target"
)

const (
	ActionCreate      = "create"
	ActionUpdate      = "update"
	ActionNoop        = "noop"
	ActionUnsupported = "unsupported"
)

// Operation is the non-persistent execution form of one finalized plan target.
// It deliberately carries no ProfileDeck store or lock dependency.
type Operation struct {
	TargetID       string
	BackendID      string
	TargetLabel    string
	Path           string
	Action         string
	FileExists     bool
	BeforeSHA256   string
	DesiredSHA256  string
	DesiredContent string
	BeforeMode     os.FileMode
	DesiredMode    os.FileMode
	UseDesiredMode bool
	Spec           target.Spec
	Snapshot       target.Snapshot
}

// Entry is the stable, private backup-manifest representation of one target.
type Entry struct {
	TargetID      string `json:"target_id"`
	BackendID     string `json:"backend_id"`
	TargetLabel   string `json:"target_label"`
	Path          string `json:"path"`
	Action        string `json:"action"`
	Existed       bool   `json:"existed"`
	BeforeSHA256  string `json:"before_sha256"`
	Mode          string `json:"mode"`
	BackupRelPath string `json:"backup_rel_path"`
	// PrivateLocator is backend-owned recovery state. It must not enter DTOs.
	PrivateLocator string `json:"private_locator,omitempty"`
}

// Manifest is the stable on-disk backup contract.
type Manifest struct {
	OperationID     string  `json:"operation_id"`
	ProviderID      string  `json:"provider_id"`
	ProfileID       string  `json:"profile_id"`
	PlanFingerprint string  `json:"plan_fingerprint"`
	CreatedAtUnixMS int64   `json:"created_at_unix_ms"`
	Entries         []Entry `json:"entries"`
}

// Backup is the private location and manifest entries created for an operation.
type Backup struct {
	Path    string
	Entries []Entry
}

// BackupRequest describes one operation snapshot to preserve before mutation.
type BackupRequest struct {
	BackupsDir      string
	OperationID     string
	ProviderID      string
	ProfileID       string
	PlanFingerprint string
	Operations      []Operation
}

// Executor is an immutable target-backend dependency injection point.
type Executor struct {
	registry target.Registry
	now      func() time.Time
}

func New(registry target.Registry) Executor {
	return Executor{registry: registry, now: time.Now}
}

// WithClock exists for deterministic unit tests; it preserves registry immutability.
func (executor Executor) WithClock(now func() time.Time) Executor {
	if now != nil {
		executor.now = now
	}
	return executor
}

// VerifyPlan re-reads every prepared target before backup or apply.
func (executor Executor) VerifyPlan(ctx context.Context, operations []Operation) error {
	for _, operation := range operations {
		if operation.Action == ActionUnsupported {
			continue
		}
		if operation.Spec == nil {
			return apperror.New(apperror.SwitchPlanUnsupported, "switch target spec is missing").WithDetail("target_id", operation.TargetID)
		}
		backend, err := executor.backend(operation.BackendID, apperror.SwitchPlanUnsupported)
		if err != nil {
			return err
		}
		if err := backend.Verify(ctx, operation.Spec, operation.Snapshot); err != nil {
			return err
		}
	}
	return nil
}

// CreateBackup creates and writes a private manifest before any target mutation.
func (executor Executor) CreateBackup(ctx context.Context, req BackupRequest) (Backup, error) {
	backupPath := filepath.Join(req.BackupsDir, req.OperationID)
	filesPath := filepath.Join(backupPath, "files")
	secretsPath := filepath.Join(backupPath, "secrets")
	if err := os.MkdirAll(req.BackupsDir, 0o700); err != nil {
		return Backup{}, apperror.Wrap(apperror.BackupFailed, "failed to create backup directory", err).WithDetail("path", backupPath)
	}
	// Claim the operation directory exclusively so failure cleanup cannot remove
	// a backup created by another operation or an earlier process.
	if err := os.Mkdir(backupPath, 0o700); err != nil {
		return Backup{}, apperror.Wrap(apperror.BackupFailed, "failed to create operation backup directory", err).WithDetail("path", backupPath)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(backupPath)
		}
	}()
	chmodBestEffort(backupPath, 0o700)
	if err := os.Mkdir(filesPath, 0o700); err != nil {
		return Backup{}, apperror.Wrap(apperror.BackupFailed, "failed to create file backup directory", err).WithDetail("path", backupPath)
	}
	chmodBestEffort(filesPath, 0o700)
	if err := os.Mkdir(secretsPath, 0o700); err != nil {
		return Backup{}, apperror.Wrap(apperror.BackupFailed, "failed to create credential backup directory", err)
	}
	chmodBestEffort(secretsPath, 0o700)

	backup := Backup{Path: backupPath, Entries: []Entry{}}
	for _, operation := range req.Operations {
		keepsPrivateLocator := operation.Action == ActionNoop && operation.Snapshot.OpaqueState != ""
		if operation.Action != ActionCreate && operation.Action != ActionUpdate && !keepsPrivateLocator {
			continue
		}
		entry := Entry{
			TargetID: operation.TargetID, BackendID: operation.BackendID, TargetLabel: operation.TargetLabel,
			Path: operation.Path, Action: operation.Action, Existed: operation.FileExists,
			BeforeSHA256: operation.Snapshot.Fingerprint, Mode: fileModeString(operation.BeforeMode),
			PrivateLocator: operation.Snapshot.OpaqueState,
		}
		if operation.FileExists && operation.Action != ActionNoop {
			if operation.Spec == nil {
				return Backup{}, apperror.New(apperror.BackupFailed, "switch target spec is missing").WithDetail("target_id", operation.TargetID)
			}
			backend, err := executor.backend(operation.BackendID, apperror.BackupFailed)
			if err != nil {
				return Backup{}, err
			}
			directory := "files"
			if operation.Spec.Sensitive() && operation.Spec.BackendID() != target.BackendFile {
				directory = "secrets"
			}
			relPath := filepath.Join(directory, operation.TargetID+".bak")
			copiedSHA, err := backend.Backup(ctx, operation.Spec, operation.Snapshot, filepath.Join(backupPath, relPath))
			if err != nil {
				return Backup{}, err
			}
			if copiedSHA != operation.Snapshot.Fingerprint {
				return Backup{}, apperror.New(apperror.BackupFailed, "backup hash does not match planned target hash").
					WithDetail("target_id", operation.TargetID).WithDetail("backend_id", operation.BackendID)
			}
			entry.BackupRelPath = filepath.ToSlash(relPath)
		}
		backup.Entries = append(backup.Entries, entry)
	}

	manifest := Manifest{
		OperationID: req.OperationID, ProviderID: req.ProviderID, ProfileID: req.ProfileID,
		PlanFingerprint: req.PlanFingerprint, CreatedAtUnixMS: executor.now().UnixMilli(), Entries: backup.Entries,
	}
	if err := WriteManifest(backupPath, manifest); err != nil {
		return Backup{}, err
	}
	complete = true
	return backup, nil
}

// Apply writes one target only after the caller's lifecycle has recorded its backup checkpoint.
func (executor Executor) Apply(ctx context.Context, operation Operation) error {
	if operation.Spec == nil {
		return apperror.New(apperror.SwitchPlanUnsupported, "switch target spec is missing").WithDetail("target_id", operation.TargetID)
	}
	backend, err := executor.backend(operation.BackendID, apperror.SwitchPlanUnsupported)
	if err != nil {
		return err
	}
	return backend.Apply(ctx, operation.Spec, operation.Snapshot, operation.DesiredContent, operation.DesiredMode, operation.UseDesiredMode)
}

// VerifyApplied confirms final desired content after all writes.
func (executor Executor) VerifyApplied(ctx context.Context, operations []Operation) error {
	for _, operation := range operations {
		if operation.Action == ActionUnsupported {
			continue
		}
		if operation.Spec == nil {
			return apperror.New(apperror.SwitchPlanUnsupported, "switch target spec is missing").WithDetail("target_id", operation.TargetID)
		}
		backend, err := executor.backend(operation.BackendID, apperror.SwitchPlanUnsupported)
		if err != nil {
			return err
		}
		expected := operation.Snapshot
		if operation.Action == ActionCreate || operation.Action == ActionUpdate {
			expected.Exists = true
			expected.IsSymlink = false
			expected.Fingerprint = target.SHA256String(operation.DesiredContent)
		}
		if err := backend.Verify(ctx, operation.Spec, expected); err != nil {
			return err
		}
		// Windows exposes only a read-only attribute through os.FileMode, so an
		// exact POSIX permission comparison would reject successful writes.
		if operation.UseDesiredMode && expected.Exists && runtime.GOOS != "windows" {
			state, err := backend.Inspect(ctx, operation.Spec)
			if err != nil {
				return err
			}
			if state.Mode.Perm() != operation.DesiredMode.Perm() {
				return apperror.New(apperror.TargetWriteFailed, "target permissions could not be verified").
					WithDetail("target_id", operation.TargetID).
					WithDetail("backend_id", operation.BackendID)
			}
		}
	}
	return nil
}

// Restore applies one private backup entry to a verified current target.
func (executor Executor) Restore(ctx context.Context, backendID string, spec target.Spec, current target.Snapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	if spec == nil {
		return apperror.New(apperror.RollbackUnsupported, "rollback target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RollbackUnsupported)
	if err != nil {
		return err
	}
	return backend.Restore(ctx, spec, current, sourcePath, sourceSHA, mode, useMode)
}

// Remove deletes one verified target when rolling back a create operation.
func (executor Executor) Remove(ctx context.Context, backendID string, spec target.Spec, current target.Snapshot, allowMissing bool) (bool, error) {
	if spec == nil {
		return false, apperror.New(apperror.RollbackUnsupported, "rollback target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RollbackUnsupported)
	if err != nil {
		return false, err
	}
	return backend.Remove(ctx, spec, current, allowMissing)
}

// Verify validates one recovery expectation against the owning backend.
func (executor Executor) Verify(ctx context.Context, backendID string, spec target.Spec, expected target.Snapshot) error {
	if spec == nil {
		return apperror.New(apperror.RollbackUnsupported, "rollback target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RollbackUnsupported)
	if err != nil {
		return err
	}
	return backend.Verify(ctx, spec, expected)
}

// Inspect reads one target through the selected backend.
func (executor Executor) Inspect(ctx context.Context, backendID string, spec target.Spec) (target.Snapshot, error) {
	if spec == nil {
		return target.Snapshot{}, apperror.New(apperror.RollbackUnsupported, "rollback target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RollbackUnsupported)
	if err != nil {
		return target.Snapshot{}, err
	}
	return backend.Inspect(ctx, spec)
}

func (executor Executor) backend(id string, code apperror.Code) (target.Backend, error) {
	backend, ok := executor.registry.Backend(id)
	if !ok {
		return nil, apperror.New(code, "target backend is unavailable").WithDetail("backend_id", id)
	}
	return backend, nil
}

// WriteManifest uses a private, exclusive file to prevent mixed backup states.
func WriteManifest(backupPath string, manifest Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to encode backup manifest", err)
	}
	raw = append(raw, '\n')
	manifestPath := filepath.Join(backupPath, "manifest.json")
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to write backup manifest", err).WithDetail("path", backupPath)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(manifestPath)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to write backup manifest", err).WithDetail("path", backupPath)
	}
	if err := file.Sync(); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to sync backup manifest", err).WithDetail("path", backupPath)
	}
	if err := file.Close(); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to close backup manifest", err).WithDetail("path", backupPath)
	}
	remove = false
	return nil
}

func LoadManifest(backupPath string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(backupPath, "manifest.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, apperror.New(apperror.BackupNotFound, "backup manifest not found").WithDetail("path", backupPath)
		}
		return Manifest{}, apperror.Wrap(apperror.BackupFailed, "failed to read backup manifest", err).WithDetail("path", backupPath)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, apperror.Wrap(apperror.BackupInvalid, "backup manifest is invalid", err).WithDetail("path", backupPath)
	}
	return manifest, nil
}

func chmodBestEffort(path string, mode os.FileMode) {
	_ = os.Chmod(path, mode)
}

func fileModeString(mode os.FileMode) string {
	if mode == 0 {
		return ""
	}
	return fmt.Sprintf("%#o", mode.Perm())
}
