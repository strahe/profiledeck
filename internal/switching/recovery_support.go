package switching

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type RecoveryCounts struct {
	Restore int `json:"restore"`
	Remove  int `json:"remove"`
	Noop    int `json:"noop"`
}

type recoveryOperationMetadata struct {
	Checkpoint        string                   `json:"checkpoint"`
	SourceOperationID string                   `json:"source_operation_id"`
	ProviderID        string                   `json:"provider_id"`
	ProfileID         string                   `json:"profile_id"`
	RestoredProfileID string                   `json:"restored_profile_id,omitempty"`
	Counts            RecoveryCounts           `json:"counts"`
	Targets           []recoveryTargetMetadata `json:"targets,omitempty"`
	ProcessedTargets  []string                 `json:"processed_targets,omitempty"`
	UpdatedAtUnixMS   int64                    `json:"updated_at_unix_ms"`
}

type recoveryTargetMetadata struct {
	TargetID  string `json:"target_id"`
	BackendID string `json:"backend_id"`
	Action    string `json:"action"`
}

type recoverySource struct {
	Operation    store.Operation
	Manifest     transaction.Manifest
	Metadata     switchOperationMetadata
	Targets      []recoveryTarget
	RecoveryPath string
}

type recoveryTarget struct {
	TargetID        string
	BackendID       string
	TargetLabel     string
	Path            string
	Action          string
	FileExists      bool
	BeforeSHA256    string
	DesiredSHA256   string
	RecoveryRelPath string
	Mode            os.FileMode
	HasMode         bool
	Spec            targetSpec
	PrivateLocator  string
}

type recognizedRecoveryState string

const (
	recoveryStateBefore  recognizedRecoveryState = "before"
	recoveryStateDesired recognizedRecoveryState = "desired"
)

