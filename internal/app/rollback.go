package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const rollbackOperationRandomBytes = 6

type ApplyRollbackRequest struct {
	ConfigDir string
	BackupID  string
	Confirm   bool
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

type ListBackupsRequest struct {
	ConfigDir string
}

type ListBackupsResult struct {
	Backups []BackupSummary `json:"backups"`
}

type ShowBackupRequest struct {
	ConfigDir string
	BackupID  string
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
	Manifest   switchBackupManifest
	Metadata   switchOperationMetadata
	Targets    []rollbackTarget
	BackupPath string
}

type rollbackTarget struct {
	TargetID      string
	BackendID     string
	TargetLabel   string
	Path          string
	Action        string
	FileExists    bool
	BeforeSHA256  string
	DesiredSHA256 string
	BackupRelPath string
	Mode          os.FileMode
	HasMode       bool
	Spec          targetSpec
}

func ApplyRollback(ctx context.Context, req ApplyRollbackRequest) (ApplyRollbackResult, error) {
	backupID, appErr := validateBackupID(req.BackupID)
	if appErr != nil {
		return ApplyRollbackResult{}, appErr
	}
	if !req.Confirm {
		return ApplyRollbackResult{}, NewError(ErrorConfirmationRequired, "rollback apply requires confirmation")
	}

	_, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return ApplyRollbackResult{}, err
	}
	if err := createRuntimeDirs(paths); err != nil {
		return ApplyRollbackResult{}, WrapError(ErrorRuntimeInitFailed, "failed to initialize runtime directories", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
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
		return ApplyRollbackResult{}, WrapError(ErrorOperationCreateFailed, "failed to create rollback operation id", err)
	}
	initialMetadata, err := marshalRollbackOperationMetadata("created", rollbackOperationMetadata{
		BackupID: backupID,
	})
	if err != nil {
		return ApplyRollbackResult{}, WrapError(ErrorOperationCreateFailed, "failed to encode rollback operation metadata", err)
	}
	if _, err := db.CreatePendingRollbackOperation(ctx, store.CreateRollbackOperationParams{
		ID:           operationID,
		ProfileID:    sourceProfileID,
		MetadataJSON: initialMetadata,
	}); err != nil {
		return ApplyRollbackResult{}, WrapError(ErrorOperationCreateFailed, "failed to create rollback operation", err)
	}

	lock, err := acquireSwitchLock(paths.Lock, operationID)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, err)
	}
	defer lock.Release()

	source, err := loadRollbackSource(ctx, db, paths, backupID, true)
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
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode rollback operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, validatedMetadata); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, initialMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update rollback operation metadata", err))
	}

	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, err)
	}

	currentBackup, err := createRollbackCurrentBackup(ctx, paths, operationID, source)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, err)
	}
	metadataBase.CurrentBackupPath = currentBackup.Path
	backedUpMetadata, err := marshalRollbackOperationMetadata("backed_up", metadataBase)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode rollback operation metadata", err))
	}
	if err := db.UpdateOperationMetadata(ctx, operationID, backedUpMetadata); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, validatedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to update rollback operation metadata", err))
	}

	if err := verifyRollbackTargets(ctx, source.Targets); err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, backedUpMetadata, err)
	}

	counts, processed, err := applyRollbackTargets(ctx, db, operationID, backedUpMetadata, metadataBase, source)
	if err != nil {
		return ApplyRollbackResult{}, err
	}
	metadataBase.Counts = counts
	metadataBase.ProcessedTargets = processed
	appliedMetadata, err := marshalRollbackOperationMetadata("applied", metadataBase)
	if err != nil {
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, backedUpMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode rollback operation metadata", err))
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
		return ApplyRollbackResult{}, failRollbackOperation(ctx, db, operationID, failedMetadata, WrapError(ErrorOperationUpdateFailed, "failed to complete rollback operation", err))
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

func ListBackups(ctx context.Context, req ListBackupsRequest) (ListBackupsResult, error) {
	_, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return ListBackupsResult{}, err
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return ListBackupsResult{}, err
	}
	defer db.Close()

	entries, err := os.ReadDir(paths.Backups)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ListBackupsResult{}, nil
		}
		return ListBackupsResult{}, WrapError(ErrorBackupFailed, "failed to read backups directory", err)
	}

	backups := make([]BackupSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summary := inspectBackup(ctx, db, paths, entry.Name())
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

func ShowBackup(ctx context.Context, req ShowBackupRequest) (BackupDetail, error) {
	backupID, appErr := validateBackupID(req.BackupID)
	if appErr != nil {
		return BackupDetail{}, appErr
	}
	_, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return BackupDetail{}, err
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return BackupDetail{}, err
	}
	defer db.Close()

	backupPath := filepath.Join(paths.Backups, backupID)
	info, err := os.Stat(backupPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BackupDetail{}, NewError(ErrorBackupNotFound, "backup not found").WithDetail("backup_id", backupID)
		}
		return BackupDetail{}, WrapError(ErrorBackupFailed, "failed to inspect backup", err).WithDetail("backup_id", backupID)
	}
	if !info.IsDir() {
		return BackupDetail{}, NewError(ErrorBackupInvalid, "backup path is not a directory").WithDetail("backup_id", backupID)
	}

	summary := inspectBackup(ctx, db, paths, backupID)
	detail := BackupDetail{BackupSummary: summary}
	if summary.Valid {
		manifest, err := loadBackupManifest(filepath.Join(paths.Backups, backupID))
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

func validateBackupID(raw string) (string, *AppError) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", NewError(ErrorBackupInvalid, "backup id is required")
	}
	if id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return "", NewError(ErrorBackupInvalid, "backup id must be a safe directory name")
	}
	return id, nil
}

