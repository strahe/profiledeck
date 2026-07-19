package switching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
	"github.com/strahe/profiledeck/internal/validate"
)

const recoveryOperationRandomBytes = 6

const (
	RecoveryStatusRunning       = "running"
	RecoveryStatusClosable      = "closable"
	RecoveryStatusRecoverable   = "recoverable"
	RecoveryStatusUnrecoverable = "unrecoverable"
	RecoveryStatusUnknown       = "unknown"

	RecoveryActionClose   = "close"
	RecoveryActionRestore = "restore"
)

type RecoverOperationParams struct {
	OperationID string `json:"operation_id"`
	Confirm     bool   `json:"confirm"`
}

type RecoverOperationResult struct {
	SourceOperationID        string         `json:"source_operation_id"`
	RecoveryOperationID      string         `json:"recovery_operation_id,omitempty"`
	Action                   string         `json:"action"`
	Status                   string         `json:"status"`
	ProviderID               string         `json:"provider_id,omitempty"`
	ProfileID                string         `json:"profile_id,omitempty"`
	RestoredProfileID        string         `json:"restored_profile_id,omitempty"`
	Counts                   RecoveryCounts `json:"counts"`
	RecoveryCleanupCompleted bool           `json:"recovery_cleanup_completed"`
}

type RecoveryInspection struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	Action      string `json:"action,omitempty"`
	Reason      string `json:"reason"`
}

type recoveryAssessment struct {
	Inspection     RecoveryInspection
	Source         recoverySource
	ResolutionKind string
}