func validateRecoveryManifest(manifest transaction.Manifest, metadata switchOperationMetadata, operationID, recoveryPath string) error {
	if manifest.OperationID != operationID {
		return apperror.New(apperror.BackupInvalid, "operation recovery manifest does not match the switch operation").WithDetail("operation_id", operationID)
	}
	if manifest.ProviderID != metadata.ProviderID || manifest.ProfileID != metadata.ProfileID || manifest.PlanFingerprint != metadata.PlanFingerprint {
		return apperror.New(apperror.BackupInvalid, "operation recovery manifest metadata does not match the switch operation").WithDetail("operation_id", operationID)
	}
	for _, entry := range manifest.Entries {
		if entry.RecoveryRelPath != "" {
			if _, err := safeRecoveryRelPath(recoveryPath, entry.RecoveryRelPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func recoveryTargetsFromMetadataWithAdapter(
	metadata switchOperationMetadata,
	manifest transaction.Manifest,
	recoveryPath string,
	adapter switchplan.Adapter,
) ([]recoveryTarget, error) {
	entries := map[string]transaction.Entry{}
	for _, entry := range manifest.Entries {
		if entry.TargetID == "" {
			return nil, apperror.New(apperror.BackupInvalid, "operation recovery entry target id is empty")
		}
		if _, exists := entries[entry.TargetID]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "operation recovery contains a duplicate target").WithDetail("target_id", entry.TargetID)
		}
		entries[entry.TargetID] = entry
	}

	targets := make([]recoveryTarget, 0, len(metadata.Targets))
	seenTargetIDs := make(map[string]struct{}, len(metadata.Targets))
	seenLocators := make(map[string]string, len(metadata.Targets))
	for _, target := range metadata.Targets {
		backendID := target.BackendID
		if target.TargetID == "" || backendID == "" || target.DesiredSHA256 == "" || (backendID == targetBackendFile && target.Path == "") {
			return nil, apperror.New(apperror.RecoveryUnsupported, "switch target recovery metadata is incomplete").WithDetail("target_id", target.TargetID)
		}
		if _, exists := seenTargetIDs[target.TargetID]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "switch recovery metadata contains duplicate target IDs").WithDetail("target_id", target.TargetID)
		}
		seenTargetIDs[target.TargetID] = struct{}{}
		var spec targetSpec
		var err error
		if adapter != nil {
			spec, err = adapter.ResolveTargetSpec(metadata.ProviderID, target.TargetID, backendID, target.Path, target.TargetLabel)
		} else if backendID == targetBackendFile {
			spec, err = resolveFileTargetSpec(target.TargetID, backendID, target.Path, target.TargetLabel)
		} else {
			err = apperror.New(apperror.RecoveryUnsupported, "switch recovery adapter is unavailable").WithDetail("backend_id", backendID)
		}
		if err != nil {
			return nil, err
		}
		locatorKey := spec.BackendID() + "\x00" + spec.LocatorFingerprint()
		if firstTargetID, exists := seenLocators[locatorKey]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "switch recovery metadata contains duplicate target locators").
				WithDetail("target_id", target.TargetID).WithDetail("first_target_id", firstTargetID)
		}
		seenLocators[locatorKey] = target.TargetID
		recoveryTarget := recoveryTarget{
			TargetID: target.TargetID, BackendID: backendID, TargetLabel: target.TargetLabel,
			Path: target.Path, Action: target.Action, FileExists: target.FileExists,
			BeforeSHA256: target.BeforeSHA256, DesiredSHA256: target.DesiredSHA256,
			Spec: spec, PrivateLocator: entryPrivateLocator(entries, target.TargetID),
		}
		switch target.Action {
		case planActionCreate, planActionUpdate:
			entry, ok := entries[target.TargetID]
			if !ok {
				return nil, apperror.New(apperror.BackupInvalid, "operation recovery is missing a target entry").WithDetail("target_id", target.TargetID)
			}
			if err := validateRecoveryEntry(target, entry, spec); err != nil {
				return nil, err
			}
			delete(entries, target.TargetID)
			if entry.RecoveryRelPath != "" {
				relPath, err := safeRecoveryRelPath(recoveryPath, entry.RecoveryRelPath)
				if err != nil {
					return nil, err
				}
				recoveryTarget.RecoveryRelPath = relPath
			}
			if entry.Mode != "" {
				mode, err := parseFileMode(entry.Mode)
				if err != nil {
					return nil, apperror.New(apperror.BackupInvalid, "operation recovery file mode is invalid").WithDetail("target_id", target.TargetID)
				}
				recoveryTarget.Mode = mode
				recoveryTarget.HasMode = true
			}
			if modeSpec, ok := spec.(switchtarget.RecoveryModeSpec); ok {
				recoveryTarget.Mode, recoveryTarget.HasMode = modeSpec.RecoveryMode(recoveryTarget.Mode, recoveryTarget.HasMode)
			}
		case planActionNoop:
			if _, requiresRecoveryIdentity := spec.(switchtarget.RecoveryIdentitySpec); requiresRecoveryIdentity {
				entry, ok := entries[target.TargetID]
				if !ok {
					return nil, apperror.New(apperror.BackupInvalid, "operation recovery is missing target identity").WithDetail("target_id", target.TargetID)
				}
				if err := validateRecoveryEntry(target, entry, spec); err != nil {
					return nil, err
				}
				delete(entries, target.TargetID)
			}
		default:
			return nil, apperror.New(apperror.RecoveryUnsupported, "switch target action cannot be recovered").WithDetail("target_id", target.TargetID)
		}
		targets = append(targets, recoveryTarget)
	}
	for targetID := range entries {
		return nil, apperror.New(apperror.BackupInvalid, "operation recovery contains an unknown target").WithDetail("target_id", targetID)
	}
	return targets, nil
}

func switchMetadataNeedsAdapter(metadata switchOperationMetadata) bool {
	for _, target := range metadata.Targets {
		if target.BackendID != targetBackendFile {
			return true
		}
	}
	return false
}

