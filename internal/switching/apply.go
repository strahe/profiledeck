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
	OperationID              string       `json:"operation_id"`
	Status                   string       `json:"status"`
	Provider                 PlanProvider `json:"provider"`
	Profile                  PlanProfile  `json:"profile"`
	PlanFingerprint          string       `json:"plan_fingerprint"`
	Counts                   SwitchCounts `json:"counts"`
	RecoveryCleanupCompleted bool         `json:"recovery_cleanup_completed"`
	Warnings                 []string     `json:"warnings"`
}

type SwitchCounts struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Noop   int `json:"noop"`
}

type switchRecoveryPoint struct {
	Path    string
	Entries []transaction.Entry
}

type switchOperationMetadata struct {
	Checkpoint      string                          `json:"checkpoint"`
	ProviderID      string                          `json:"provider_id"`
	ProfileID       string                          `json:"profile_id"`
	RelatedProfiles []string                        `json:"related_profile_ids,omitempty"`
	PlanFingerprint string                          `json:"plan_fingerprint,omitempty"`
	RecoveryPath    string                          `json:"recovery_path,omitempty"`
	Counts          SwitchCounts                    `json:"counts"`
	PreviousActive  *switchPreviousActiveState      `json:"previous_active_state,omitempty"`
	StateCaptures   []StateCapture                  `json:"state_captures,omitempty"`
	Targets         []switchOperationTargetMetadata `json:"targets,omitempty"`
	Warnings        []string                        `json:"warnings,omitempty"`
	UpdatedAtUnixMS int64                           `json:"updated_at_unix_ms"`
}