func (service *Service) InspectRecovery(ctx context.Context, rawOperationID string) (RecoveryInspection, error) {
	operationID, appErr := validate.ID(rawOperationID, apperror.RecoveryUnsupported)
	if appErr != nil {
		return RecoveryInspection{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return RecoveryInspection{}, err
	}
	defer db.Close()
	operation, err := db.GetOperation(ctx, operationID)
	if errors.Is(err, store.ErrNotFound) {
		return RecoveryInspection{}, apperror.New(apperror.RecoveryUnsupported, "switch operation not found").WithDetail("operation_id", operationID)
	}
	if err != nil {
		return RecoveryInspection{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read switch operation", err)
	}
	return service.inspectRecoveryFromOperation(ctx, db, service.paths, operation, true).Inspection, nil
}

func (service *Service) RecoverOperation(ctx context.Context, req RecoverOperationParams) (RecoverOperationResult, error) {
	operationID, appErr := validate.ID(req.OperationID, apperror.RecoveryUnsupported)
	if appErr != nil {
		return RecoverOperationResult{}, appErr
	}
	if !req.Confirm {
		return RecoverOperationResult{}, apperror.New(apperror.ConfirmationRequired, "operation recovery requires confirmation")
	}
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return RecoverOperationResult{}, err
	}
	defer db.Close()

	// Acquiring the shared switch lock before any recovery mutation proves the
	// original switch is no longer running and excludes new switch writes.
	lock, err := acquireSwitchLock(service.paths.Lock, "recover-"+operationID)
	if err != nil {
		return RecoverOperationResult{}, err
	}
	defer lock.Release()
	if err := service.reconcileRecoveryCleanupLocked(ctx, db); err != nil {
		return RecoverOperationResult{}, err
	}
	operation, err := db.GetOperation(ctx, operationID)
	if errors.Is(err, store.ErrNotFound) {
		return RecoverOperationResult{}, apperror.New(apperror.RecoveryUnsupported, "switch operation not found").WithDetail("operation_id", operationID)
	}
	if err != nil {
		return RecoverOperationResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read switch operation", err)
	}
	assessment := service.inspectRecoveryFromOperation(ctx, db, service.paths, operation, false)
	if assessment.Inspection.Status == RecoveryStatusClosable {
		if err := db.ResolveSwitchOperationForCleanup(ctx, operationID, assessment.ResolutionKind); err != nil {
			return RecoverOperationResult{}, apperror.Wrap(apperror.OperationUpdateFailed, "failed to close incomplete switch operation", err)
		}
		cleanupResult, cleanupErr := service.cleanup.ReconcileLocked(ctx, db)
		return RecoverOperationResult{
			SourceOperationID:        operationID,
			Action:                   RecoveryActionClose,
			Status:                   "resolved",
			ProviderID:               assessment.Source.Metadata.ProviderID,
			ProfileID:                assessment.Source.Metadata.ProfileID,
			RestoredProfileID:        restoredProfileID(assessment.Source.Metadata.PreviousActive),
			RecoveryCleanupCompleted: cleanupErr == nil && cleanupResult.RecoveryCleanupCompleted,
		}, nil
	}
	if assessment.Inspection.Status != RecoveryStatusRecoverable {
		return RecoverOperationResult{}, recoveryInspectionError(assessment.Inspection)
	}
	source := assessment.Source

	recoveryOperationID, err := newRecoveryOperationID(time.Now())
	if err != nil {
		return RecoverOperationResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create recovery operation id", err)
	}
	metadataBase := recoveryMetadata(source)
	initialMetadata, err := marshalRecoveryOperationMetadata("created", metadataBase)
	if err != nil {
		return RecoverOperationResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to encode recovery operation metadata", err)
	}
	if _, err := db.CreatePendingRecoveryOperation(ctx, store.CreateRecoveryOperationParams{
		ID: recoveryOperationID, ProfileID: source.Metadata.ProfileID, MetadataJSON: initialMetadata,
	}); err != nil {
		return RecoverOperationResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create recovery operation", err)
	}

	counts, processed, err := service.applyRecoveryTargets(ctx, db, recoveryOperationID, initialMetadata, metadataBase, source)
	if err != nil {
		return RecoverOperationResult{}, err
	}
	if err := service.verifyRestoredRecoveryTargets(ctx, source.Targets); err != nil {
		return RecoverOperationResult{}, failRecoveryWithProcessed(ctx, db, recoveryOperationID, initialMetadata, metadataBase, counts, processed, err)
	}
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	appliedMetadata, err := marshalRecoveryOperationMetadata("applied", metadataBase)
	if err != nil {
		return RecoverOperationResult{}, failRecoveryOperation(ctx, db, recoveryOperationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode recovery operation metadata", err))
	}
	if err := db.CompleteRecoveryOperation(ctx, store.CompleteRecoveryOperationParams{
		ID:                  recoveryOperationID,
		SourceOperationID:   source.Operation.ID,
		ResolutionKind:      "recovered_pre_switch",
		ProfileID:           restoredProfileID(source.Metadata.PreviousActive),
		ProviderID:          source.Metadata.ProviderID,
		RestoredActiveState: restoredStoreActiveState(source.Metadata.PreviousActive),
		MetadataJSON:        appliedMetadata,
	}); err != nil {
		return RecoverOperationResult{}, failRecoveryOperation(ctx, db, recoveryOperationID, appliedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to complete recovery operation", err))
	}
	cleanupResult, cleanupErr := service.cleanup.ReconcileLocked(ctx, db)
	return RecoverOperationResult{
		SourceOperationID:        source.Operation.ID,
		RecoveryOperationID:      recoveryOperationID,
		Action:                   RecoveryActionRestore,
		Status:                   "resolved",
		ProviderID:               source.Metadata.ProviderID,
		ProfileID:                source.Metadata.ProfileID,
		RestoredProfileID:        restoredProfileID(source.Metadata.PreviousActive),
		Counts:                   counts,
		RecoveryCleanupCompleted: cleanupErr == nil && cleanupResult.RecoveryCleanupCompleted,
	}, nil
}

func (service *Service) inspectRecoveryFromOperation(
	ctx context.Context,
	db *store.Store,
	paths runtime.Paths,
	operation store.Operation,
	probeLock bool,
) recoveryAssessment {
	inspection := RecoveryInspection{OperationID: operation.ID}
	if operation.OperationType != store.OperationTypeSwitch ||
		(operation.Status != store.OperationStatusPending && operation.Status != store.OperationStatusFailed) ||
		operation.ResolvedAtUnixMS != 0 {
		inspection.Status = RecoveryStatusUnrecoverable
		inspection.Reason = "operation_not_unresolved_switch"
		return recoveryAssessment{Inspection: inspection}
	}
	if probeLock {
		probe, err := targetfs.ProbeLock(paths.Lock)
		if err != nil {
			inspection.Status = RecoveryStatusUnknown
			inspection.Reason = "switch_lock_check_failed"
			return recoveryAssessment{Inspection: inspection}
		}
		if probe.Held {
			inspection.Status = RecoveryStatusRunning
			inspection.Reason = "switch_operation_in_progress"
			return recoveryAssessment{Inspection: inspection}
		}
	}

	var metadata switchOperationMetadata
	if err := decodeSwitchOperationMetadata(operation.MetadataJSON, &metadata); err != nil {
		inspection.Status = RecoveryStatusUnrecoverable
		inspection.Reason = "operation_metadata_invalid"
		return recoveryAssessment{Inspection: inspection}
	}
	source := recoverySource{Operation: operation, Metadata: metadata}
	switch metadata.Checkpoint {
	case "created", "planned":
		inspection.Status = RecoveryStatusClosable
		inspection.Action = RecoveryActionClose
		inspection.Reason = "targets_not_written"
		return recoveryAssessment{
			Inspection:     inspection,
			Source:         source,
			ResolutionKind: "closed_before_target_writes",
		}
	case "recovery_created":
	default:
		inspection.Status = RecoveryStatusUnrecoverable
		inspection.Reason = "recovery_checkpoint_invalid"
		return recoveryAssessment{Inspection: inspection, Source: source}
	}
	loaded, err := service.loadOperationRecoverySourceFromOperation(ctx, db, paths, operation)
	if err != nil {
		return recoveryAssessment{Inspection: recoveryInspectionFromError(operation.ID, err), Source: source}
	}
	if err := validateRecoveryActiveState(ctx, db, loaded); err != nil {
		return recoveryAssessment{Inspection: recoveryInspectionFromError(operation.ID, err), Source: loaded}
	}
	allBefore, err := service.inspectRecoveryTargetStates(ctx, loaded.Targets)
	if err != nil {
		return recoveryAssessment{Inspection: recoveryInspectionFromError(operation.ID, err), Source: loaded}
	}
	if allBefore {
		inspection.Status = RecoveryStatusClosable
		inspection.Action = RecoveryActionClose
		inspection.Reason = "targets_already_before_switch"
		return recoveryAssessment{
			Inspection:     inspection,
			Source:         loaded,
			ResolutionKind: "closed_targets_unchanged",
		}
	}
	inspection.Status = RecoveryStatusRecoverable
	inspection.Action = RecoveryActionRestore
	inspection.Reason = "recognized_partial_switch_state"
	return recoveryAssessment{Inspection: inspection, Source: loaded}
}

func (service *Service) loadOperationRecoverySourceFromOperation(
	ctx context.Context,
	db *store.Store,
	paths runtime.Paths,
	operation store.Operation,
) (recoverySource, error) {
	var metadata switchOperationMetadata
	if err := decodeSwitchOperationMetadata(operation.MetadataJSON, &metadata); err != nil {
		return recoverySource{}, apperror.New(apperror.RecoveryUnsupported, "switch operation metadata is invalid").WithDetail("operation_id", operation.ID)
	}
	if metadata.Checkpoint != "recovery_created" || metadata.PreviousActive == nil ||
		metadata.ProviderID == "" || metadata.ProfileID == "" || metadata.PlanFingerprint == "" || metadata.RecoveryPath == "" {
		return recoverySource{}, apperror.New(apperror.RecoveryUnsupported, "switch operation has no valid recovery checkpoint").WithDetail("operation_id", operation.ID)
	}
	recoveryPath := filepath.Join(paths.Recovery, operation.ID)
	if filepath.Clean(metadata.RecoveryPath) != filepath.Clean(recoveryPath) {
		return recoverySource{}, apperror.New(apperror.BackupInvalid, "operation recovery location does not match the switch operation").WithDetail("operation_id", operation.ID)
	}
	manifest, err := transaction.LoadRecoveryManifest(recoveryPath)
	if err != nil {
		return recoverySource{}, err
	}
	if err := validateRecoveryManifest(manifest, metadata, operation.ID, recoveryPath); err != nil {
		return recoverySource{}, err
	}
	adapter, err := service.recoveryAdapter(ctx, db, metadata)
	if err != nil {
		return recoverySource{}, err
	}
	targets, err := recoveryTargetsFromMetadataWithAdapter(metadata, manifest, recoveryPath, adapter)
	if err != nil {
		return recoverySource{}, err
	}
	if err := validateRecoveryFiles(ctx, recoveryPath, targets); err != nil {
		return recoverySource{}, err
	}
	return recoverySource{
		Operation: operation, Manifest: manifest, Metadata: metadata, Targets: targets, RecoveryPath: recoveryPath,
	}, nil
}

func validateRecoveryActiveState(ctx context.Context, db *store.Store, source recoverySource) error {
	previous := source.Metadata.PreviousActive
	if previous == nil {
		return apperror.New(apperror.RecoveryUnsupported, "switch operation does not include its previous active state").WithDetail("operation_id", source.Operation.ID)
	}
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, source.Metadata.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		if !previous.Exists {
			return nil
		}
		return apperror.New(apperror.TargetChanged, "active state no longer matches the incomplete switch").WithDetail("operation_id", source.Operation.ID)
	}
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to read active state", err)
	}
	if !previous.Exists || activeState.ProfileID != previous.ProfileID || activeState.OperationID != previous.OperationID {
		return apperror.New(apperror.TargetChanged, "active state no longer matches the incomplete switch").WithDetail("operation_id", source.Operation.ID)
	}
	return nil
}

// InspectRecoveryFromOperation is the read-only Doctor integration point.
func (service *Service) InspectRecoveryFromOperation(
	ctx context.Context,
	db *store.Store,
	paths runtime.Paths,
	operation store.Operation,
) RecoveryInspection {
	return service.inspectRecoveryFromOperation(ctx, db, paths, operation, true).Inspection
}

func newRecoveryOperationID(now time.Time) (string, error) {
	randomBytes := make([]byte, recoveryOperationRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("recovery-%d-%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func decodeSwitchOperationMetadata(raw string, metadata *switchOperationMetadata) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(metadata); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("switch operation metadata contains extra data")
	}
	return nil
}

func recoveryInspectionFromError(operationID string, err error) RecoveryInspection {
	inspection := RecoveryInspection{OperationID: operationID}
	if recoveryTargetStateUnknown(err) {
		inspection.Status = RecoveryStatusUnknown
		inspection.Reason = "target_state_unknown"
		return inspection
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case apperror.RecoveryUnsupported:
			inspection.Status = RecoveryStatusUnrecoverable
			inspection.Reason = "recovery_unsupported"
		case apperror.BackupInvalid, apperror.BackupNotFound:
			inspection.Status = RecoveryStatusUnrecoverable
			inspection.Reason = "recovery_files_invalid"
		case apperror.TargetChanged:
			inspection.Status = RecoveryStatusUnrecoverable
			inspection.Reason = "target_state_unrecognized"
		case apperror.BackupFailed, apperror.StoreStatusFailed, apperror.TargetReadFailed:
			inspection.Status = RecoveryStatusUnknown
			inspection.Reason = "recovery_check_failed"
		default:
			inspection.Status = RecoveryStatusUnknown
			inspection.Reason = "recovery_check_failed"
		}
		return inspection
	}
	inspection.Status = RecoveryStatusUnknown
	inspection.Reason = "recovery_check_failed"
	return inspection
}

func recoveryInspectionError(inspection RecoveryInspection) error {
	switch inspection.Status {
	case RecoveryStatusRunning:
		return apperror.New(apperror.LockAcquireFailed, "a switch operation is still running")
	case RecoveryStatusUnknown:
		return apperror.New(apperror.RecoveryUnsupported, "operation recovery could not safely inspect every target").WithDetail("reason", inspection.Reason)
	default:
		return apperror.New(apperror.RecoveryUnsupported, "operation cannot be recovered safely").WithDetail("reason", inspection.Reason)
	}
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