func (service *Service) recoveryAdapter(ctx context.Context, db *store.Store, metadata switchOperationMetadata) (switchplan.Adapter, error) {
	if adapter, ok := service.dependencies.Adapters.ManagedAdapter(metadata.ProviderID); ok {
		return adapter, nil
	}
	provider, err := db.GetProvider(ctx, metadata.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		if switchMetadataNeedsAdapter(metadata) {
			return nil, apperror.New(apperror.RecoveryUnsupported, "switch Provider is unavailable").WithDetail("provider_id", metadata.ProviderID)
		}
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read switch Provider", err)
	}
	adapter, ok := service.dependencies.Adapters.Adapter(provider.AdapterID)
	if !ok {
		if switchMetadataNeedsAdapter(metadata) {
			return nil, apperror.New(apperror.RecoveryUnsupported, "switch recovery adapter is unavailable").WithDetail("adapter_id", provider.AdapterID)
		}
		return nil, nil
	}
	return adapter, nil
}

func validateRecoveryFiles(ctx context.Context, recoveryPath string, targets []recoveryTarget) error {
	for _, target := range targets {
		if target.Action != planActionUpdate {
			continue
		}
		recoveryFile := filepath.Join(recoveryPath, target.RecoveryRelPath)
		state, err := targetfs.Inspect(ctx, recoveryFile)
		if err != nil {
			return mapTargetFSError(err)
		}
		if !state.Exists || state.IsSymlink || state.IsDir || !state.IsRegular {
			return apperror.New(apperror.BackupInvalid, "operation recovery file is missing or invalid").WithDetail("target_id", target.TargetID)
		}
		if state.SHA256 != target.BeforeSHA256 {
			return apperror.New(apperror.BackupInvalid, "operation recovery file does not match its manifest").WithDetail("target_id", target.TargetID)
		}
	}
	return nil
}

func validateRecoveryEntry(target switchOperationTargetMetadata, entry transaction.Entry, spec targetSpec) error {
	if target.BackendID == "" || entry.BackendID == "" || entry.BackendID != target.BackendID ||
		entry.Path != target.Path || entry.Action != target.Action || entry.BeforeSHA256 != target.BeforeSHA256 {
		return apperror.New(apperror.BackupInvalid, "operation recovery entry does not match its target").WithDetail("target_id", target.TargetID)
	}
	_, requiresRecoveryIdentity := spec.(switchtarget.RecoveryIdentitySpec)
	if !requiresRecoveryIdentity && entry.PrivateLocator != "" {
		return apperror.New(apperror.BackupInvalid, "operation recovery contains unexpected private state").WithDetail("target_id", target.TargetID)
	}
	switch target.Action {
	case planActionCreate:
		if entry.Existed || entry.RecoveryRelPath != "" {
			return apperror.New(apperror.BackupInvalid, "create recovery entry contains previous content").WithDetail("target_id", target.TargetID)
		}
	case planActionUpdate:
		if !entry.Existed || entry.RecoveryRelPath == "" {
			return apperror.New(apperror.BackupInvalid, "update recovery entry is missing previous content").WithDetail("target_id", target.TargetID)
		}
		if requiresRecoveryIdentity && entry.PrivateLocator == "" {
			return apperror.New(apperror.BackupInvalid, "operation recovery is missing target identity").WithDetail("target_id", target.TargetID)
		}
	case planActionNoop:
		if !requiresRecoveryIdentity || !entry.Existed || entry.RecoveryRelPath != "" || entry.PrivateLocator == "" {
			return apperror.New(apperror.BackupInvalid, "no-op recovery entry contains unsupported state").WithDetail("target_id", target.TargetID)
		}
	default:
		return apperror.New(apperror.RecoveryUnsupported, "operation recovery entry action is unsupported").WithDetail("target_id", target.TargetID)
	}
	return nil
}

func safeRecoveryRelPath(recoveryPath, raw string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(raw))
	if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", apperror.New(apperror.BackupInvalid, "operation recovery relative path is invalid")
	}
	fullPath := filepath.Join(recoveryPath, rel)
	if !strings.HasPrefix(fullPath, filepath.Clean(recoveryPath)+string(os.PathSeparator)) {
		return "", apperror.New(apperror.BackupInvalid, "operation recovery relative path escapes its directory")
	}
	return rel, nil
}

