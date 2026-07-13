package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const rollbackKindFailedSwitchRecovery = "failed_switch_recovery"

const (
	RecoveryStatusRecoverable   = "recoverable"
	RecoveryStatusUnrecoverable = "unrecoverable"
	RecoveryStatusUnknown       = "unknown"
)

type RecoverFailedSwitchParams struct {
	ConfigDir   string
	OperationID string
	Confirm     bool
}

type RecoverFailedSwitchResult struct {
	OperationID       string         `json:"operation_id"`
	OperationType     string         `json:"operation_type"`
	RollbackKind      string         `json:"rollback_kind"`
	Status            string         `json:"status"`
	SourceOperationID string         `json:"source_operation_id"`
	ProviderID        string         `json:"provider_id"`
	ProfileID         string         `json:"profile_id"`
	RestoredProfileID string         `json:"restored_profile_id"`
	Counts            RollbackCounts `json:"counts"`
	BackupPath        string         `json:"backup_path"`
	Warnings          []string       `json:"warnings"`
}

type failedSwitchRecoveryInspection struct {
	Status string
	Reason string
}

func RecoverFailedSwitch(ctx context.Context, req RecoverFailedSwitchParams) (RecoverFailedSwitchResult, error) {
	operationID, appErr := validateID(req.OperationID, ErrorRecoveryUnsupported)
	if appErr != nil {
		return RecoverFailedSwitchResult{}, appErr
	}
	if !req.Confirm {
		return RecoverFailedSwitchResult{}, NewError(ErrorConfirmationRequired, "failed switch recovery requires confirmation")
	}

	_, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return RecoverFailedSwitchResult{}, err
	}
	if err := createRuntimeDirs(paths); err != nil {
		return RecoverFailedSwitchResult{}, WrapError(ErrorRuntimeInitFailed, "failed to initialize runtime directories", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return RecoverFailedSwitchResult{}, err
	}
	defer db.Close()

	// Unknown target state must fail before a recovery operation or backup
	// side effect is created.
	source, err := loadFailedSwitchRecoverySource(ctx, db, paths, operationID)
	if err != nil {
		return RecoverFailedSwitchResult{}, err
	}
	if err := validateRecoveryActiveState(ctx, db, source); err != nil {
		return RecoverFailedSwitchResult{}, err
	}
	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return RecoverFailedSwitchResult{}, err
	}

	recoveryOperationID, err := newRollbackOperationID(time.Now())
	if err != nil {
		return RecoverFailedSwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to create recovery operation id", err)
	}
	metadataBase := recoveryRollbackMetadata(source)
	initialMetadata, err := marshalRollbackOperationMetadata("created", metadataBase)
	if err != nil {
		return RecoverFailedSwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to encode recovery operation metadata", err)
	}
	if _, err := db.CreatePendingRollbackOperation(ctx, store.CreateRollbackOperationParams{
		ID:           recoveryOperationID,
		ProfileID:    source.Metadata.ProfileID,
		MetadataJSON: initialMetadata,
	}); err != nil {
		return RecoverFailedSwitchResult{}, WrapError(ErrorOperationCreateFailed, "failed to create recovery operation", err)
	}

	lock, err := acquireSwitchLock(paths.Lock, recoveryOperationID)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, initialMetadata, err)
	}
	defer lock.Release()

	// The lock closes the cross-process window; source, active state, and
	// target hashes are rechecked before creating the current-state backup.
	source, err = loadFailedSwitchRecoverySource(ctx, db, paths, operationID)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, initialMetadata, err)
	}
	if err := validateRecoveryActiveState(ctx, db, source); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, initialMetadata, err)
	}

	metadataBase = recoveryRollbackMetadata(source)
	validatedMetadata, err := marshalRollbackOperationMetadata("validated", metadataBase)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode recovery operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, recoveryOperationID, validatedMetadata); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update recovery operation metadata", err))
	}

	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, validatedMetadata, err)
	}

	// The current-state backup protects the partial failed-switch state; the
	// target hashes are checked again before the restore/remove writes begin.
	currentBackup, err := createRollbackCurrentBackup(ctx, paths, recoveryOperationID, source)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, validatedMetadata, err)
	}
	metadataBase.CurrentBackupPath = currentBackup.Path
	backedUpMetadata, err := marshalRollbackOperationMetadata("backed_up", metadataBase)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, validatedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode recovery operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, recoveryOperationID, backedUpMetadata); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, validatedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update recovery operation metadata", err))
	}

	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, backedUpMetadata, err)
	}

	counts, processed, err := applyRollbackTargets(ctx, db, recoveryOperationID, backedUpMetadata, metadataBase, source)
	if err != nil {
		return RecoverFailedSwitchResult{}, err
	}
	if err := verifyRestoredRollbackTargets(ctx, source.Targets); err != nil {
		return RecoverFailedSwitchResult{}, failRollbackWithProcessed(ctx, db, recoveryOperationID, backedUpMetadata, metadataBase, counts, processed, err)
	}
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	appliedMetadata, err := marshalRollbackOperationMetadata("applied", metadataBase)
	if err != nil {
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, backedUpMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode recovery operation metadata", err))
	}

	restoredActive := restoredStoreActiveState(source.Metadata.PreviousActive)
	if err := db.CompleteRollbackOperation(ctx, store.CompleteRollbackOperationParams{
		ID:                  recoveryOperationID,
		ProfileID:           restoredProfileID(source.Metadata.PreviousActive),
		ProviderID:          source.Metadata.ProviderID,
		RestoredActiveState: restoredActive,
		MetadataJSON:        appliedMetadata,
	}); err != nil {
		failedMetadata, metadataErr := marshalRollbackOperationMetadata("db_update_failed", metadataBase)
		if metadataErr != nil {
			failedMetadata = backedUpMetadata
		}
		return RecoverFailedSwitchResult{}, failRollbackOperation(ctx, db, recoveryOperationID, failedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to complete recovery operation", err))
	}

	return RecoverFailedSwitchResult{
		OperationID:       recoveryOperationID,
		OperationType:     store.OperationTypeRollback,
		RollbackKind:      rollbackKindFailedSwitchRecovery,
		Status:            store.OperationStatusApplied,
		SourceOperationID: source.Operation.ID,
		ProviderID:        source.Metadata.ProviderID,
		ProfileID:         source.Metadata.ProfileID,
		RestoredProfileID: restoredProfileID(source.Metadata.PreviousActive),
		Counts:            counts,
		BackupPath:        currentBackup.Path,
	}, nil
}