type switchPreviousActiveState struct {
	Exists    bool   `json:"exists"`
	ProfileID string `json:"profile_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
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
	if err := service.retryRecoveryCleanup(ctx, db); err != nil {
		return ApplySwitchResult{}, err
	}
	// A new switch must not supersede evidence needed to recover an earlier one.
	if err := requireNoUnresolvedSwitchOperation(ctx, db, ""); err != nil {
		return ApplySwitchResult{}, err
	}

	operationID, err := newSwitchOperationID(time.Now())
	if err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create switch operation id", err)
	}
	initialMetadata, err := marshalSwitchOperationMetadata("created", providerID, profileID, applyPlan{}, switchRecoveryPoint{}, SwitchCounts{}, nil)
	if err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to encode switch operation metadata", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: operationID, ProviderID: providerID, ProfileIDs: []string{profileID},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
		MetadataJSON:          initialMetadata,
	}); err != nil {
		return ApplySwitchResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create switch operation", err)
	}

	lock, err := acquireSwitchLock(service.paths.Lock, operationID)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	defer lock.Release()
	if err := service.reconcileRecoveryCleanupLocked(ctx, db); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, err)
	}
	if err := requireNoUnresolvedSwitchOperation(ctx, db, operationID); err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Code == apperror.OperationRecoveryRequired {
			return ApplySwitchResult{}, rejectBlockedSwitchOperation(ctx, db, operationID)
		}
		return ApplySwitchResult{}, failSwitchOperation(
			ctx,
			db,
			operationID,
			initialMetadata,
			err,
		)
	}
	// A Desktop Agent may be disabled while this operation waits for the lock.
	// Once the lock is owned, later preference changes must not interrupt it.
	if err := service.requireProviderWithStore(ctx, db, providerID); err != nil {
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
	plannedMetadata, err := marshalSwitchOperationMetadata("planned", providerID, profileID, plan, switchRecoveryPoint{}, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	relatedProfileIDs, err := switchRelatedProfileIDs(ctx, db, profileID, previousActive, plan)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(
			ctx,
			db,
			operationID,
			plannedMetadata,
			apperror.Wrap(apperror.StoreStatusFailed, "failed to resolve Profiles affected by the switch", err),
		)
	}
	if err := db.UpdateOperationMetadata(
		ctx,
		operationID,
		store.OperationMetadataSchemaVersion,
		plannedMetadata,
		relatedProfileIDs,
	); err != nil {
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

	recoveryPoint, err := createSwitchRecoveryPointWithExecutor(ctx, executor, service.paths, operationID, plan)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, err)
	}
	recoveryMetadata, err := marshalSwitchOperationMetadata("recovery_created", providerID, profileID, plan, recoveryPoint, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(
		ctx,
		operationID,
		store.OperationMetadataSchemaVersion,
		recoveryMetadata,
		relatedProfileIDs,
	); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, plannedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update switch operation metadata", err))
	}
	if err := verifySwitchPlanHashesWithExecutor(ctx, executor, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, recoveryMetadata, err)
	}

	for _, op := range plan.Operations {
		if op.Action != planActionCreate && op.Action != planActionUpdate {
			continue
		}
		if err := writeTargetAtomicWithExecutor(ctx, executor, op); err != nil {
			return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, recoveryMetadata, err)
		}
	}
	// External writes and active-state commit are not one storage transaction.
	// Re-read every target after all writes so an already failed or replaced
	// working copy does not advance active state; external writers can still race
	// after this final read.
	if err := verifyAppliedSwitchTargetsWithExecutor(ctx, executor, plan.Operations); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, recoveryMetadata, err)
	}

	appliedMetadata, err := marshalSwitchOperationMetadata("applied", providerID, profileID, plan, switchRecoveryPoint{}, counts, previousActive)
	if err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, recoveryMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode switch operation metadata", err))
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID: operationID, ProfileID: profileID, ProviderID: providerID,
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
		MetadataJSON:          appliedMetadata,
		CredentialUpdates:     plan.CredentialUpdates,
		ConfigSetUpdates:      plan.ConfigSetUpdates,
	}); err != nil {
		return ApplySwitchResult{}, failSwitchOperation(ctx, db, operationID, recoveryMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to complete switch operation", err))
	}
	cleanupResult, cleanupErr := service.cleanup.ReconcileLocked(ctx, db)
	recoveryCleanupCompleted := cleanupErr == nil && cleanupResult.RecoveryCleanupCompleted

	return ApplySwitchResult{
		OperationID:              operationID,
		Status:                   store.OperationStatusApplied,
		Provider:                 plan.SwitchPlan.Provider,
		Profile:                  plan.SwitchPlan.Profile,
		PlanFingerprint:          plan.SwitchPlan.PlanFingerprint,
		Counts:                   counts,
		RecoveryCleanupCompleted: recoveryCleanupCompleted,
		Warnings:                 plan.SwitchPlan.Warnings,
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
	activeState, err := db.GetActiveState(ctx, providerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &switchPreviousActiveState{Exists: false}, nil
		}
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read previous active state", err)
	}
	return &switchPreviousActiveState{
		Exists: true, ProfileID: activeState.ProfileID, Revision: activeState.Revision,
	}, nil
}

func switchRelatedProfileIDs(
	ctx context.Context,
	db *store.Store,
	profileID string,
	previous *switchPreviousActiveState,
	plan applyPlan,
) ([]string, error) {
	profileIDs := []string{profileID}
	if previous != nil && previous.Exists {
		profileIDs = append(profileIDs, previous.ProfileID)
	}
	credentialIDs := make([]string, 0, len(plan.CredentialUpdates))
	for _, update := range plan.CredentialUpdates {
		credentialIDs = append(credentialIDs, update.ID)
	}
	configSetIDs := make([]string, 0, len(plan.ConfigSetUpdates))
	for _, update := range plan.ConfigSetUpdates {
		configSetIDs = append(configSetIDs, update.ID)
	}
	resourceProfileIDs, err := db.ListProviderResourceProfileIDs(
		ctx,
		plan.SwitchPlan.Provider.ID,
		credentialIDs,
		configSetIDs,
	)
	if err != nil {
		return nil, err
	}
	return append(profileIDs, resourceProfileIDs...), nil
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

func createSwitchRecoveryPointWithExecutor(ctx context.Context, executor transaction.Executor, paths runtime.Paths, operationID string, plan applyPlan) (switchRecoveryPoint, error) {
	recovery, err := executor.CreateRecoveryPoint(ctx, transaction.RecoveryPointRequest{
		RecoveryRoot: paths.Recovery, OperationID: operationID, ProviderID: plan.SwitchPlan.Provider.ID,
		ProfileID: plan.SwitchPlan.Profile.ID, PlanFingerprint: plan.SwitchPlan.PlanFingerprint,
		Operations: transactionOperations(plan.Operations),
	})
	if err != nil {
		return switchRecoveryPoint{}, err
	}
	return switchRecoveryPoint{Path: recovery.Path, Entries: recovery.Entries}, nil
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

func rejectBlockedSwitchOperation(ctx context.Context, db *store.Store, operationID string) error {
	operationErr := unresolvedSwitchOperationError()
	cleanupCtx, cancel := switchCleanupContext(ctx)
	defer cancel()
	if err := db.RejectPendingSwitchOperation(
		cleanupCtx,
		operationID,
		string(operationErr.Code),
		operationErr.Message,
		"blocked_by_unfinished_switch",
	); err != nil {
		return preserveSwitchOperationError(operationErr, err)
	}
	return operationErr
}

func requireNoUnresolvedSwitchOperation(ctx context.Context, db *store.Store, excludeID string) error {
	unresolved, err := db.HasUnresolvedSwitchOperation(ctx, excludeID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "unfinished Profile switches could not be inspected", err)
	}
	if unresolved {
		return unresolvedSwitchOperationError()
	}
	return nil
}

func unresolvedSwitchOperationError() *apperror.Error {
	return apperror.New(
		apperror.OperationRecoveryRequired,
		"resolve the unfinished Profile switch before trying again",
	)
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

	publicErr := apperror.Public(operationErr)
	// Keep both private failures available to internal callers without attaching
	// database diagnostics to a detail map that can cross an output boundary.
	return apperror.Wrap(publicErr.Code, publicErr.Message, errors.Join(operationErr, updateErr))
}

func errorCodeAndMessage(err error) (apperror.Code, string) {
	publicErr := apperror.Public(err)
	return publicErr.Code, publicErr.Message
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

func marshalSwitchOperationMetadata(checkpoint, providerID, profileID string, plan applyPlan, recovery switchRecoveryPoint, counts SwitchCounts, previousActive *switchPreviousActiveState) (string, error) {
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
		RecoveryPath:    recovery.Path,
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
