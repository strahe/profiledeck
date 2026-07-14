package switching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const rollbackOperationRandomBytes = 6

type ApplyRollbackRequest struct {
	BackupID string `json:"backup_id"`
	Confirm  bool   `json:"confirm"`
}

type ApplyRollbackResult struct {
	OperationID       string         `json:"operation_id"`
	Status            string         `json:"status"`
	SourceOperationID string         `json:"source_operation_id"`
	ProviderID        string         `json:"provider_id"`
	ProfileID         string         `json:"profile_id"`
	RestoredProfileID string         `json:"restored_profile_id"`
	Counts            RollbackCounts `json:"counts"`
	BackupPath        string         `json:"backup_path"`
	Warnings          []string       `json:"warnings"`
}

type RollbackCounts struct {
	Restore int `json:"restore"`
	Remove  int `json:"remove"`
	Noop    int `json:"noop"`
}

type ListBackupsResult struct {
	Backups []BackupSummary `json:"backups"`
}

type BackupDetail struct {
	BackupSummary
	Entries []BackupEntrySummary `json:"entries"`
}

type BackupSummary struct {
	BackupID          string `json:"backup_id"`
	Path              string `json:"path"`
	OperationID       string `json:"operation_id"`
	OperationType     string `json:"operation_type"`
	OperationStatus   string `json:"operation_status"`
	ProviderID        string `json:"provider_id"`
	ProfileID         string `json:"profile_id"`
	PlanFingerprint   string `json:"plan_fingerprint"`
	CreatedAtUnixMS   int64  `json:"created_at_unix_ms"`
	EntryCount        int    `json:"entry_count"`
	Valid             bool   `json:"valid"`
	InvalidReason     string `json:"invalid_reason,omitempty"`
	RollbackSupported bool   `json:"rollback_supported"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
}

type BackupEntrySummary struct {
	TargetID     string `json:"target_id"`
	BackendID    string `json:"backend_id"`
	TargetLabel  string `json:"target_label"`
	Path         string `json:"path"`
	Action       string `json:"action"`
	Existed      bool   `json:"existed"`
	BeforeSHA256 string `json:"before_sha256"`
	Mode         string `json:"mode,omitempty"`
}

type rollbackOperationMetadata struct {
	Checkpoint        string                   `json:"checkpoint"`
	RollbackKind      string                   `json:"rollback_kind,omitempty"`
	SourceOperationID string                   `json:"source_operation_id,omitempty"`
	BackupID          string                   `json:"backup_id,omitempty"`
	ProviderID        string                   `json:"provider_id,omitempty"`
	ProfileID         string                   `json:"profile_id,omitempty"`
	RestoredProfileID string                   `json:"restored_profile_id,omitempty"`
	CurrentBackupPath string                   `json:"current_backup_path,omitempty"`
	Counts            RollbackCounts           `json:"counts"`
	Targets           []rollbackTargetMetadata `json:"targets,omitempty"`
	ProcessedTargets  []string                 `json:"processed_targets,omitempty"`
	Warnings          []string                 `json:"warnings,omitempty"`
	UpdatedAtUnixMS   int64                    `json:"updated_at_unix_ms"`
}

type rollbackTargetMetadata struct {
	TargetID       string `json:"target_id"`
	BackendID      string `json:"backend_id"`
	TargetLabel    string `json:"target_label"`
	Path           string `json:"path"`
	Action         string `json:"action"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	BeforeSHA256   string `json:"before_sha256,omitempty"`
	BackupRelPath  string `json:"backup_rel_path,omitempty"`
}

type rollbackSource struct {
	Operation  store.Operation
	Manifest   transaction.Manifest
	Metadata   switchOperationMetadata
	Targets    []rollbackTarget
	BackupPath string
}

type rollbackTarget struct {
	TargetID       string
	BackendID      string
	TargetLabel    string
	Path           string
	Action         string
	FileExists     bool
	BeforeSHA256   string
	DesiredSHA256  string
	BackupRelPath  string
	Mode           os.FileMode
	HasMode        bool
	Spec           targetSpec
	PrivateLocator string
}

