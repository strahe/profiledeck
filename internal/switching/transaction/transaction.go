package transaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	maxManifestBytes  = 16 * 1024 * 1024
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

// Entry is the stable, private recovery-manifest representation of one target.
type Entry struct {
	TargetID        string `json:"target_id"`
	BackendID       string `json:"backend_id"`
	TargetLabel     string `json:"target_label"`
	Path            string `json:"path"`
	Action          string `json:"action"`
	Existed         bool   `json:"existed"`
	BeforeSHA256    string `json:"before_sha256"`
	Mode            string `json:"mode"`
	RecoveryRelPath string `json:"recovery_rel_path"`
	// PrivateLocator is backend-owned recovery state. It must not enter DTOs.
	PrivateLocator string `json:"private_locator,omitempty"`
}

// Manifest is the stable on-disk operation recovery contract.
type Manifest struct {
	OperationID     string  `json:"operation_id"`
	ProviderID      string  `json:"provider_id"`
	ProfileID       string  `json:"profile_id"`
	PlanFingerprint string  `json:"plan_fingerprint"`
	CreatedAtUnixMS int64   `json:"created_at_unix_ms"`
	Entries         []Entry `json:"entries"`
}

// RecoveryPoint is the private state captured before an operation mutates targets.
type RecoveryPoint struct {
	Path    string
	Entries []Entry
}

type RecoveryPointRequest struct {
	RecoveryRoot    string
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

// VerifyPlan re-reads every prepared target before recovery capture or apply.
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

// CreateRecoveryPoint captures private target state before any target mutation.
func (executor Executor) CreateRecoveryPoint(ctx context.Context, req RecoveryPointRequest) (RecoveryPoint, error) {
	recoveryPath := filepath.Join(req.RecoveryRoot, req.OperationID)
	rootInfo, err := os.Lstat(req.RecoveryRoot)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		// The cleanup service exclusively creates and validates this root before
		// switching reaches recovery capture; never recreate or follow it here.
		return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "operation recovery directory is unavailable")
	}
	// Claim the operation directory exclusively so failure cleanup cannot remove
	// recovery state created by another operation or an earlier process.
	if err := os.Mkdir(recoveryPath, 0o700); err != nil {
		return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "operation recovery point could not be created")
	}
	complete := false
	defer func() {
		current, err := os.Lstat(req.RecoveryRoot)
		if !complete && err == nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(rootInfo, current) {
			_ = os.RemoveAll(recoveryPath)
		}
	}()
	chmodBestEffort(recoveryPath, 0o700)

	recovery := RecoveryPoint{Path: recoveryPath, Entries: []Entry{}}
	for index, operation := range req.Operations {
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
				return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "switch target spec is missing").WithDetail("target_id", operation.TargetID)
			}
			backend, err := executor.backend(operation.BackendID, apperror.BackupFailed)
			if err != nil {
				return RecoveryPoint{}, err
			}
			relPath := fmt.Sprintf("target-%03d.recovery", index)
			copiedSHA, err := backend.Backup(ctx, operation.Spec, operation.Snapshot, filepath.Join(recoveryPath, relPath))
			if err != nil {
				return RecoveryPoint{}, safeBackendError(
					err, apperror.BackupFailed, "target recovery state could not be captured", operation.TargetID, operation.BackendID,
				)
			}
			if copiedSHA != operation.Snapshot.Fingerprint {
				return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "recovery copy hash does not match planned target hash").
					WithDetail("target_id", operation.TargetID).WithDetail("backend_id", operation.BackendID)
			}
			entry.RecoveryRelPath = filepath.ToSlash(relPath)
		}
		recovery.Entries = append(recovery.Entries, entry)
	}

	manifest := Manifest{
		OperationID: req.OperationID, ProviderID: req.ProviderID, ProfileID: req.ProfileID,
		PlanFingerprint: req.PlanFingerprint, CreatedAtUnixMS: executor.now().UnixMilli(), Entries: recovery.Entries,
	}
	if err := WriteManifest(recoveryPath, manifest); err != nil {
		return RecoveryPoint{}, err
	}
	if err := syncRecoveryDirectory(recoveryPath); err != nil {
		return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "operation recovery point could not be finalized")
	}
	currentRoot, err := os.Lstat(req.RecoveryRoot)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(rootInfo, currentRoot) {
		return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "operation recovery directory changed")
	}
	if err := syncRecoveryDirectory(req.RecoveryRoot); err != nil {
		return RecoveryPoint{}, apperror.New(apperror.BackupFailed, "operation recovery point could not be finalized")
	}
	complete = true
	return recovery, nil
}

