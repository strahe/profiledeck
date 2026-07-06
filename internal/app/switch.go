package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

const switchOperationRandomBytes = 6

type ApplySwitchRequest struct {
	ConfigDir               string
	ProviderID              string
	ProfileID               string
	Confirm                 bool
	ExpectedPlanFingerprint string
}

type ApplySwitchResult struct {
	OperationID     string       `json:"operation_id"`
	Status          string       `json:"status"`
	Provider        PlanProvider `json:"provider"`
	Profile         PlanProfile  `json:"profile"`
	PlanFingerprint string       `json:"plan_fingerprint"`
	Counts          SwitchCounts `json:"counts"`
	BackupPath      string       `json:"backup_path"`
	Warnings        []string     `json:"warnings"`
}

type SwitchCounts struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Noop   int `json:"noop"`
}

type switchBackup struct {
	Path    string
	Entries []switchBackupEntry
}

type switchBackupManifest struct {
	OperationID     string              `json:"operation_id"`
	ProviderID      string              `json:"provider_id"`
	ProfileID       string              `json:"profile_id"`
	PlanFingerprint string              `json:"plan_fingerprint"`
	CreatedAtUnixMS int64               `json:"created_at_unix_ms"`
	Entries         []switchBackupEntry `json:"entries"`
}

type switchBackupEntry struct {
	TargetID      string `json:"target_id"`
	Path          string `json:"path"`
	Action        string `json:"action"`
	Existed       bool   `json:"existed"`
	BeforeSHA256  string `json:"before_sha256"`
	Mode          string `json:"mode"`
	BackupRelPath string `json:"backup_rel_path"`
}

type switchOperationMetadata struct {
	Checkpoint      string                          `json:"checkpoint"`
	ProviderID      string                          `json:"provider_id"`
	ProfileID       string                          `json:"profile_id"`
	PlanFingerprint string                          `json:"plan_fingerprint,omitempty"`
	BackupPath      string                          `json:"backup_path,omitempty"`
	Counts          SwitchCounts                    `json:"counts"`
	Targets         []switchOperationTargetMetadata `json:"targets,omitempty"`
	Warnings        []string                        `json:"warnings,omitempty"`
	UpdatedAtUnixMS int64                           `json:"updated_at_unix_ms"`
}