func (service *Service) Rollback(ctx context.Context, req ApplyRollbackRequest) (ApplyRollbackResult, error) {
	backupID, appErr := validateBackupID(req.BackupID)
	if appErr != nil {
		return ApplyRollbackResult{}, appErr
	}
	if !req.Confirm {
		return ApplyRollbackResult{}, apperror.New(apperror.ConfirmationRequired, "rollback apply requires confirmation")
	}

	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return ApplyRollbackResult{}, err
	}
	defer db.Close()

	sourceProfileID, err := rollbackSourceProfileID(ctx, db, backupID)
	if err != nil {
		return ApplyRollbackResult{}, err
	}

	operationID, err := newRollbackOperationID(time.Now())
	if err != nil {
		return ApplyRollbackResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create rollback operation id", err)
	}
	initialMetadata, err := marshalRollbackOperationMetadata("created", rollbackOperationMetadata{
		BackupID: backupID,
	})
	if err != nil {
		return ApplyRollbackResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to encode rollback operation metadata", err)
	}
	if _, err := db.CreatePendingRollbackOperation(ctx, store.CreateRollbackOperationParams{
		ID:           operationID,
		ProfileID:    sourceProfileID,
		MetadataJSON: initialMetadata,
	}); err != nil {
		return ApplyRollbackResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create rollback operation", err)
	}

	lock, err := acquireSwitchLock(service.paths.Lock, operationID)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, err)
	}
	defer lock.Release()

	source, err := service.loadRollbackSource(ctx, db, service.paths, backupID, true)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, err)
	}

	metadataBase := rollbackOperationMetadata{
		SourceOperationID: source.Operation.ID,
		BackupID:          backupID,
		ProviderID:        source.Metadata.ProviderID,
		ProfileID:         source.Metadata.ProfileID,
		RestoredProfileID: restoredProfileID(source.Metadata.PreviousActive),
		Targets:           rollbackTargetMetadataList(source.Targets),
	}
	validatedMetadata, err := marshalRollbackOperationMetadata("validated", metadataBase)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode rollback operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, validatedMetadata); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update rollback operation metadata", err))
	}

	if err := service.verifyRollbackTargets(ctx, source.Targets); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, err)
	}

	currentBackup, err := service.createRollbackCurrentBackup(ctx, service.paths, operationID, source)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, err)
	}
	metadataBase.CurrentBackupPath = currentBackup.Path
	backedUpMetadata, err := marshalRollbackOperationMetadata("backed_up", metadataBase)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode rollback operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, backedUpMetadata); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update rollback operation metadata", err))
	}

	if err := service.verifyRollbackTargets(ctx, source.Targets); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, backedUpMetadata, err)
	}

	counts, processed, err := service.applyRollbackTargets(ctx, db, operationID, backedUpMetadata, metadataBase, source)
	if err != nil {
		return ApplyRollbackResult{}, err
	}
	// Re-read the restored targets before committing active state. External
	// writers can still race after this check, but an already diverged restore
	// must leave the rollback visible as failed.
	if err := service.verifyRestoredRollbackTargets(ctx, source.Targets); err != nil {
		return ApplyRollbackResult{}, failRollbackWithProcessed(ctx, db, operationID, backedUpMetadata, metadataBase, counts, processed, err)
	}
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	appliedMetadata, err := marshalRollbackOperationMetadata("applied", metadataBase)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, backedUpMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode rollback operation metadata", err))
	}

	restoredActive := restoredStoreActiveState(source.Metadata.PreviousActive)
	if err := db.CompleteRollbackOperation(ctx, store.CompleteRollbackOperationParams{
		ID:                  operationID,
		ProfileID:           restoredProfileID(source.Metadata.PreviousActive),
		ProviderID:          source.Metadata.ProviderID,
		RestoredActiveState: restoredActive,
		MetadataJSON:        appliedMetadata,
	}); err != nil {
		failedMetadata, metadataErr := marshalRollbackOperationMetadata("db_update_failed", metadataBase)
		if metadataErr != nil {
			failedMetadata = backedUpMetadata
		}
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, failedMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to complete rollback operation", err))
	}

	return ApplyRollbackResult{
		OperationID:       operationID,
		Status:            store.OperationStatusApplied,
		SourceOperationID: source.Operation.ID,
		ProviderID:        source.Metadata.ProviderID,
		ProfileID:         source.Metadata.ProfileID,
		RestoredProfileID: restoredProfileID(source.Metadata.PreviousActive),
		Counts:            counts,
		BackupPath:        currentBackup.Path,
	}, nil
}

func (service *Service) ListBackups(ctx context.Context) (ListBackupsResult, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ListBackupsResult{}, err
	}
	defer db.Close()

	entries, err := os.ReadDir(service.paths.Backups)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ListBackupsResult{}, nil
		}
		return ListBackupsResult{}, apperror.Wrap(apperror.BackupFailed, "failed to read backups directory", err)
	}

	backups := make([]BackupSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summary := service.inspectBackup(ctx, db, service.paths, entry.Name())
		backups = append(backups, summary)
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAtUnixMS == backups[j].CreatedAtUnixMS {
			return backups[i].BackupID < backups[j].BackupID
		}
		return backups[i].CreatedAtUnixMS < backups[j].CreatedAtUnixMS
	})
	return ListBackupsResult{Backups: backups}, nil
}