func rollbackSourceProfileID(ctx context.Context, db *store.Store, backupID string) (string, error) {
	operation, err := db.GetOperation(ctx, backupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", NewError(ErrorBackupNotFound, "backup operation not found").WithDetail("backup_id", backupID)
		}
		return "", WrapError(ErrorStoreStatusFailed, "failed to read backup operation", err)
	}
	if operation.OperationType != store.OperationTypeSwitch {
		return "", NewError(ErrorRollbackUnsupported, "backup is not from a switch operation").WithDetail("backup_id", backupID)
	}
	if operation.Status != store.OperationStatusApplied {
		return "", NewError(ErrorRollbackUnsupported, "source switch operation is not applied").WithDetail("backup_id", backupID)
	}
	if operation.ProfileID != "" {
		return operation.ProfileID, nil
	}

	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		return "", WrapError(ErrorRollbackUnsupported, "source switch metadata is invalid", err).WithDetail("backup_id", backupID)
	}
	if metadata.ProfileID == "" {
		return "", NewError(ErrorRollbackUnsupported, "source switch metadata is incomplete").WithDetail("backup_id", backupID)
	}
	return metadata.ProfileID, nil
}

func loadRollbackSource(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string, requireActive bool) (rollbackSource, error) {
	operation, err := db.GetOperation(ctx, backupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return rollbackSource{}, NewError(ErrorBackupNotFound, "backup operation not found").WithDetail("backup_id", backupID)
		}
		return rollbackSource{}, WrapError(ErrorStoreStatusFailed, "failed to read backup operation", err)
	}
	if operation.OperationType != store.OperationTypeSwitch {
		return rollbackSource{}, NewError(ErrorRollbackUnsupported, "backup is not from a switch operation").WithDetail("backup_id", backupID)
	}
	if operation.Status != store.OperationStatusApplied {
		return rollbackSource{}, NewError(ErrorRollbackUnsupported, "source switch operation is not applied").WithDetail("backup_id", backupID)
	}

	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		return rollbackSource{}, WrapError(ErrorRollbackUnsupported, "source switch metadata is invalid", err).WithDetail("backup_id", backupID)
	}
	if metadata.PreviousActive == nil {
		return rollbackSource{}, NewError(ErrorRollbackUnsupported, "source switch metadata does not include previous active state").WithDetail("backup_id", backupID)
	}
	if metadata.ProviderID == "" || metadata.ProfileID == "" || metadata.PlanFingerprint == "" {
		return rollbackSource{}, NewError(ErrorRollbackUnsupported, "source switch metadata is incomplete").WithDetail("backup_id", backupID)
	}

	backupPath := filepath.Join(paths.Backups, backupID)
	manifest, err := loadBackupManifest(backupPath)
	if err != nil {
		return rollbackSource{}, err
	}
	if err := validateRollbackManifest(manifest, metadata, operation.ID, backupID, backupPath); err != nil {
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

	if requireActive {
		activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, metadata.ProviderID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return rollbackSource{}, NewError(ErrorTargetChanged, "active state no longer points to source switch").WithDetail("backup_id", backupID)
			}
			return rollbackSource{}, WrapError(ErrorStoreStatusFailed, "failed to read active state", err)
		}
		if activeState.OperationID != operation.ID || activeState.ProfileID != metadata.ProfileID {
			return rollbackSource{}, NewError(ErrorTargetChanged, "active state no longer points to source switch").WithDetail("backup_id", backupID)
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

func loadBackupManifest(backupPath string) (switchBackupManifest, error) {
	raw, err := os.ReadFile(filepath.Join(backupPath, "manifest.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return switchBackupManifest{}, NewError(ErrorBackupNotFound, "backup manifest not found").WithDetail("path", backupPath)
		}
		return switchBackupManifest{}, WrapError(ErrorBackupFailed, "failed to read backup manifest", err).WithDetail("path", backupPath)
	}
	var manifest switchBackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return switchBackupManifest{}, WrapError(ErrorBackupInvalid, "backup manifest is invalid", err).WithDetail("path", backupPath)
	}
	return manifest, nil
}