func loadFailedSwitchRecoverySource(ctx context.Context, db *store.Store, paths runtime.Paths, operationID string) (rollbackSource, error) {
	operation, err := db.GetOperation(ctx, operationID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "failed switch operation not found").WithDetail("operation_id", operationID)
		}
		return rollbackSource{}, WrapError(ErrorStoreStatusFailed, "failed to read failed switch operation", err)
	}
	return loadFailedSwitchRecoverySourceFromOperation(ctx, db, paths, operation)
}

func loadFailedSwitchRecoverySourceFromOperation(ctx context.Context, db *store.Store, paths runtime.Paths, operation store.Operation) (rollbackSource, error) {
	if _, appErr := validateBackupID(operation.ID); appErr != nil {
		return rollbackSource{}, appErr
	}
	if operation.OperationType != store.OperationTypeSwitch {
		return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "operation is not a switch operation").WithDetail("operation_id", operation.ID)
	}
	if operation.Status != store.OperationStatusFailed {
		return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "switch operation is not failed").WithDetail("operation_id", operation.ID)
	}

	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		return rollbackSource{}, WrapError(ErrorRecoveryUnsupported, "failed switch metadata is invalid", err).WithDetail("operation_id", operation.ID)
	}
	if metadata.Checkpoint != "backed_up" {
		return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "failed switch has no recoverable backup checkpoint").WithDetail("operation_id", operation.ID)
	}
	if metadata.PreviousActive == nil {
		return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "failed switch metadata does not include previous active state").WithDetail("operation_id", operation.ID)
	}
	if metadata.ProviderID == "" || metadata.ProfileID == "" || metadata.PlanFingerprint == "" || metadata.BackupPath == "" {
		return rollbackSource{}, NewError(ErrorRecoveryUnsupported, "failed switch metadata is incomplete").WithDetail("operation_id", operation.ID)
	}

	backupPath := filepath.Join(paths.Backups, operation.ID)
	if filepath.Clean(metadata.BackupPath) != filepath.Clean(backupPath) {
		return rollbackSource{}, NewError(ErrorBackupInvalid, "failed switch backup path does not match runtime backup path").WithDetail("operation_id", operation.ID)
	}
	manifest, err := loadBackupManifest(backupPath)
	if err != nil {
		return rollbackSource{}, err
	}
	if err := validateRollbackManifest(manifest, metadata, operation.ID, operation.ID, backupPath); err != nil {
		return rollbackSource{}, err
	}
	adapter, err := rollbackAdapter(ctx, db, metadata)
	if err != nil {
		return rollbackSource{}, err
	}
	targets, err := rollbackTargetsFromMetadataWithAdapter(metadata, manifest, backupPath, adapter)
	if err != nil {
		return rollbackSource{}, err
	}
	if err := validateSourceBackupFiles(ctx, backupPath, targets); err != nil {
		return rollbackSource{}, err
	}

	return rollbackSource{
		Operation:  operation,
		Manifest:   manifest,
		Metadata:   metadata,
		Targets:    targets,
		BackupPath: backupPath,
	}, nil
}