func (service *Service) ShowBackup(ctx context.Context, rawBackupID string) (BackupDetail, error) {
	backupID, appErr := validateBackupID(rawBackupID)
	if appErr != nil {
		return BackupDetail{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return BackupDetail{}, err
	}
	defer db.Close()

	backupPath := filepath.Join(service.paths.Backups, backupID)
	info, err := os.Stat(backupPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BackupDetail{}, apperror.New(apperror.BackupNotFound, "backup not found").WithDetail("backup_id", backupID)
		}
		return BackupDetail{}, apperror.Wrap(apperror.BackupFailed, "failed to inspect backup", err).WithDetail("backup_id", backupID)
	}
	if !info.IsDir() {
		return BackupDetail{}, apperror.New(apperror.BackupInvalid, "backup path is not a directory").WithDetail("backup_id", backupID)
	}

	summary := service.inspectBackup(ctx, db, service.paths, backupID)
	detail := BackupDetail{BackupSummary: summary}
	if summary.Valid {
		manifest, err := transaction.LoadManifest(filepath.Join(service.paths.Backups, backupID))
		if err == nil {
			detail.Entries = backupEntrySummaries(manifest.Entries)
		}
	}
	return detail, nil
}

func newRollbackOperationID(now time.Time) (string, error) {
	randomBytes := make([]byte, rollbackOperationRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("rollback-%d-%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func validateBackupID(raw string) (string, *apperror.Error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", apperror.New(apperror.BackupInvalid, "backup id is required")
	}
	if id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return "", apperror.New(apperror.BackupInvalid, "backup id must be a safe directory name")
	}
	return id, nil
}

func rollbackSourceProfileID(ctx context.Context, db *store.Store, backupID string) (string, error) {
	operation, err := db.GetOperation(ctx, backupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", apperror.New(apperror.BackupNotFound, "backup operation not found").WithDetail("backup_id", backupID)
		}
		return "", apperror.Wrap(apperror.StoreStatusFailed, "failed to read backup operation", err)
	}
	if operation.OperationType != store.OperationTypeSwitch {
		return "", apperror.New(apperror.RollbackUnsupported, "backup is not from a switch operation").WithDetail("backup_id", backupID)
	}
	if operation.Status != store.OperationStatusApplied {
		return "", apperror.New(apperror.RollbackUnsupported, "source switch operation is not applied").WithDetail("backup_id", backupID)
	}
	if operation.ProfileID != "" {
		return operation.ProfileID, nil
	}

	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		return "", apperror.Wrap(apperror.RollbackUnsupported, "source switch metadata is invalid", err).WithDetail("backup_id", backupID)
	}
	if metadata.ProfileID == "" {
		return "", apperror.New(apperror.RollbackUnsupported, "source switch metadata is incomplete").WithDetail("backup_id", backupID)
	}
	return metadata.ProfileID, nil
}

func (service *Service) loadRollbackSource(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string, requireActive bool) (rollbackSource, error) {
	operation, err := db.GetOperation(ctx, backupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return rollbackSource{}, apperror.New(apperror.BackupNotFound, "backup operation not found").WithDetail("backup_id", backupID)
		}
		return rollbackSource{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read backup operation", err)
	}
	if operation.OperationType != store.OperationTypeSwitch {
		return rollbackSource{}, apperror.New(apperror.RollbackUnsupported, "backup is not from a switch operation").WithDetail("backup_id", backupID)
	}
	if operation.Status != store.OperationStatusApplied {
		return rollbackSource{}, apperror.New(apperror.RollbackUnsupported, "source switch operation is not applied").WithDetail("backup_id", backupID)
	}

	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		return rollbackSource{}, apperror.Wrap(apperror.RollbackUnsupported, "source switch metadata is invalid", err).WithDetail("backup_id", backupID)
	}
	if metadata.PreviousActive == nil {
		return rollbackSource{}, apperror.New(apperror.RollbackUnsupported, "source switch metadata does not include previous active state").WithDetail("backup_id", backupID)
	}
	if metadata.ProviderID == "" || metadata.ProfileID == "" || metadata.PlanFingerprint == "" {
		return rollbackSource{}, apperror.New(apperror.RollbackUnsupported, "source switch metadata is incomplete").WithDetail("backup_id", backupID)
	}

	backupPath := filepath.Join(paths.Backups, backupID)
	manifest, err := transaction.LoadManifest(backupPath)
	if err != nil {
		return rollbackSource{}, err
	}
	if err := validateRollbackManifest(manifest, metadata, operation.ID, backupID, backupPath); err != nil {
		return rollbackSource{}, err
	}

	adapter, err := service.rollbackAdapter(ctx, db, metadata)
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

	if requireActive {
		activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, metadata.ProviderID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return rollbackSource{}, apperror.New(apperror.TargetChanged, "active state no longer points to source switch").WithDetail("backup_id", backupID)
			}
			return rollbackSource{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active state", err)
		}
		if activeState.OperationID != operation.ID || activeState.ProfileID != metadata.ProfileID {
			return rollbackSource{}, apperror.New(apperror.TargetChanged, "active state no longer points to source switch").WithDetail("backup_id", backupID)
		}
	}

	return rollbackSource{
		Operation:  operation,
		Manifest:   manifest,
		Metadata:   metadata,
		Targets:    targets,
		BackupPath: backupPath,
	}, nil
}