// Apply writes one target only after the caller has recorded its recovery checkpoint.
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

// Restore applies one private recovery entry to a verified current target.
func (executor Executor) Restore(ctx context.Context, backendID string, spec target.Spec, current target.Snapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	if spec == nil {
		return apperror.New(apperror.RecoveryUnsupported, "recovery target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RecoveryUnsupported)
	if err != nil {
		return err
	}
	if err := backend.Restore(ctx, spec, current, sourcePath, sourceSHA, mode, useMode); err != nil {
		return safeBackendError(err, apperror.BackupInvalid, "target recovery state could not be restored", spec.TargetID(), backendID)
	}
	return nil
}

// Remove deletes one verified target when recovering a create operation.
func (executor Executor) Remove(ctx context.Context, backendID string, spec target.Spec, current target.Snapshot, allowMissing bool) (bool, error) {
	if spec == nil {
		return false, apperror.New(apperror.RecoveryUnsupported, "recovery target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RecoveryUnsupported)
	if err != nil {
		return false, err
	}
	return backend.Remove(ctx, spec, current, allowMissing)
}

// Verify validates one recovery expectation against the owning backend.
func (executor Executor) Verify(ctx context.Context, backendID string, spec target.Spec, expected target.Snapshot) error {
	if spec == nil {
		return apperror.New(apperror.RecoveryUnsupported, "recovery target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RecoveryUnsupported)
	if err != nil {
		return err
	}
	return backend.Verify(ctx, spec, expected)
}

// Inspect reads one target through the selected backend.
func (executor Executor) Inspect(ctx context.Context, backendID string, spec target.Spec) (target.Snapshot, error) {
	if spec == nil {
		return target.Snapshot{}, apperror.New(apperror.RecoveryUnsupported, "recovery target spec is missing")
	}
	backend, err := executor.backend(backendID, apperror.RecoveryUnsupported)
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

func safeBackendError(err error, fallbackCode apperror.Code, fallbackMessage, targetID, backendID string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	code := fallbackCode
	message := fallbackMessage
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		if appErr.Code != "" {
			code = appErr.Code
		}
		if appErr.Message != "" {
			message = appErr.Message
		}
	}
	return apperror.New(code, message).WithDetail("target_id", targetID).WithDetail("backend_id", backendID)
}

// WriteManifest uses a private, exclusive file to prevent mixed recovery states.
func WriteManifest(recoveryPath string, manifest Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return apperror.New(apperror.BackupFailed, "operation recovery manifest could not be encoded")
	}
	raw = append(raw, '\n')
	manifestPath := filepath.Join(recoveryPath, "manifest.json")
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return apperror.New(apperror.BackupFailed, "operation recovery manifest could not be created")
	}
	remove := true
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if remove {
			_ = os.Remove(manifestPath)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return apperror.New(apperror.BackupFailed, "operation recovery manifest could not be written")
	}
	if err := file.Sync(); err != nil {
		return apperror.New(apperror.BackupFailed, "operation recovery manifest could not be finalized")
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return apperror.New(apperror.BackupFailed, "operation recovery manifest could not be finalized")
	}
	remove = false
	return nil
}

func LoadRecoveryManifest(recoveryPath string) (Manifest, error) {
	manifestPath := filepath.Join(recoveryPath, "manifest.json")
	info, err := os.Lstat(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, apperror.New(apperror.BackupNotFound, "operation recovery manifest not found")
		}
		return Manifest{}, apperror.New(apperror.BackupFailed, "operation recovery manifest could not be inspected")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxManifestBytes {
		return Manifest{}, apperror.New(apperror.BackupInvalid, "operation recovery manifest is invalid")
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return Manifest{}, apperror.New(apperror.BackupFailed, "operation recovery manifest could not be read")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, apperror.New(apperror.BackupInvalid, "operation recovery manifest is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, apperror.New(apperror.BackupInvalid, "operation recovery manifest is invalid")
	}
	return manifest, nil
}

func syncRecoveryDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
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