func validateRecoveryActiveState(ctx context.Context, db *store.Store, source rollbackSource) error {
	previous := source.Metadata.PreviousActive
	if previous == nil {
		return NewError(ErrorRecoveryUnsupported, "failed switch metadata does not include previous active state").WithDetail("operation_id", source.Operation.ID)
	}

	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, source.Metadata.ProviderID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if !previous.Exists {
				return nil
			}
			return NewError(ErrorTargetChanged, "active state no longer matches failed switch previous state").
				WithDetail("operation_id", source.Operation.ID).
				WithDetail("provider_id", source.Metadata.ProviderID)
		}
		return WrapError(ErrorStoreStatusFailed, "failed to read active state", err)
	}
	if !previous.Exists {
		return NewError(ErrorTargetChanged, "active state no longer matches failed switch previous state").
			WithDetail("operation_id", source.Operation.ID).
			WithDetail("provider_id", source.Metadata.ProviderID)
	}
	if activeState.ProfileID != previous.ProfileID || activeState.OperationID != previous.OperationID {
		return NewError(ErrorTargetChanged, "active state no longer matches failed switch previous state").
			WithDetail("operation_id", source.Operation.ID).
			WithDetail("provider_id", source.Metadata.ProviderID)
	}
	return nil
}

func recoveryRollbackMetadata(source rollbackSource) rollbackOperationMetadata {
	return rollbackOperationMetadata{
		RollbackKind:      rollbackKindFailedSwitchRecovery,
		SourceOperationID: source.Operation.ID,
		BackupID:          source.Operation.ID,
		ProviderID:        source.Metadata.ProviderID,
		ProfileID:         source.Metadata.ProfileID,
		RestoredProfileID: restoredProfileID(source.Metadata.PreviousActive),
		Targets:           rollbackTargetMetadataList(source.Targets),
	}
}

func inspectFailedSwitchRecovery(ctx context.Context, db *store.Store, paths runtime.Paths, operation store.Operation) failedSwitchRecoveryInspection {
	// Doctor must stay read-only, so it reuses recovery validation and maps
	// failures to diagnostic statuses instead of creating recovery records.
	source, err := loadFailedSwitchRecoverySourceFromOperation(ctx, db, paths, operation)
	if err != nil {
		return failedSwitchRecoveryInspectionFromError(err)
	}
	if err := validateRecoveryActiveState(ctx, db, source); err != nil {
		return failedSwitchRecoveryInspectionFromError(err)
	}
	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return failedSwitchRecoveryInspectionFromError(err)
	}
	return failedSwitchRecoveryInspection{
		Status: RecoveryStatusRecoverable,
		Reason: "valid_backup_checkpoint",
	}
}

func failedSwitchRecoveryInspectionFromError(err error) failedSwitchRecoveryInspection {
	if recoveryTargetStateUnknown(err) {
		return failedSwitchRecoveryInspection{Status: RecoveryStatusUnknown, Reason: "target_state_unknown"}
	}

	var appErr *AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case ErrorRecoveryUnsupported, ErrorRollbackUnsupported:
			return failedSwitchRecoveryInspection{Status: RecoveryStatusUnrecoverable, Reason: "recovery_unsupported"}
		case ErrorBackupInvalid, ErrorBackupNotFound:
			return failedSwitchRecoveryInspection{Status: RecoveryStatusUnrecoverable, Reason: "backup_invalid"}
		case ErrorTargetChanged:
			return failedSwitchRecoveryInspection{Status: RecoveryStatusUnrecoverable, Reason: "target_state_unrecognized"}
		case ErrorBackupFailed, ErrorStoreStatusFailed, ErrorTargetReadFailed:
			return failedSwitchRecoveryInspection{Status: RecoveryStatusUnknown, Reason: "recovery_check_failed"}
		}
	}
	return failedSwitchRecoveryInspection{Status: RecoveryStatusUnknown, Reason: "recovery_check_failed"}
}

func recoveryTargetStateUnknown(err error) bool {
	var targetErr *targetfs.Error
	if !errors.As(err, &targetErr) {
		return false
	}
	switch targetErr.Message {
	case "failed to inspect target", "failed to read target", "failed to hash target", "target file is too large":
		return true
	default:
		return false
	}
}