func validateRollbackManifest(manifest transaction.Manifest, metadata switchOperationMetadata, operationID, backupID, backupPath string) error {
	if manifest.OperationID != backupID || manifest.OperationID != operationID {
		return apperror.New(apperror.BackupInvalid, "backup manifest operation id does not match source operation").WithDetail("backup_id", backupID)
	}
	if manifest.ProviderID != metadata.ProviderID || manifest.ProfileID != metadata.ProfileID || manifest.PlanFingerprint != metadata.PlanFingerprint {
		return apperror.New(apperror.BackupInvalid, "backup manifest metadata does not match source operation").WithDetail("backup_id", backupID)
	}
	for _, entry := range manifest.Entries {
		if entry.BackupRelPath != "" {
			if _, err := safeBackupRelPath(backupPath, entry.BackupRelPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func rollbackTargetsFromMetadata(metadata switchOperationMetadata, manifest transaction.Manifest, backupPath string) ([]rollbackTarget, error) {
	return rollbackTargetsFromMetadataWithAdapter(metadata, manifest, backupPath, nil)
}

func rollbackTargetsFromMetadataWithAdapter(metadata switchOperationMetadata, manifest transaction.Manifest, backupPath string, adapter switchplan.Adapter) ([]rollbackTarget, error) {
	entries := map[string]transaction.Entry{}
	for _, entry := range manifest.Entries {
		if entry.TargetID == "" {
			return nil, apperror.New(apperror.BackupInvalid, "backup manifest entry target id is empty")
		}
		if _, exists := entries[entry.TargetID]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "backup manifest contains duplicate target").WithDetail("target_id", entry.TargetID)
		}
		entries[entry.TargetID] = entry
	}

	targets := make([]rollbackTarget, 0, len(metadata.Targets))
	seenTargetIDs := make(map[string]struct{}, len(metadata.Targets))
	seenLocators := make(map[string]string, len(metadata.Targets))
	for _, target := range metadata.Targets {
		backendID := target.BackendID
		if backendID == "" {
			backendID = targetBackendFile
		}
		if target.TargetID == "" || target.DesiredSHA256 == "" || (backendID == targetBackendFile && target.Path == "") {
			return nil, apperror.New(apperror.RollbackUnsupported, "source switch target metadata is incomplete").WithDetail("target_id", target.TargetID)
		}
		if _, exists := seenTargetIDs[target.TargetID]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "source switch metadata contains duplicate target IDs").WithDetail("target_id", target.TargetID)
		}
		seenTargetIDs[target.TargetID] = struct{}{}
		var spec targetSpec
		var err error
		if adapter != nil {
			spec, err = adapter.ResolveTargetSpec(metadata.ProviderID, target.TargetID, backendID, target.Path, target.TargetLabel)
		} else if backendID == targetBackendFile {
			spec, err = resolveFileTargetSpec(target.TargetID, backendID, target.Path, target.TargetLabel)
		} else {
			err = apperror.New(apperror.RollbackUnsupported, "source switch adapter is unavailable").WithDetail("backend_id", backendID)
		}
		if err != nil {
			return nil, err
		}
		locatorKey := spec.BackendID() + "\x00" + spec.LocatorFingerprint()
		if firstTargetID, exists := seenLocators[locatorKey]; exists {
			return nil, apperror.New(apperror.BackupInvalid, "source switch metadata contains duplicate target locators").
				WithDetail("target_id", target.TargetID).WithDetail("first_target_id", firstTargetID)
		}
		seenLocators[locatorKey] = target.TargetID
		rollbackTarget := rollbackTarget{
			TargetID:       target.TargetID,
			BackendID:      backendID,
			TargetLabel:    target.TargetLabel,
			Path:           target.Path,
			Action:         target.Action,
			FileExists:     target.FileExists,
			BeforeSHA256:   target.BeforeSHA256,
			DesiredSHA256:  target.DesiredSHA256,
			Spec:           spec,
			PrivateLocator: entryPrivateLocator(entries, target.TargetID),
		}
		switch target.Action {
		case planActionCreate, planActionUpdate:
			entry, ok := entries[target.TargetID]
			if !ok {
				return nil, apperror.New(apperror.BackupInvalid, "backup manifest is missing target entry").WithDetail("target_id", target.TargetID)
			}
			if err := validateRollbackEntry(target, entry, spec); err != nil {
				return nil, err
			}
			delete(entries, target.TargetID)
			if entry.BackupRelPath != "" {
				relPath, err := safeBackupRelPath(backupPath, entry.BackupRelPath)
				if err != nil {
					return nil, err
				}
				rollbackTarget.BackupRelPath = relPath
			}
			if entry.Mode != "" {
				mode, err := parseFileMode(entry.Mode)
				if err != nil {
					return nil, apperror.New(apperror.BackupInvalid, "backup manifest file mode is invalid").WithDetail("target_id", target.TargetID)
				}
				rollbackTarget.Mode = mode
				rollbackTarget.HasMode = true
			}
			if modeSpec, ok := spec.(switchtarget.RecoveryModeSpec); ok {
				rollbackTarget.Mode, rollbackTarget.HasMode = modeSpec.RecoveryMode(rollbackTarget.Mode, rollbackTarget.HasMode)
			}
		case planActionNoop:
			if _, requiresRecoveryIdentity := spec.(switchtarget.RecoveryIdentitySpec); requiresRecoveryIdentity {
				entry, ok := entries[target.TargetID]
				if !ok {
					return nil, apperror.New(apperror.BackupInvalid, "backup is missing target recovery identity").WithDetail("target_id", target.TargetID)
				}
				if err := validateRollbackEntry(target, entry, spec); err != nil {
					return nil, err
				}
				delete(entries, target.TargetID)
			}
		default:
			return nil, apperror.New(apperror.RollbackUnsupported, "source switch target action is unsupported").WithDetail("target_id", target.TargetID)
		}
		targets = append(targets, rollbackTarget)
	}
	if len(entries) != 0 {
		for targetID := range entries {
			return nil, apperror.New(apperror.BackupInvalid, "backup manifest contains unknown target entry").WithDetail("target_id", targetID)
		}
	}
	return targets, nil
}

