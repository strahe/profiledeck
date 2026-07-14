package switching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
	"github.com/strahe/profiledeck/internal/validate"
)

const switchOperationRandomBytes = 6

type ApplySwitchRequest struct {
	ProviderID              string `json:"provider_id"`
	ProfileID               string `json:"profile_id"`
	Confirm                 bool   `json:"confirm"`
	ExpectedPlanFingerprint string `json:"expected_plan_fingerprint"`
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
	Entries []transaction.Entry
}

type switchOperationMetadata struct {
	Checkpoint      string                          `json:"checkpoint"`
	ProviderID      string                          `json:"provider_id"`
	ProfileID       string                          `json:"profile_id"`
	PlanFingerprint string                          `json:"plan_fingerprint,omitempty"`
	BackupPath      string                          `json:"backup_path,omitempty"`
	Counts          SwitchCounts                    `json:"counts"`
	PreviousActive  *switchPreviousActiveState      `json:"previous_active_state,omitempty"`
	StateCaptures   []StateCapture                  `json:"state_captures,omitempty"`
	Targets         []switchOperationTargetMetadata `json:"targets,omitempty"`
	Warnings        []string                        `json:"warnings,omitempty"`
	UpdatedAtUnixMS int64                           `json:"updated_at_unix_ms"`
}

type switchPreviousActiveState struct {
	Exists          bool   `json:"exists"`
	ProfileID       string `json:"profile_id,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms,omitempty"`
}

type switchOperationTargetMetadata struct {
	TargetID      string   `json:"target_id"`
	BackendID     string   `json:"backend_id"`
	TargetLabel   string   `json:"target_label"`
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

func (service *Service) Apply(ctx context.Context, req ApplySwitchRequest) (ApplySwitchResult, error) {
	providerID, appErr := validate.ID(req.ProviderID, apperror.ProviderInvalid)
	if appErr != nil {
		return ApplySwitchResult{}, appErr
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ApplySwitchResult{}, appErr
	}
	if !req.Confirm {
		return ApplySwitchResult{}, apperror.New(apperror.ConfirmationRequired, "switch apply requires confirmation")
	}
	if err := service.RequireProvider(ctx, providerID); err != nil {
		return ApplySwitchResult{}, err
	}

	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return ApplySwitchResult{}, err
	}
	defer db.Close()

	operationID, err := newSwitchOperationID(time.Now())
	if err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create switch operation id", err)
	}
	initialMetadata, err := marshalSwitchOperationMetadata("created", providerID, profileID, applyPlan{}, switchBackup{}, SwitchCounts{}, nil)
	if err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to encode switch operation metadata", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		MetadataJSON: initialMetadata,
	}); err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create switch operation", err)
	}

	lock, err := acquireSwitchLock(service.paths.Lock, operationID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	defer lock.Release()
	// A Desktop Agent may be disabled while this operation waits for the lock.
	// Once the lock is owned, later preference changes must not interrupt it.
	if err := service.RequireProvider(ctx, providerID); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}

	previousActive, err := readPreviousActiveState(ctx, db, providerID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}

	plan, err := service.buildApplyPlan(ctx, db, providerID, profileID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	counts := countSwitchOperations(plan.Operations)
	plannedMetadata, err := marshalSwitchOperationMetadata("planned", providerID, profileID, plan, switchBackup{}, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, plannedMetadata); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update switch operation metadata", err))
	}

	expectedFingerprint := strings.TrimSpace(req.ExpectedPlanFingerprint)
	if expectedFingerprint != "" && expectedFingerprint != plan.SwitchPlan.PlanFingerprint {
		err := apperror.New(apperror.TargetChanged, "switch plan changed after preview").
			WithDetail("expected_plan_fingerprint", expectedFingerprint).
			WithDetail("actual_plan_fingerprint", plan.SwitchPlan.PlanFingerprint)
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	if err := rejectUnsupportedSwitchOperations(plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	executor := service.transactionExecutor()
	if err := verifySwitchPlanHashesWithExecutor(ctx, executor, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}

	backup, err := createSwitchBackupWithExecutor(ctx, executor, service.paths, operationID, plan)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	backupMetadata, err := marshalSwitchOperationMetadata("backed_up", providerID, profileID, plan, backup, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, backupMetadata); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update switch operation metadata", err))
	}
	if err := verifySwitchPlanHashesWithExecutor(ctx, executor, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, err)
	}

	for _, op := range plan.Operations {
		if op.Action != planActionCreate && op.Action != planActionUpdate {
			continue
		}
		if err := writeTargetAtomicWithExecutor(ctx, executor, op); err != nil {
			return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, err)
		}
	}
	// External writes and active-state commit are not one storage transaction.
	// Re-read every target after all writes so an already failed or replaced
	// working copy does not advance active state; external writers can still race
	// after this final read.
	if err := verifyAppliedSwitchTargetsWithExecutor(ctx, executor, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, err)
	}

	appliedMetadata, err := marshalSwitchOperationMetadata("applied", providerID, profileID, plan, backup, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:                operationID,
		ProfileID:         profileID,
		ProviderID:        providerID,
		MetadataJSON:      appliedMetadata,
		CredentialUpdates: plan.CredentialUpdates,
		ConfigSetUpdates:  plan.ConfigSetUpdates,
	}); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, backupMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to complete switch operation", err))
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

func readPreviousActiveState(ctx context.Context, db *store.Store, providerID string) (*switchPreviousActiveState, error) {
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, providerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &switchPreviousActiveState{Exists: false}, nil
		}
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read previous active state", err)
	}
	return &switchPreviousActiveState{
		Exists:          true,
		ProfileID:       activeState.ProfileID,
		OperationID:     activeState.OperationID,
		UpdatedAtUnixMS: activeState.UpdatedAtUnixMS,
	}, nil
}

