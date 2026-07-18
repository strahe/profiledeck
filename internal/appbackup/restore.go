package appbackup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

type RestoreSource struct {
	BackupID string `json:"backup_id,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

type RestorePreview struct {
	Backup                 BackupDetail `json:"backup"`
	Fingerprint            string       `json:"fingerprint"`
	CurrentDatabaseHealthy bool         `json:"current_database_healthy"`
	SchemaUpgradeRequired  bool         `json:"schema_upgrade_required"`
}

type RestoreRequest struct {
	Source              RestoreSource `json:"source"`
	ExpectedFingerprint string        `json:"expected_fingerprint"`
	Confirm             bool          `json:"confirm"`
}

type RestoreResult struct {
	Backup                   BackupDetail   `json:"backup"`
	SafetyBackup             *BackupSummary `json:"safety_backup,omitempty"`
	SafetyBackupSkipped      bool           `json:"safety_backup_skipped"`
	RecoveryCleanupCompleted bool           `json:"recovery_cleanup_completed"`
	RestartRequired          bool           `json:"restart_required"`
}

func (service *Service) PreviewRestore(ctx context.Context, source RestoreSource) (RestorePreview, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-preview-restore")
	if err != nil {
		return RestorePreview{}, err
	}
	defer lock.Release()
	identity, err := service.loadIdentity()
	if err != nil {
		return RestorePreview{}, err
	}
	path, expectedID, err := service.resolveRestoreSource(source)
	if err != nil {
		return RestorePreview{}, err
	}
	temporary, err := os.CreateTemp(service.paths.Root, ".profiledeck-restore-preview-*.db")
	if err != nil {
		return RestorePreview{}, apperror.New(apperror.RestoreFailed, "application backup could not be prepared for preview")
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			_ = temporary.Close()
		}
		removeDatabaseFiles(temporaryPath)
	}()
	closeErr := temporary.Close()
	temporary = nil
	if closeErr != nil {
		return RestorePreview{}, apperror.New(apperror.RestoreFailed, "application backup could not be prepared for preview")
	}
	if err := os.Remove(temporaryPath); err != nil {
		return RestorePreview{}, apperror.New(apperror.RestoreFailed, "application backup could not be prepared for preview")
	}

	inspected, err := inspectArchive(ctx, path, identity, temporaryPath)
	if err != nil {
		return RestorePreview{}, err
	}
	if expectedID != "" && inspected.Manifest.BackupID != expectedID {
		return RestorePreview{}, apperror.New(apperror.BackupInvalid, "application backup id does not match its file name")
	}
	applied, err := prepareDatabaseForRestore(ctx, temporaryPath, false)
	if err != nil {
		return RestorePreview{}, err
	}
	return RestorePreview{
		Backup:                 detailFromManifest(inspected.Manifest, inspected.SizeBytes),
		Fingerprint:            inspected.Fingerprint,
		CurrentDatabaseHealthy: currentDatabaseHealthy(ctx, service.stores),
		SchemaUpgradeRequired:  applied > 0,
	}, nil
}

func (service *Service) Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error) {
	if !req.Confirm {
		return RestoreResult{}, apperror.New(apperror.ConfirmationRequired, "restoring application data requires confirmation")
	}
	fingerprint := strings.TrimSpace(req.ExpectedFingerprint)
	if fingerprint == "" {
		return RestoreResult{}, apperror.New(apperror.ConfirmationRequired, "application backup preview is required before restore")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-restore")
	if err != nil {
		return RestoreResult{}, err
	}
	defer lock.Release()
	identity, err := service.loadIdentity()
	if err != nil {
		return RestoreResult{}, err
	}
	path, expectedID, err := service.resolveRestoreSource(req.Source)
	if err != nil {
		return RestoreResult{}, err
	}

	var result RestoreResult
	err = service.exclusive.RunExclusive(ctx, "application-restore", func(ctx context.Context) error {
		if err := store.ReconcileDatabaseSwap(ctx, service.paths.Database); err != nil {
			return apperror.New(apperror.RestoreFailed, "an interrupted application restore could not be resolved")
		}
		candidate := store.RestoreCandidatePath(service.paths.Database)
		removeDatabaseFiles(candidate)
		defer removeDatabaseFiles(candidate)
		inspected, err := inspectArchive(ctx, path, identity, candidate)
		if err != nil {
			return err
		}
		if expectedID != "" && inspected.Manifest.BackupID != expectedID {
			return apperror.New(apperror.BackupInvalid, "application backup id does not match its file name")
		}
		if inspected.Fingerprint != fingerprint {
			return apperror.New(apperror.BackupInvalid, "application backup changed after preview")
		}
		if _, err := prepareDatabaseForRestore(ctx, candidate, true); err != nil {
			return err
		}

		if currentDatabaseHealthy(ctx, service.stores) {
			safety, err := service.create(ctx, CreateRequest{Kind: KindAutomatic, Reason: ReasonBeforeRestore})
			if err != nil {
				return apperror.New(apperror.RestoreFailed, "the safety backup required before restore could not be created")
			}
			result.SafetyBackup = &safety.BackupSummary
		} else {
			result.SafetyBackupSkipped = true
		}
		if err := store.ReplaceDatabase(ctx, service.paths.Database); err != nil {
			return apperror.New(apperror.RestoreFailed, "application data could not be restored; the previous database was kept")
		}
		result.RecoveryCleanupCompleted = resetRecoveryDirectory(service.paths.Recovery) == nil
		result.Backup = detailFromManifest(inspected.Manifest, inspected.SizeBytes)
		result.RestartRequired = true
		return nil
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func (service *Service) resolveRestoreSource(source RestoreSource) (string, string, error) {
	backupID := strings.TrimSpace(source.BackupID)
	filePath := strings.TrimSpace(source.FilePath)
	if (backupID == "") == (filePath == "") {
		return "", "", apperror.New(apperror.BackupInvalid, "choose one application backup to restore")
	}
	if backupID != "" {
		id, err := normalizeBackupID(backupID)
		if err != nil {
			return "", "", err
		}
		return filepath.Join(service.paths.Backups, id+Extension), id, nil
	}
	path := filepath.Clean(filePath)
	if path == "." || path == "" {
		return "", "", apperror.New(apperror.BackupInvalid, "application backup file is required")
	}
	return path, "", nil
}

func resetRecoveryDirectory(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o700)
}

func removeDatabaseFiles(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

func (service *Service) CreateAutomaticIfDue(ctx context.Context) (*BackupDetail, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	lock, err := service.acquireOperationLock("application-backup-create-automatic")
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	list, err := service.list(ctx)
	if err != nil {
		return nil, err
	}
	if list.AutomaticCleanupRequired {
		service.cleanupAutomaticBackups(ctx)
	}
	cutoff := service.now().Add(-24 * time.Hour).UnixMilli()
	for _, backup := range list.Backups {
		if backup.Kind == KindAutomatic && backup.CreatedAtUnixMS >= cutoff {
			return nil, nil
		}
	}
	created, err := service.create(ctx, CreateRequest{Kind: KindAutomatic, Reason: ReasonScheduled})
	if err != nil {
		return nil, err
	}
	return &created, nil
}