func switchMetadataNeedsAdapter(metadata switchOperationMetadata) bool {
	for _, target := range metadata.Targets {
		backendID := firstNonEmpty(target.BackendID, targetBackendFile)
		if backendID != targetBackendFile {
			return true
		}
	}
	return false
}

func (service *Service) rollbackAdapter(ctx context.Context, db *store.Store, metadata switchOperationMetadata) (switchplan.Adapter, error) {
	if adapter, ok := service.dependencies.Adapters.ManagedAdapter(metadata.ProviderID); ok {
		return adapter, nil
	}
	provider, err := db.GetProvider(ctx, metadata.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		if switchMetadataNeedsAdapter(metadata) {
			return nil, apperror.New(apperror.RollbackUnsupported, "source switch Provider is unavailable").WithDetail("provider_id", metadata.ProviderID)
		}
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read source switch Provider", err)
	}
	adapter, ok := service.dependencies.Adapters.Adapter(provider.AdapterID)
	if !ok {
		if switchMetadataNeedsAdapter(metadata) {
			return nil, apperror.New(apperror.RollbackUnsupported, "source switch adapter is unavailable").WithDetail("adapter_id", provider.AdapterID)
		}
		return nil, nil
	}
	return adapter, nil
}

func validateSourceBackupFiles(ctx context.Context, backupPath string, targets []rollbackTarget) error {
	for _, target := range targets {
		if target.Action != planActionUpdate {
			continue
		}
		backupFile := filepath.Join(backupPath, target.BackupRelPath)
		state, err := targetfs.Inspect(ctx, backupFile)
		if err != nil {
			return mapTargetFSError(err)
		}
		if !state.Exists || state.IsSymlink || state.IsDir || !state.IsRegular {
			return apperror.New(apperror.BackupInvalid, "source backup file is missing or invalid").WithDetail("target_id", target.TargetID)
		}
		if state.SHA256 != target.BeforeSHA256 {
			return apperror.New(apperror.BackupInvalid, "source backup file hash does not match manifest").WithDetail("target_id", target.TargetID)
		}
	}
	return nil
}