func validateRollbackManifest(manifest switchBackupManifest, metadata switchOperationMetadata, operationID, backupID, backupPath string) error {
	if manifest.OperationID != backupID || manifest.OperationID != operationID {
		return NewError(ErrorBackupInvalid, "backup manifest operation id does not match source operation").WithDetail("backup_id", backupID)
	}
	if manifest.ProviderID != metadata.ProviderID || manifest.ProfileID != metadata.ProfileID || manifest.PlanFingerprint != metadata.PlanFingerprint {
		return NewError(ErrorBackupInvalid, "backup manifest metadata does not match source operation").WithDetail("backup_id", backupID)
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

func rollbackTargetsFromMetadata(metadata switchOperationMetadata, manifest switchBackupManifest, backupPath string) ([]rollbackTarget, error) {
	return rollbackTargetsFromMetadataWithAdapter(metadata, manifest, backupPath, nil)
}

func rollbackTargetsFromMetadataWithAdapter(metadata switchOperationMetadata, manifest switchBackupManifest, backupPath string, adapter planAdapter) ([]rollbackTarget, error) {
	entries := map[string]switchBackupEntry{}
	for _, entry := range manifest.Entries {
		if entry.TargetID == "" {
			return nil, NewError(ErrorBackupInvalid, "backup manifest entry target id is empty")
		}
		if _, exists := entries[entry.TargetID]; exists {
			return nil, NewError(ErrorBackupInvalid, "backup manifest contains duplicate target").WithDetail("target_id", entry.TargetID)
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
			return nil, NewError(ErrorRollbackUnsupported, "source switch target metadata is incomplete").WithDetail("target_id", target.TargetID)
		}
		if _, exists := seenTargetIDs[target.TargetID]; exists {
			return nil, NewError(ErrorBackupInvalid, "source switch metadata contains duplicate target IDs").WithDetail("target_id", target.TargetID)
		}
		seenTargetIDs[target.TargetID] = struct{}{}
		var spec targetSpec
		var err error
		if backendID == targetBackendFile {
			spec, err = resolveFileTargetSpec(target.TargetID, backendID, target.Path, target.TargetLabel)
		} else if adapter == nil {
			err = NewError(ErrorRollbackUnsupported, "source switch adapter is unavailable").WithDetail("backend_id", backendID)
		} else {
			spec, err = adapter.ResolveTargetSpec(metadata.ProviderID, target.TargetID, backendID, target.Path, target.TargetLabel)
		}
		if err != nil {
			return nil, err
		}
		locatorKey := spec.BackendID() + "\x00" + spec.LocatorFingerprint()
		if firstTargetID, exists := seenLocators[locatorKey]; exists {
			return nil, NewError(ErrorBackupInvalid, "source switch metadata contains duplicate target locators").
				WithDetail("target_id", target.TargetID).WithDetail("first_target_id", firstTargetID)
		}
		seenLocators[locatorKey] = target.TargetID
		rollbackTarget := rollbackTarget{
			TargetID:      target.TargetID,
			BackendID:     backendID,
			TargetLabel:   target.TargetLabel,
			Path:          target.Path,
			Action:        target.Action,
			FileExists:    target.FileExists,
			BeforeSHA256:  target.BeforeSHA256,
			DesiredSHA256: target.DesiredSHA256,
			Spec:          spec,
		}
		switch target.Action {
		case planActionCreate, planActionUpdate:
			entry, ok := entries[target.TargetID]
			if !ok {
				return nil, NewError(ErrorBackupInvalid, "backup manifest is missing target entry").WithDetail("target_id", target.TargetID)
			}
			if err := validateRollbackEntry(target, entry); err != nil {
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
					return nil, NewError(ErrorBackupInvalid, "backup manifest file mode is invalid").WithDetail("target_id", target.TargetID)
				}
				rollbackTarget.Mode = mode
				rollbackTarget.HasMode = true
			}
		case planActionNoop:
		default:
			return nil, NewError(ErrorRollbackUnsupported, "source switch target action is unsupported").WithDetail("target_id", target.TargetID)
		}
		targets = append(targets, rollbackTarget)
	}
	if len(entries) != 0 {
		for targetID := range entries {
			return nil, NewError(ErrorBackupInvalid, "backup manifest contains unknown target entry").WithDetail("target_id", targetID)
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

func rollbackAdapter(ctx context.Context, db *store.Store, metadata switchOperationMetadata) (planAdapter, error) {
	if !switchMetadataNeedsAdapter(metadata) {
		return nil, nil
	}
	provider, err := db.GetProvider(ctx, metadata.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, NewError(ErrorRollbackUnsupported, "source switch Provider is unavailable").WithDetail("provider_id", metadata.ProviderID)
	}
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to read source switch Provider", err)
	}
	adapter, ok := planAdapters[provider.AdapterID]
	if !ok {
		return nil, NewError(ErrorRollbackUnsupported, "source switch adapter is unavailable").WithDetail("adapter_id", provider.AdapterID)
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
			return NewError(ErrorBackupInvalid, "source backup file is missing or invalid").WithDetail("target_id", target.TargetID)
		}
		if state.SHA256 != target.BeforeSHA256 {
			return NewError(ErrorBackupInvalid, "source backup file hash does not match manifest").WithDetail("target_id", target.TargetID)
		}
	}
	return nil
}

func validateRollbackEntry(target switchOperationTargetMetadata, entry switchBackupEntry) error {
	targetBackend := target.BackendID
	if targetBackend == "" {
		targetBackend = targetBackendFile
	}
	entryBackend := entry.BackendID
	if entryBackend == "" {
		entryBackend = targetBackendFile
	}
	if entryBackend != targetBackend || entry.Path != target.Path || entry.Action != target.Action || entry.BeforeSHA256 != target.BeforeSHA256 {
		return NewError(ErrorBackupInvalid, "backup manifest entry does not match source target").WithDetail("target_id", target.TargetID)
	}
	switch target.Action {
	case planActionCreate:
		if entry.Existed || entry.BackupRelPath != "" {
			return NewError(ErrorBackupInvalid, "create backup entry must not contain previous file content").WithDetail("target_id", target.TargetID)
		}
	case planActionUpdate:
		if !entry.Existed || entry.BackupRelPath == "" {
			return NewError(ErrorBackupInvalid, "update backup entry must contain previous file content").WithDetail("target_id", target.TargetID)
		}
	default:
		return NewError(ErrorRollbackUnsupported, "backup entry action is unsupported").WithDetail("target_id", target.TargetID)
	}
	return nil
}

func safeBackupRelPath(backupPath, raw string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(raw))
	if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", NewError(ErrorBackupInvalid, "backup relative path is invalid").WithDetail("path", raw)
	}
	fullPath := filepath.Join(backupPath, rel)
	if !strings.HasPrefix(fullPath, filepath.Clean(backupPath)+string(os.PathSeparator)) {
		return "", NewError(ErrorBackupInvalid, "backup relative path escapes backup directory").WithDetail("path", raw)
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

func verifyRollbackTargets(ctx context.Context, targets []rollbackTarget) error {
	for _, target := range targets {
		if err := verifyRollbackTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func verifyRollbackTarget(ctx context.Context, target rollbackTarget) error {
	backend, ok := targetBackends[target.BackendID]
	if !ok {
		return NewError(ErrorRollbackUnsupported, "target backend is unavailable").WithDetail("backend_id", target.BackendID)
	}
	state, err := backend.Inspect(ctx, target.Spec)
	if err != nil {
		return err
	}
	if target.Action == planActionCreate {
		if !state.Exists {
			return nil
		}
		if state.IsSymlink {
			return NewError(ErrorTargetChanged, "target is not writable").WithDetail("target_id", target.TargetID)
		}
		if state.Fingerprint != target.DesiredSHA256 {
			return NewError(ErrorTargetChanged, "target content changed").WithDetail("target_id", target.TargetID)
		}
		return nil
	}
	if target.Action == planActionUpdate {
		_, err := rollbackUpdateAlreadyRestored(ctx, target)
		return err
	}
	return backend.Verify(ctx, target.Spec, targetSnapshot{Exists: target.FileExists, Fingerprint: target.DesiredSHA256})
}

func rollbackUpdateAlreadyRestored(ctx context.Context, target rollbackTarget) (bool, error) {
	backend, ok := targetBackends[target.BackendID]
	if !ok {
		return false, NewError(ErrorRollbackUnsupported, "target backend is unavailable").WithDetail("backend_id", target.BackendID)
	}
	state, err := backend.Inspect(ctx, target.Spec)
	if err != nil {
		return false, err
	}
	if !state.Exists {
		return false, NewError(ErrorTargetChanged, "target disappeared").WithDetail("target_id", target.TargetID)
	}
	if state.IsSymlink {
		return false, NewError(ErrorTargetChanged, "target is not writable").WithDetail("target_id", target.TargetID)
	}
	if state.Fingerprint == target.DesiredSHA256 {
		return false, nil
	}
	if target.BeforeSHA256 != "" && state.Fingerprint == target.BeforeSHA256 {
		return true, nil
	}
	return false, NewError(ErrorTargetChanged, "target content changed").WithDetail("target_id", target.TargetID)
}

func createRollbackCurrentBackup(ctx context.Context, paths runtime.Paths, operationID string, source rollbackSource) (switchBackup, error) {
	backupPath := filepath.Join(paths.Backups, operationID)
	filesPath := filepath.Join(backupPath, "files")
	secretsPath := filepath.Join(backupPath, "secrets")
	if err := os.MkdirAll(filesPath, 0o700); err != nil {
		return switchBackup{}, WrapError(ErrorBackupFailed, "failed to create rollback backup directory", err).WithDetail("path", backupPath)
	}
	if err := os.MkdirAll(secretsPath, 0o700); err != nil {
		return switchBackup{}, WrapError(ErrorBackupFailed, "failed to create rollback credential backup directory", err)
	}
	chmodBestEffort(backupPath, 0o700)
	chmodBestEffort(filesPath, 0o700)
	chmodBestEffort(secretsPath, 0o700)

	backup := switchBackup{Path: backupPath, Entries: []switchBackupEntry{}}
	for _, target := range source.Targets {
		if target.Action != planActionCreate && target.Action != planActionUpdate {
			continue
		}
		backend, ok := targetBackends[target.BackendID]
		if !ok {
			return switchBackup{}, NewError(ErrorBackupFailed, "target backend is unavailable").WithDetail("backend_id", target.BackendID)
		}
		state, err := backend.Inspect(ctx, target.Spec)
		if err != nil {
			return switchBackup{}, err
		}
		entry := switchBackupEntry{
			TargetID: target.TargetID, BackendID: target.BackendID, TargetLabel: target.TargetLabel,
			Path: target.Path, Action: target.Action, Existed: state.Exists,
		}
		if state.Exists {
			if state.IsSymlink {
				return switchBackup{}, NewError(ErrorBackupFailed, "target is not writable").WithDetail("target_id", target.TargetID)
			}
			entry.BeforeSHA256 = state.Fingerprint
			entry.Mode = fileModeString(state.Mode)
			directory := "files"
			if target.Spec.Sensitive() && target.BackendID != targetBackendFile {
				directory = "secrets"
			}
			relPath := filepath.Join(directory, target.TargetID+".bak")
			copiedSHA, err := backend.Backup(ctx, target.Spec, state, filepath.Join(backupPath, relPath))
			if err != nil {
				return switchBackup{}, err
			}
			if copiedSHA != state.Fingerprint {
				return switchBackup{}, NewError(ErrorBackupFailed, "rollback backup hash does not match current target hash").
					WithDetail("target_id", target.TargetID)
			}
			entry.BackupRelPath = filepath.ToSlash(relPath)
		}
		backup.Entries = append(backup.Entries, entry)
	}

	manifest := switchBackupManifest{
		OperationID:     operationID,
		ProviderID:      source.Metadata.ProviderID,
		ProfileID:       source.Metadata.ProfileID,
		PlanFingerprint: source.Metadata.PlanFingerprint,
		CreatedAtUnixMS: time.Now().UnixMilli(),
		Entries:         backup.Entries,
	}
	if err := writeBackupManifest(backupPath, manifest); err != nil {
		return switchBackup{}, err
	}
	return backup, nil
}

func applyRollbackTargets(ctx context.Context, db *store.Store, operationID, lastMetadata string, metadataBase rollbackOperationMetadata, source rollbackSource) (RollbackCounts, []string, error) {
	var counts RollbackCounts
	processed := []string{}
	for _, target := range source.Targets {
		backend, ok := targetBackends[target.BackendID]
		if !ok {
			return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed,
				NewError(ErrorRollbackUnsupported, "target backend is unavailable").WithDetail("backend_id", target.BackendID))
		}
		switch target.Action {
		case planActionUpdate:
			alreadyRestored, err := rollbackUpdateAlreadyRestored(ctx, target)
			if err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			if alreadyRestored {
				counts.Noop++
				processed = append(processed, target.TargetID)
				break
			}
			backupFile := filepath.Join(source.BackupPath, target.BackupRelPath)
			err = backend.Restore(ctx, target.Spec, targetSnapshot{Exists: true, Fingerprint: target.DesiredSHA256},
				backupFile, target.BeforeSHA256, target.Mode, target.HasMode)
			if err != nil {
				return counts, processed, failRollbackWithProcessed(ctx, db, operationID, lastMetadata, metadataBase, counts, processed, err)
			}
			counts.Restore++
			processed = append(processed, target.TargetID)
		case planActionCreate:
			removed, err := backend.Remove(ctx, target.Spec, targetSnapshot{Exists: true, Fingerprint: target.DesiredSHA256}, true)
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
			counts.Noop++
			processed = append(processed, target.TargetID)
		}
		metadataBase.Counts = counts
		metadataBase.ProcessedTargets = processed
		metadataJSON, err := marshalRollbackOperationMetadata("restoring", metadataBase)
		if err != nil {
			return counts, processed, failRollbackOperation(ctx, db, operationID, lastMetadata, WrapError(ErrorOperationUpdateFailed, "failed to encode rollback operation metadata", err))
		}
		if err := db.UpdateOperationMetadata(ctx, operationID, metadataJSON); err != nil {
			return counts, processed, failRollbackOperation(ctx, db, operationID, metadataJSON, WrapError(ErrorOperationUpdateFailed, "failed to update rollback operation metadata", err))
		}
		lastMetadata = metadataJSON
	}
	return counts, processed, nil
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

func writeBackupManifest(backupPath string, manifest switchBackupManifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return WrapError(ErrorBackupFailed, "failed to encode backup manifest", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(backupPath, "manifest.json"), raw, 0o600); err != nil {
		return WrapError(ErrorBackupFailed, "failed to write backup manifest", err).WithDetail("path", backupPath)
	}
	return nil
}

func inspectBackup(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string) BackupSummary {
	backupPath := filepath.Join(paths.Backups, backupID)
	summary := BackupSummary{
		BackupID: backupID,
		Path:     backupPath,
	}
	manifest, err := loadBackupManifest(backupPath)
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

	reason := rollbackUnsupportedReason(ctx, db, paths, backupID)
	if reason == "" {
		summary.RollbackSupported = true
	} else {
		summary.UnsupportedReason = reason
	}
	return summary
}

func rollbackUnsupportedReason(ctx context.Context, db *store.Store, paths runtime.Paths, backupID string) string {
	_, err := loadRollbackSource(ctx, db, paths, backupID, true)
	if err == nil {
		return ""
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

func backupEntrySummaries(entries []switchBackupEntry) []BackupEntrySummary {
	result := make([]BackupEntrySummary, 0, len(entries))
	for _, entry := range entries {
		summary := BackupEntrySummary{
			TargetID:     entry.TargetID,
			BackendID:    firstNonEmpty(entry.BackendID, targetBackendFile),
			TargetLabel:  entry.TargetLabel,
			Path:         entry.Path,
			Action:       entry.Action,
			Existed:      entry.Existed,
			BeforeSHA256: entry.BeforeSHA256,
			Mode:         entry.Mode,
		}
		if summary.BackendID != targetBackendFile {
			summary.Path = ""
			summary.BeforeSHA256 = ""
			summary.Mode = ""
		}
		result = append(result, summary)
	}
	return result
}