func parseFileMode(raw string) (os.FileMode, error) {
	value, err := strconv.ParseUint(raw, 0, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(value).Perm(), nil
}

func (service *Service) inspectRecoveryTargetStates(ctx context.Context, targets []recoveryTarget) (bool, error) {
	allBefore := true
	for _, target := range targets {
		state, err := service.inspectRecoveryTargetState(ctx, target)
		if err != nil {
			return false, err
		}
		if state != recoveryStateBefore {
			allBefore = false
		}
	}
	return allBefore, nil
}

func (service *Service) inspectRecoveryTargetState(ctx context.Context, target recoveryTarget) (recognizedRecoveryState, error) {
	executor := service.transactionExecutor()
	state, err := executor.Inspect(ctx, target.BackendID, target.Spec)
	if err != nil {
		return "", err
	}
	if target.Action == planActionCreate {
		if !state.Exists {
			return recoveryStateBefore, nil
		}
		if state.IsSymlink || state.Fingerprint != target.DesiredSHA256 {
			return "", apperror.New(apperror.TargetChanged, "target no longer matches a recognized recovery state").WithDetail("target_id", target.TargetID)
		}
		return recoveryStateDesired, nil
	}
	if target.Action == planActionUpdate {
		if !state.Exists || state.IsSymlink {
			return "", apperror.New(apperror.TargetChanged, "target no longer matches a recognized recovery state").WithDetail("target_id", target.TargetID)
		}
		if target.PrivateLocator != "" && state.OpaqueState != target.PrivateLocator {
			return "", apperror.New(apperror.TargetChanged, "credential store item was replaced").WithDetail("target_id", target.TargetID)
		}
		if state.Fingerprint == target.DesiredSHA256 {
			return recoveryStateDesired, nil
		}
		if target.BeforeSHA256 != "" && state.Fingerprint == target.BeforeSHA256 &&
			(!target.HasMode || target.BackendID != targetBackendFile || fileModeCompatible(state.Mode, target.Mode)) {
			return recoveryStateBefore, nil
		}
		return "", apperror.New(apperror.TargetChanged, "target no longer matches a recognized recovery state").WithDetail("target_id", target.TargetID)
	}
	if err := executor.Verify(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{
		Exists: target.FileExists, Fingerprint: target.DesiredSHA256, OpaqueState: target.PrivateLocator,
	}); err != nil {
		return "", err
	}
	return recoveryStateBefore, nil
}

func (service *Service) applyRecoveryTargets(
	ctx context.Context,
	db *store.Store,
	operationID string,
	lastMetadata string,
	metadataBase recoveryOperationMetadata,
	source recoverySource,
) (RecoveryCounts, []string, error) {
	var counts RecoveryCounts
	processed := []string{}
	executor := service.transactionExecutor()
	for _, target := range source.Targets {
		state, err := service.inspectRecoveryTargetState(ctx, target)
		if err != nil {
			return counts, processed, failRecoveryWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
		}
		if state == recoveryStateBefore {
			counts.Noop++
			processed = append(processed, target.TargetID)
		} else {
			switch target.Action {
			case planActionUpdate:
				recoveryFile := filepath.Join(source.RecoveryPath, target.RecoveryRelPath)
				err = executor.Restore(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{
					Exists: true, Fingerprint: target.DesiredSHA256, OpaqueState: target.PrivateLocator,
				}, recoveryFile, target.BeforeSHA256, target.Mode, target.HasMode)
				if err == nil {
					counts.Restore++
				}
			case planActionCreate:
				var removed bool
				removed, err = executor.Remove(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{
					Exists: true, Fingerprint: target.DesiredSHA256,
				}, true)
				if err == nil && removed {
					counts.Remove++
				} else if err == nil {
					counts.Noop++
				}
			case planActionNoop:
				counts.Noop++
			}
			if err != nil {
				return counts, processed, failRecoveryWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			processed = append(processed, target.TargetID)
		}
		metadataBase.Counts = counts
		metadataBase.ProcessedTargets = processed
		metadataJSON, err := marshalRecoveryOperationMetadata("restoring", metadataBase)
		if err != nil {
			return counts, processed, failRecoveryOperation(ctx, db, operationID, lastMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode recovery operation metadata", err))
		}
		if err := db.UpdateOperationMetadata(ctx, operationID, metadataJSON); err != nil {
			return counts, processed, failRecoveryOperation(ctx, db, operationID, metadataJSON, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update recovery operation metadata", err))
		}
		lastMetadata = metadataJSON
	}
	return counts, processed, nil
}

func (service *Service) verifyRestoredRecoveryTargets(ctx context.Context, targets []recoveryTarget) error {
	for _, target := range targets {
		state, err := service.inspectRecoveryTargetState(ctx, target)
		if err != nil {
			return err
		}
		if state != recoveryStateBefore {
			return apperror.New(apperror.TargetChanged, "target was not restored to its pre-switch state").WithDetail("target_id", target.TargetID)
		}
	}
	return nil
}

// Windows does not expose exact POSIX permission bits through os.FileMode.
func fileModeCompatible(actual, expected os.FileMode) bool {
	return goruntime.GOOS == "windows" || actual.Perm() == expected.Perm()
}

func entryPrivateLocator(entries map[string]transaction.Entry, targetID string) string {
	if entry, ok := entries[targetID]; ok {
		return entry.PrivateLocator
	}
	return ""
}

func recoveryMetadata(source recoverySource) recoveryOperationMetadata {
	return recoveryOperationMetadata{
		SourceOperationID: source.Operation.ID,
		ProviderID:        source.Metadata.ProviderID,
		ProfileID:         source.Metadata.ProfileID,
		RestoredProfileID: restoredProfileID(source.Metadata.PreviousActive),
		Targets:           recoveryTargetMetadataList(source.Targets),
	}
}

func recoveryTargetMetadataList(targets []recoveryTarget) []recoveryTargetMetadata {
	result := make([]recoveryTargetMetadata, 0, len(targets))
	for _, target := range targets {
		result = append(result, recoveryTargetMetadata{
			TargetID:  target.TargetID,
			BackendID: target.BackendID,
			Action:    target.Action,
		})
	}
	return result
}

func marshalRecoveryOperationMetadata(checkpoint string, metadata recoveryOperationMetadata) (string, error) {
	metadata.Checkpoint = checkpoint
	metadata.UpdatedAtUnixMS = time.Now().UnixMilli()
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func failRecoveryWithProcessed(
	ctx context.Context,
	db *store.Store,
	operationID string,
	lastMetadata string,
	metadataBase recoveryOperationMetadata,
	counts RecoveryCounts,
	processed []string,
	operationErr error,
) error {
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	metadataJSON, err := marshalRecoveryOperationMetadata("failed", metadataBase)
	if err != nil {
		return failRecoveryOperation(ctx, db, operationID, lastMetadata, mapTargetFSError(operationErr))
	}
	return failRecoveryOperation(ctx, db, operationID, metadataJSON, mapTargetFSError(operationErr))
}

func failRecoveryOperation(ctx context.Context, db *store.Store, operationID, metadataJSON string, operationErr error) error {
	code, message := errorCodeAndMessage(operationErr)
	cleanupCtx, cancel := switchCleanupContext(ctx)
	defer cancel()
	if err := db.MarkOperationFailed(cleanupCtx, store.MarkOperationFailedParams{
		ID: operationID, ErrorCode: string(code), ErrorMessage: message, MetadataJSON: &metadataJSON,
	}); err != nil {
		return preserveSwitchOperationError(operationErr, err)
	}
	// Failed recovery attempts are leaves, not new roots. The original switch
	// remains unresolved and can be retried against the same recovery point.
	if err := db.ResolveOperation(cleanupCtx, operationID, "recovery_attempt_failed"); err != nil {
		return preserveSwitchOperationError(operationErr, err)
	}
	return operationErr
}

func restoredProfileID(previous *switchPreviousActiveState) string {
	if previous == nil || !previous.Exists {
		return ""
	}
	return previous.ProfileID
}

func restoredStoreActiveState(previous *switchPreviousActiveState) *store.RecoveryActiveStateParams {
	if previous == nil || !previous.Exists {
		return nil
	}
	return &store.RecoveryActiveStateParams{ProfileID: previous.ProfileID, OperationID: previous.OperationID}
}