func validateRollbackEntry(target switchOperationTargetMetadata, entry transaction.Entry, spec targetSpec) error {
	targetBackend := target.BackendID
	if targetBackend == "" {
		targetBackend = targetBackendFile
	}
	entryBackend := entry.BackendID
	if entryBackend == "" {
		entryBackend = targetBackendFile
	}
	if entryBackend != targetBackend || entry.Path != target.Path || entry.Action != target.Action || entry.BeforeSHA256 != target.BeforeSHA256 {
		return apperror.New(apperror.BackupInvalid, "backup manifest entry does not match source target").WithDetail("target_id", target.TargetID)
	}
	_, requiresRecoveryIdentity := spec.(switchtarget.RecoveryIdentitySpec)
	if !requiresRecoveryIdentity && entry.PrivateLocator != "" {
		return apperror.New(apperror.BackupInvalid, "backup manifest contains unexpected private recovery state").WithDetail("target_id", target.TargetID)
	}
	switch target.Action {
	case planActionCreate:
		if entry.Existed || entry.BackupRelPath != "" {
			return apperror.New(apperror.BackupInvalid, "create backup entry must not contain previous file content").WithDetail("target_id", target.TargetID)
		}
	case planActionUpdate:
		if !entry.Existed || entry.BackupRelPath == "" {
			return apperror.New(apperror.BackupInvalid, "update backup entry must contain previous file content").WithDetail("target_id", target.TargetID)
		}
		if requiresRecoveryIdentity && entry.PrivateLocator == "" {
			return apperror.New(apperror.BackupInvalid, "backup manifest is missing target recovery identity").WithDetail("target_id", target.TargetID)
		}
	case planActionNoop:
		if !requiresRecoveryIdentity || !entry.Existed || entry.BackupRelPath != "" || entry.PrivateLocator == "" {
			return apperror.New(apperror.BackupInvalid, "no-op backup entry contains unsupported recovery state").WithDetail("target_id", target.TargetID)
		}
	default:
		return apperror.New(apperror.RollbackUnsupported, "backup entry action is unsupported").WithDetail("target_id", target.TargetID)
	}
	return nil
}

