package appbackup

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func validateDatabase(ctx context.Context, path string, requireCurrentSchema bool) error {
	db, err := store.Open(ctx, path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	scope := store.IntegrityAppliedBaseline
	if requireCurrentSchema {
		scope = store.IntegrityCurrentBaseline
	}
	report, err := db.InspectIntegrity(ctx, scope)
	if err != nil {
		return err
	}
	if !report.Healthy {
		return errors.New("application database integrity validation failed")
	}
	return nil
}

func prepareDatabaseForRestore(ctx context.Context, path string, applyRestoreReset bool) (int, error) {
	db, err := store.Open(ctx, path, false)
	if err != nil {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database could not be opened")
	}
	defer db.Close()
	baseline, err := db.InspectIntegrity(ctx, store.IntegrityAppliedBaseline)
	if err != nil {
		if errors.Is(err, store.ErrUnsupportedSchema) {
			return 0, unsupportedBackupSchemaError()
		}
		return 0, apperror.New(apperror.BackupInvalid, "application backup database could not be inspected")
	}
	if !baseline.Healthy {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database is invalid")
	}
	migration, err := db.Migrate(ctx)
	if err != nil {
		if errors.Is(err, store.ErrUnsupportedSchema) {
			return 0, unsupportedBackupSchemaError()
		}
		return 0, apperror.New(apperror.BackupInvalid, "application backup database could not be upgraded")
	}
	current, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
	if err != nil || !current.Healthy {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database is invalid")
	}
	if applyRestoreReset {
		if err := db.PrepareForApplicationRestore(ctx); err != nil {
			return 0, apperror.New(apperror.RestoreFailed, "restored application state could not be prepared")
		}
		final, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
		if err != nil || !final.Healthy {
			return 0, apperror.New(apperror.BackupInvalid, "application backup database is invalid")
		}
	}
	if err := db.Checkpoint(ctx); err != nil {
		return 0, apperror.New(apperror.RestoreFailed, "restored application database could not be finalized")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return 0, apperror.New(apperror.RestoreFailed, "restored application database could not be secured")
	}
	return migration.Applied, nil
}

func unsupportedBackupSchemaError() *apperror.Error {
	return apperror.New(apperror.BackupSchemaUnsupported, apperror.BackupSchemaUnsupportedMessage)
}

func currentDatabaseHealthy(ctx context.Context, stores store.Factory) bool {
	db, err := stores.Open(ctx, true)
	if err != nil {
		return false
	}
	defer db.Close()
	report, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
	return err == nil && report.Healthy
}