type switchOperationTargetMetadata struct {
	TargetID      string   `json:"target_id"`
	Path          string   `json:"path"`
	Format        string   `json:"format"`
	Strategy      string   `json:"strategy"`
	Action        string   `json:"action"`
	StatusReason  string   `json:"status_reason"`
	FileExists    bool     `json:"file_exists"`
	BeforeSHA256  string   `json:"before_sha256,omitempty"`
	DesiredSHA256 string   `json:"desired_sha256,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type switchLock struct {
	path  string
	token string
}

func ApplySwitch(ctx context.Context, req ApplySwitchRequest) (ApplySwitchResult, error) {
	providerID, appErr := validateID(req.ProviderID, ErrorProviderInvalid)
	if appErr != nil {
		return ApplySwitchResult{}, appErr
	}
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return ApplySwitchResult{}, appErr
	}
	if !req.Confirm {
		return ApplySwitchResult{}, NewError(ErrorConfirmationRequired, "switch apply requires confirmation")
	}

	_, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return ApplySwitchResult{}, err
	}
	if err := createRuntimeDirs(paths); err != nil {
		return ApplySwitchResult{}, WrapError(ErrorRuntimeInitFailed, "failed to initialize runtime directories", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return ApplySwitchResult{}, err
	}
	defer db.Close()

	operationID, err := newSwitchOperationID(time.Now())
	if err != nil {
		return ApplySwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to create switch operation id", err)
	}
	initialMetadata, err := marshalSwitchOperationMetadata("created", providerID, profileID, applyPlan{}, switchBackup{}, SwitchCounts{})
	if err != nil {
		return ApplySwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to encode switch operation metadata", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		MetadataJSON: initialMetadata,
	}); err != nil {
		return ApplySwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to create switch operation", err)
	}

	lock, err := acquireSwitchLock(paths.Lock, operationID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	defer lock.Release()

	plan, err := buildApplyPlan(ctx, db, providerID, profileID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	counts := countSwitchOperations(plan.Operations)
	plannedMetadata, err := marshalSwitchOperationMetadata("planned", providerID, profileID, plan, switchBackup{}, counts)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, plannedMetadata); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update switch operation metadata", err))
	}

	expectedFingerprint := strings.TrimSpace(req.ExpectedPlanFingerprint)
	if expectedFingerprint != "" && expectedFingerprint != plan.SwitchPlan.PlanFingerprint {
		err := NewError(ErrorTargetChanged, "switch plan changed after preview").
			WithDetail("expected_plan_fingerprint", expectedFingerprint).
			WithDetail("actual_plan_fingerprint", plan.SwitchPlan.PlanFingerprint)
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	if err := rejectUnsupportedSwitchOperations(plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	if err := verifySwitchPlanHashes(ctx, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}

	backup, err := createSwitchBackup(ctx, paths, operationID, plan)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	backupMetadata, err := marshalSwitchOperationMetadata("backed_up", providerID, profileID, plan, backup, counts)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, backupMetadata); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update switch operation metadata", err))
	}
	if err := verifySwitchPlanHashes(ctx, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, err)
	}

	for _, op := range plan.Operations {
		if op.Action != planActionCreate && op.Action != planActionUpdate {
			continue
		}
		if err := writeTargetAtomic(ctx, op); err != nil {
			return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, err)
		}
	}

	appliedMetadata, err := marshalSwitchOperationMetadata("applied", providerID, profileID, plan, backup, counts)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		ProviderID:   providerID,
		MetadataJSON: appliedMetadata,
	}); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, WrapError(ErrorOperationUpdateFailed, "failed to complete switch operation", err))
	}

	return ApplySwitchResult{
		OperationID:     operationID,
		Status:          store.OperationStatusApplied,
		Provider:        plan.SwitchPlan.Provider,
		Profile:         plan.SwitchPlan.Profile,
		PlanFingerprint: plan.SwitchPlan.PlanFingerprint,
		Counts:          counts,
		BackupPath:      backup.Path,
		Warnings:        plan.SwitchPlan.Warnings,
	}, nil
}

func newSwitchOperationID(now time.Time) (string, error) {
	randomBytes := make([]byte, switchOperationRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("switch-%d-%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func acquireSwitchLock(path string, operationID string) (switchLock, error) {
	token := fmt.Sprintf("%s\npid=%d\ncreated_at_unix_ms=%d\n", operationID, os.Getpid(), time.Now().UnixMilli())
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return switchLock{}, NewError(ErrorLockAcquireFailed, "switch lock is already held").WithDetail("path", path)
		}
		return switchLock{}, WrapError(ErrorLockAcquireFailed, "failed to acquire switch lock", err).WithDetail("path", path)
	}
	if _, err := io.WriteString(file, token); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return switchLock{}, WrapError(ErrorLockAcquireFailed, "failed to write switch lock", err).WithDetail("path", path)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return switchLock{}, WrapError(ErrorLockAcquireFailed, "failed to close switch lock", err).WithDetail("path", path)
	}
	return switchLock{path: path, token: token}, nil
}

func (l switchLock) Release() {
	if l.path == "" {
		return
	}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	if string(raw) != l.token {
		return
	}
	_ = os.Remove(l.path)
}

func rejectUnsupportedSwitchOperations(operations []applyPlanOperation) error {
	for _, op := range operations {
		if op.Action != planActionUnsupported {
			continue
		}
		return NewError(ErrorSwitchPlanUnsupported, "switch plan contains unsupported target operation").
			WithDetail("target_id", op.TargetID).
			WithDetail("path", op.Path).
			WithDetail("reason", op.StatusReason)
	}
	return nil
}

func verifySwitchPlanHashes(ctx context.Context, operations []applyPlanOperation) error {
	for _, op := range operations {
		if err := verifySingleSwitchPlanHash(ctx, op); err != nil {
			return err
		}
	}
	return nil
}

func verifySingleSwitchPlanHash(ctx context.Context, op applyPlanOperation) error {
	if op.Action == planActionUnsupported {
		return nil
	}
	current, err := readTargetForPlan(ctx, op.Path, false)
	if err != nil {
		return WrapError(ErrorTargetChanged, "failed to verify target hash", err).
			WithDetail("target_id", op.TargetID).
			WithDetail("path", op.Path)
	}
	if !op.FileExists {
		if current.FileExists {
			return targetChangedError(op, "target appeared after plan")
		}
		return nil
	}
	if !current.FileExists {
		return targetChangedError(op, "target disappeared after plan")
	}
	if current.IsSymlink {
		return targetChangedError(op, "target became a symlink after plan")
	}
	if current.SHA256 != op.BeforeSHA256 {
		return targetChangedError(op, "target content changed after plan")
	}
	return nil
}

func targetChangedError(op applyPlanOperation, message string) *AppError {
	return NewError(ErrorTargetChanged, message).
		WithDetail("target_id", op.TargetID).
		WithDetail("path", op.Path)
}

func createSwitchBackup(ctx context.Context, paths runtime.Paths, operationID string, plan applyPlan) (switchBackup, error) {
	backupPath := filepath.Join(paths.Backups, operationID)
	filesPath := filepath.Join(backupPath, "files")
	if err := os.MkdirAll(filesPath, 0o700); err != nil {
		return switchBackup{}, WrapError(ErrorBackupFailed, "failed to create backup directory", err).WithDetail("path", backupPath)
	}

	backup := switchBackup{Path: backupPath, Entries: []switchBackupEntry{}}
	for _, op := range plan.Operations {
		if op.Action != planActionCreate && op.Action != planActionUpdate {
			continue
		}
		entry := switchBackupEntry{
			TargetID:     op.TargetID,
			Path:         op.Path,
			Action:       op.Action,
			Existed:      op.FileExists,
			BeforeSHA256: op.BeforeSHA256,
			Mode:         fileModeString(op.BeforeMode),
		}
		if op.FileExists {
			relPath := filepath.Join("files", op.TargetID+".bak")
			copiedSHA, err := copyBackupFile(ctx, op.Path, filepath.Join(backupPath, relPath))
			if err != nil {
				return switchBackup{}, err
			}
			if copiedSHA != op.BeforeSHA256 {
				return switchBackup{}, NewError(ErrorBackupFailed, "backup hash does not match planned target hash").
					WithDetail("target_id", op.TargetID).
					WithDetail("path", op.Path)
			}
			entry.BackupRelPath = filepath.ToSlash(relPath)
		}
		backup.Entries = append(backup.Entries, entry)
	}

	manifest := switchBackupManifest{
		OperationID:     operationID,
		ProviderID:      plan.SwitchPlan.Provider.ID,
		ProfileID:       plan.SwitchPlan.Profile.ID,
		PlanFingerprint: plan.SwitchPlan.PlanFingerprint,
		CreatedAtUnixMS: time.Now().UnixMilli(),
		Entries:         backup.Entries,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return switchBackup{}, WrapError(ErrorBackupFailed, "failed to encode backup manifest", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(backupPath, "manifest.json"), raw, 0o600); err != nil {
		return switchBackup{}, WrapError(ErrorBackupFailed, "failed to write backup manifest", err).WithDetail("path", backupPath)
	}
	return backup, nil
}

func copyBackupFile(ctx context.Context, source string, destination string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to inspect target for backup", err).WithDetail("path", source)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
		return "", NewError(ErrorBackupFailed, "target is not a regular file").WithDetail("path", source)
	}

	input, err := os.Open(source)
	if err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to open target for backup", err).WithDetail("path", source)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to create backup file", err).WithDetail("path", destination)
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
	if _, err := io.Copy(io.MultiWriter(output, hash), contextReader{ctx: ctx, reader: input}); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to copy backup file", err).WithDetail("path", source)
	}
	if err := output.Sync(); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to sync backup file", err).WithDetail("path", destination)
	}
	if err := output.Close(); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to close backup file", err).WithDetail("path", destination)
	}
	output = nil
	removeOutput = false
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeTargetAtomic(ctx context.Context, op applyPlanOperation) error {
	if err := verifySingleSwitchPlanHash(ctx, op); err != nil {
		return err
	}

	parent := filepath.Dir(op.Path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to inspect target parent directory", err).WithDetail("path", parent)
	}
	if !parentInfo.IsDir() {
		return NewError(ErrorTargetWriteFailed, "target parent path is not a directory").WithDetail("path", parent)
	}

	mode := os.FileMode(0o600)
	if op.FileExists {
		info, err := os.Lstat(op.Path)
		if err != nil {
			return WrapError(ErrorTargetChanged, "failed to inspect target before write", err).WithDetail("path", op.Path)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
			return NewError(ErrorTargetChanged, "target is not a regular file before write").WithDetail("path", op.Path)
		}
		mode = info.Mode().Perm()
	} else {
		if _, err := os.Lstat(op.Path); err == nil {
			return targetChangedError(op, "target appeared before write")
		} else if !errors.Is(err, os.ErrNotExist) {
			return WrapError(ErrorTargetChanged, "failed to inspect target before write", err).WithDetail("path", op.Path)
		}
	}

	temp, err := os.CreateTemp(parent, ".profiledeck-*")
	if err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to create temporary target file", err).WithDetail("path", parent)
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
		return WrapError(ErrorTargetWriteFailed, "failed to set temporary target mode", err).WithDetail("path", tempPath)
	}
	if _, err := io.Copy(temp, contextReader{ctx: ctx, reader: strings.NewReader(op.DesiredContent)}); err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to write temporary target file", err).WithDetail("path", tempPath)
	}
	if err := temp.Sync(); err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to sync temporary target file", err).WithDetail("path", tempPath)
	}
	if err := temp.Close(); err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to close temporary target file", err).WithDetail("path", tempPath)
	}
	temp = nil
	if err := os.Rename(tempPath, op.Path); err != nil {
		return WrapError(ErrorTargetWriteFailed, "failed to replace target file", err).WithDetail("path", op.Path)
	}
	removeTemp = false
	syncParentDirBestEffort(parent)
	return nil
}

func syncParentDirBestEffort(parent string) {
	dir, err := os.Open(parent)
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}

func failSwitchOperation(ctx context.Context, db *store.Store, operationID string, metadataJSON string, operationErr error) error {
	code, message := errorCodeAndMessage(operationErr)
	cleanupCtx, cancel := switchCleanupContext(ctx)
	defer cancel()

	if err := db.MarkOperationFailed(cleanupCtx, store.MarkOperationFailedParams{
		ID:           operationID,
		ErrorCode:    string(code),
		ErrorMessage: message,
		MetadataJSON: &metadataJSON,
	}); err != nil {
		return preserveSwitchOperationError(operationErr, err)
	}
	return operationErr
}

func switchCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
}

func preserveSwitchOperationError(operationErr error, updateErr error) error {
	if operationErr == nil {
		return WrapError(ErrorOperationUpdateFailed, "failed to mark switch operation failed", updateErr)
	}

	var appErr *AppError
	if errors.As(operationErr, &appErr) {
		return appErr.WithDetail("operation_update_error", updateErr.Error())
	}
	return WrapError(ErrorCommandFailed, "switch operation failed", operationErr).
		WithDetail("operation_update_error", updateErr.Error())
}

func errorCodeAndMessage(err error) (ErrorCode, string) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.Message
	}
	return ErrorCommandFailed, err.Error()
}

func countSwitchOperations(operations []applyPlanOperation) SwitchCounts {
	var counts SwitchCounts
	for _, op := range operations {
		switch op.Action {
		case planActionCreate:
			counts.Create++
		case planActionUpdate:
			counts.Update++
		case planActionNoop:
			counts.Noop++
		}
	}
	return counts
}

func marshalSwitchOperationMetadata(checkpoint string, providerID string, profileID string, plan applyPlan, backup switchBackup, counts SwitchCounts) (string, error) {
	targets := []switchOperationTargetMetadata{}
	for _, op := range plan.Operations {
		targets = append(targets, switchOperationTargetMetadata{
			TargetID:      op.TargetID,
			Path:          op.Path,
			Format:        op.Format,
			Strategy:      op.Strategy,
			Action:        op.Action,
			StatusReason:  op.StatusReason,
			FileExists:    op.FileExists,
			BeforeSHA256:  op.BeforeSHA256,
			DesiredSHA256: op.DesiredSHA256,
			Warnings:      op.Warnings,
		})
	}

	metadata := switchOperationMetadata{
		Checkpoint:      checkpoint,
		ProviderID:      providerID,
		ProfileID:       profileID,
		PlanFingerprint: plan.SwitchPlan.PlanFingerprint,
		BackupPath:      backup.Path,
		Counts:          counts,
		Targets:         targets,
		Warnings:        plan.SwitchPlan.Warnings,
		UpdatedAtUnixMS: time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func fileModeString(mode os.FileMode) string {
	if mode == 0 {
		return ""
	}
	return fmt.Sprintf("%#o", mode.Perm())
}