func acquireSwitchLock(path, operationID string) (targetfs.Lock, error) {
	lock, err := targetfs.AcquireLock(path, operationID)
	if err != nil {
		return targetfs.Lock{}, mapTargetFSError(err)
	}
	return lock, nil
}

func rejectUnsupportedSwitchOperations(operations []applyPlanOperation) error {
	for _, op := range operations {
		if op.Action != planActionUnsupported {
			continue
		}
		return apperror.New(apperror.SwitchPlanUnsupported, "switch plan contains unsupported target operation").
			WithDetail("target_id", op.TargetID).
			WithDetail("path", op.Path).
			WithDetail("reason", op.StatusReason)
	}
	return nil
}

func (service *Service) verifySwitchPlanHashes(ctx context.Context, operations []applyPlanOperation) error {
	return verifySwitchPlanHashesWithExecutor(ctx, service.transactionExecutor(), operations)
}

func verifySwitchPlanHashesWithExecutor(ctx context.Context, executor transaction.Executor, operations []applyPlanOperation) error {
	return executor.VerifyPlan(ctx, transactionOperations(operations))
}

func verifyAppliedSwitchTargetsWithExecutor(ctx context.Context, executor transaction.Executor, operations []applyPlanOperation) error {
	return executor.VerifyApplied(ctx, transactionOperations(operations))
}

func createSwitchBackupWithExecutor(ctx context.Context, executor transaction.Executor, paths runtime.Paths, operationID string, plan applyPlan) (switchBackup, error) {
	backup, err := executor.CreateBackup(ctx, transaction.BackupRequest{
		BackupsDir: paths.Backups, OperationID: operationID, ProviderID: plan.SwitchPlan.Provider.ID,
		ProfileID: plan.SwitchPlan.Profile.ID, PlanFingerprint: plan.SwitchPlan.PlanFingerprint,
		Operations: transactionOperations(plan.Operations),
	})
	if err != nil {
		return switchBackup{}, err
	}
	return switchBackup{Path: backup.Path, Entries: backup.Entries}, nil
}

func writeTargetAtomicWithExecutor(ctx context.Context, executor transaction.Executor, op applyPlanOperation) error {
	return executor.Apply(ctx, transactionOperationFromApplyPlan(op))
}

func (service *Service) transactionExecutor() transaction.Executor {
	return transaction.New(service.dependencies.Targets)
}

func transactionOperations(operations []applyPlanOperation) []transaction.Operation {
	result := make([]transaction.Operation, 0, len(operations))
	for _, operation := range operations {
		result = append(result, transactionOperationFromApplyPlan(operation))
	}
	return result
}

func transactionOperationFromApplyPlan(operation applyPlanOperation) transaction.Operation {
	return transaction.Operation{
		TargetID: operation.TargetID, BackendID: operation.BackendID, TargetLabel: operation.TargetLabel,
		Path: firstNonEmpty(operation.Path, operation.privateRecoveryLocator), Action: operation.Action,
		FileExists:     operation.FileExists,
		BeforeSHA256:   firstNonEmpty(operation.privateBeforeFingerprint, operation.BeforeSHA256),
		DesiredSHA256:  firstNonEmpty(operation.privateDesiredFingerprint, operation.DesiredSHA256),
		DesiredContent: operation.DesiredContent, BeforeMode: operation.BeforeMode,
		DesiredMode: operation.DesiredMode, UseDesiredMode: operation.UseDesiredMode,
		Spec: operation.Spec, Snapshot: switchingSnapshotFromTarget(operation.Snapshot),
	}
}

func failSwitchOperation(ctx context.Context, db *store.Store, operationID, metadataJSON string, operationErr error) error {
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

func preserveSwitchOperationError(operationErr, updateErr error) error {
	if operationErr == nil {
		return apperror.Wrap(apperror.OperationUpdateFailed, "failed to mark switch operation failed", updateErr)
	}

	var appErr *apperror.Error
	if errors.As(operationErr, &appErr) {
		return appErr.WithDetail("operation_update_error", updateErr.Error())
	}
	return apperror.Wrap(apperror.CommandFailed, "switch operation failed", operationErr).
		WithDetail("operation_update_error", updateErr.Error())
}

func errorCodeAndMessage(err error) (apperror.Code, string) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.Message
	}
	return apperror.CommandFailed, err.Error()
}

func mapTargetFSError(err error) error {
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

func marshalSwitchOperationMetadata(checkpoint, providerID, profileID string, plan applyPlan, backup switchBackup, counts SwitchCounts, previousActive *switchPreviousActiveState) (string, error) {
	targets := []switchOperationTargetMetadata{}
	for _, op := range plan.Operations {
		targets = append(targets, switchOperationTargetMetadata{
			TargetID:      op.TargetID,
			BackendID:     op.BackendID,
			TargetLabel:   op.TargetLabel,
			Path:          firstNonEmpty(op.Path, op.privateRecoveryLocator),
			Format:        op.Format,
			Strategy:      op.Strategy,
			Action:        op.Action,
			StatusReason:  op.StatusReason,
			FileExists:    op.FileExists,
			BeforeSHA256:  firstNonEmpty(op.privateBeforeFingerprint, op.BeforeSHA256),
			DesiredSHA256: firstNonEmpty(op.privateDesiredFingerprint, op.DesiredSHA256),
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
		PreviousActive:  previousActive,
		StateCaptures:   plan.SwitchPlan.StateCaptures,
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