func safeBackupRelPath(backupPath, raw string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(raw))
	if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", apperror.New(apperror.BackupInvalid, "backup relative path is invalid").WithDetail("path", raw)
	}
	fullPath := filepath.Join(backupPath, rel)
	if !strings.HasPrefix(fullPath, filepath.Clean(backupPath)+string(os.PathSeparator)) {
		return "", apperror.New(apperror.BackupInvalid, "backup relative path escapes backup directory").WithDetail("path", raw)
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

func (service *Service) verifyRollbackTargets(ctx context.Context, targets []rollbackTarget) error {
	for _, target := range targets {
		if err := service.verifyRollbackTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) verifyRollbackTarget(ctx context.Context, target rollbackTarget) error {
	executor := service.transactionExecutor()
	state, err := executor.Inspect(ctx, target.BackendID, target.Spec)
	if err != nil {
		return err
	}
	if target.Action == planActionCreate {
		if !state.Exists {
			return nil
		}
		if state.IsSymlink {
			return apperror.New(apperror.TargetChanged, "target is not writable").WithDetail("target_id", target.TargetID)
		}
		if state.Fingerprint != target.DesiredSHA256 {
			return apperror.New(apperror.TargetChanged, "target content changed").WithDetail("target_id", target.TargetID)
		}
		return nil
	}
	if target.Action == planActionUpdate {
		_, err := service.rollbackUpdateAlreadyRestored(ctx, target)
		return err
	}
	return executor.Verify(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{
		Exists: target.FileExists, Fingerprint: target.DesiredSHA256, OpaqueState: target.PrivateLocator,
	})
}

func (service *Service) rollbackUpdateAlreadyRestored(ctx context.Context, target rollbackTarget) (bool, error) {
	state, err := service.transactionExecutor().Inspect(ctx, target.BackendID, target.Spec)
	if err != nil {
		return false, err
	}
	if !state.Exists {
		return false, apperror.New(apperror.TargetChanged, "target disappeared").WithDetail("target_id", target.TargetID)
	}
	if target.PrivateLocator != "" && state.OpaqueState != target.PrivateLocator {
		return false, apperror.New(apperror.TargetChanged, "credential store item was replaced").WithDetail("target_id", target.TargetID)
	}
	if state.IsSymlink {
		return false, apperror.New(apperror.TargetChanged, "target is not writable").WithDetail("target_id", target.TargetID)
	}
	if state.Fingerprint == target.DesiredSHA256 {
		return false, nil
	}
	if target.BeforeSHA256 != "" && state.Fingerprint == target.BeforeSHA256 {
		if !target.HasMode || target.BackendID != targetBackendFile || fileModeCompatible(state.Mode, target.Mode) {
			return true, nil
		}
	}
	return false, apperror.New(apperror.TargetChanged, "target content changed").WithDetail("target_id", target.TargetID)
}

func (service *Service) createRollbackCurrentBackup(ctx context.Context, paths runtime.Paths, operationID string, source rollbackSource) (switchBackup, error) {
	executor := service.transactionExecutor()
	operations := make([]transaction.Operation, 0, len(source.Targets))
	for _, target := range source.Targets {
		if target.Action != planActionCreate && target.Action != planActionUpdate {
			continue
		}
		state, err := executor.Inspect(ctx, target.BackendID, target.Spec)
		if err != nil {
			return switchBackup{}, err
		}
		operations = append(operations, transaction.Operation{
			TargetID: target.TargetID, BackendID: target.BackendID, TargetLabel: target.TargetLabel,
			Path: target.Path, Action: target.Action, FileExists: state.Exists, BeforeSHA256: state.Fingerprint,
			BeforeMode: state.Mode, Spec: target.Spec, Snapshot: state,
		})
	}
	backup, err := executor.CreateBackup(ctx, transaction.BackupRequest{
		BackupsDir: paths.Backups, OperationID: operationID, ProviderID: source.Metadata.ProviderID,
		ProfileID: source.Metadata.ProfileID, PlanFingerprint: source.Metadata.PlanFingerprint, Operations: operations,
	})
	if err != nil {
		return switchBackup{}, err
	}
	return switchBackup{Path: backup.Path, Entries: backup.Entries}, nil
}

func (service *Service) applyRollbackTargets(ctx context.Context, db *store.Store, operationID, lastMetadata string, metadataBase rollbackOperationMetadata, source rollbackSource) (RollbackCounts, []string, error) {
	var counts RollbackCounts
	processed := []string{}
	executor := service.transactionExecutor()
	for _, target := range source.Targets {
		switch target.Action {
		case planActionUpdate:
			alreadyRestored, err := service.rollbackUpdateAlreadyRestored(ctx, target)
			if err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			if alreadyRestored {
				counts.Noop++
				processed = append(processed, target.TargetID)
				break
			}
			backupFile := filepath.Join(source.BackupPath, target.BackupRelPath)
			err = executor.Restore(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{Exists: true, Fingerprint: target.DesiredSHA256, OpaqueState: target.PrivateLocator},
				backupFile, target.BeforeSHA256, target.Mode, target.HasMode)
			if err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			counts.Restore++
			processed = append(processed, target.TargetID)
		case planActionCreate:
			removed, err := executor.Remove(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{Exists: true, Fingerprint: target.DesiredSHA256}, true)
			if err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			if removed {
				counts.Remove++
			} else {
				counts.Noop++
			}
			processed = append(processed, target.TargetID)
		case planActionNoop:
			if err := executor.Verify(ctx, target.BackendID, target.Spec, switchtarget.Snapshot{
				Exists: target.FileExists, Fingerprint: target.DesiredSHA256, OpaqueState: target.PrivateLocator,
			}); err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			counts.Noop++
			processed = append(processed, target.TargetID)
		}
		metadataBase.Counts = counts
		metadataBase.ProcessedTargets = processed
		metadataJSON, err := marshalRollbackOperationMetadata("restoring", metadataBase)
		if err != nil {
			return counts, processed, failRollbackOperation(ctx, db, operationID, lastMetadata, apperror.Wrap(apperror.OperationUpdateFailed, "failed to encode rollback operation metadata", err))
		}
		if err := db.UpdateOperationMetadata(ctx, operationID, metadataJSON); err != nil {
			return counts, processed, failRollbackOperation(ctx, db, operationID, metadataJSON, apperror.Wrap(apperror.OperationUpdateFailed, "failed to update rollback operation metadata", err))
		}
		lastMetadata = metadataJSON
	}
	return counts, processed, nil
}

func (service *Service) verifyRestoredRollbackTargets(ctx context.Context, targets []rollbackTarget) error {
	executor := service.transactionExecutor()
	for _, target := range targets {
		expected := switchtarget.Snapshot{OpaqueState: target.PrivateLocator}
		switch target.Action {
		case planActionUpdate:
			expected.Exists = true
			expected.Fingerprint = target.BeforeSHA256
		case planActionCreate:
			expected.Exists = false
		case planActionNoop:
			expected.Exists = target.FileExists
			expected.Fingerprint = target.DesiredSHA256
		default:
			return apperror.New(apperror.RollbackUnsupported, "rollback target action is unsupported").WithDetail("target_id", target.TargetID)
		}
		if err := executor.Verify(ctx, target.BackendID, target.Spec, expected); err != nil {
			return err
		}
		if target.HasMode && target.BackendID == targetBackendFile && expected.Exists && goruntime.GOOS != "windows" {
			state, err := executor.Inspect(ctx, target.BackendID, target.Spec)
			if err != nil {
				return err
			}
			if !state.Exists || state.IsSymlink || state.Fingerprint != expected.Fingerprint || state.Mode.Perm() != target.Mode.Perm() {
				return apperror.New(apperror.TargetChanged, "restored target permissions or content changed during post-verify").WithDetail("target_id", target.TargetID)
			}
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

func failRollbackWithProcessed(ctx context.Context, db *store.Store, operationID, lastMetadata string, metadataBase rollbackOperationMetadata, counts RollbackCounts, processed []string, operationErr error) error {
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	metadataJSON, err := marshalRollbackOperationMetadata("failed", metadataBase)
	if err != nil {
		return failRollbackOperation(ctx, db, operationID, lastMetadata, mapTargetFSError(operationErr))
	}
	return failRollbackOperation(ctx, db, operationID, metadataJSON, mapTargetFSError(operationErr))
}

func restoredProfileID(previous *switchPreviousActiveState) string {
	if previous == nil || !previous.Exists {
		return ""
	}
	return previous.ProfileID
}

func restoredStoreActiveState(previous *switchPreviousActiveState) *store.RollbackActiveStateParams {
	if previous == nil || !previous.Exists {
		return nil
	}
	return &store.RollbackActiveStateParams{
		ProfileID:   previous.ProfileID,
		OperationID: previous.OperationID,
	}
}

func rollbackTargetMetadataList(targets []rollbackTarget) []rollbackTargetMetadata {
	result := make([]rollbackTargetMetadata, 0, len(targets))
	for _, target := range targets {
		result = append(result, rollbackTargetMetadata{
			TargetID:       target.TargetID,
			BackendID:      target.BackendID,
			TargetLabel:    target.TargetLabel,
			Path:           target.Path,
			Action:         target.Action,
			ExpectedSHA256: target.DesiredSHA256,
			BeforeSHA256:   target.BeforeSHA256,
			BackupRelPath:  target.BackupRelPath,
		})
	}
	return result
}

func marshalRollbackOperationMetadata(checkpoint string, metadata rollbackOperationMetadata) (string, error) {
	metadata.Checkpoint = checkpoint
	metadata.UpdatedAtUnixMS = time.Now().UnixMilli()
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func failRollbackOperation(ctx context.Context, db *store.Store, operationID, metadataJSON string, operationErr error) error {
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

func (service *Service) inspectBackup(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string) BackupSummary {
	backupPath := filepath.Join(paths.Backups, backupID)
	summary := BackupSummary{
		BackupID: backupID,
		Path:     backupPath,
	}
	manifest, err := transaction.LoadManifest(backupPath)
	if err != nil {
		summary.InvalidReason = err.Error()
		return summary
	}
	summary.Valid = true
	summary.OperationID = manifest.OperationID
	summary.ProviderID = manifest.ProviderID
	summary.ProfileID = manifest.ProfileID
	summary.PlanFingerprint = manifest.PlanFingerprint
	summary.CreatedAtUnixMS = manifest.CreatedAtUnixMS
	summary.EntryCount = len(manifest.Entries)

	operation, err := db.GetOperation(ctx, manifest.OperationID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			summary.UnsupportedReason = "operation not found"
			return summary
		}
		summary.UnsupportedReason = "operation read failed"
		return summary
	}
	summary.OperationType = operation.OperationType
	summary.OperationStatus = operation.Status

	reason := service.rollbackUnsupportedReason(ctx, db, paths, backupID)
	if reason == "" {
		summary.RollbackSupported = true
	} else {
		summary.UnsupportedReason = reason
	}
	return summary
}

func (service *Service) rollbackUnsupportedReason(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string) string {
	_, err := service.loadRollbackSource(ctx, db, paths, backupID, true)
	if err == nil {
		return ""
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

func backupEntrySummaries(entries []transaction.Entry) []BackupEntrySummary {
	result := make([]BackupEntrySummary, 0, len(entries))
	for _, entry := range entries {
		// Backup detail is an output boundary; payload-derived hashes remain in
		// private manifests for recovery and never enter the public DTO.
		summary := BackupEntrySummary{
			TargetID:     entry.TargetID,
			BackendID:    firstNonEmpty(entry.BackendID, targetBackendFile),
			TargetLabel:  entry.TargetLabel,
			Path:         entry.Path,
			Action:       entry.Action,
			Existed:      entry.Existed,
			BeforeSHA256: "",
			Mode:         entry.Mode,
		}
		if summary.BackendID != targetBackendFile {
			summary.Path = ""
			summary.Mode = ""
		}
		result = append(result, summary)
	}
	return result
}
