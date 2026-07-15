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
	if err := db.QuickCheck(ctx); err != nil {
		return err
	}
	if !requireCurrentSchema {
		return nil
	}
	status, err := db.Status(ctx)
	if err != nil {
		return err
	}
	if !status.SchemaHealthy {
		return errors.New("application database schema is unhealthy")
	}
	return nil
}

func prepareDatabaseForRestore(ctx context.Context, path string, applyRestoreReset bool) (int, error) {
	db, err := store.Open(ctx, path, false)
	if err != nil {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database could not be opened")
	}
	defer db.Close()
	if err := db.QuickCheck(ctx); err != nil {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database is damaged")
	}
	if err := db.CheckMigrationCompatibility(ctx); err != nil {
		if errors.Is(err, store.ErrFutureSchema) {
			return 0, apperror.New(apperror.BackupInvalid, "application backup was created by a newer ProfileDeck version")
		}
		return 0, apperror.New(apperror.BackupInvalid, "application backup database schema could not be inspected")
	}
	migration, err := db.Migrate(ctx)
	if err != nil {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database could not be upgraded")
	}
	if err := db.QuickCheck(ctx); err != nil {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database is damaged")
	}
	status, err := db.Status(ctx)
	if err != nil || !status.SchemaHealthy {
		return 0, apperror.New(apperror.BackupInvalid, "application backup database schema is invalid")
	}
	if applyRestoreReset {
		if err := db.PrepareForApplicationRestore(ctx); err != nil {
			return 0, apperror.New(apperror.RestoreFailed, "restored application state could not be prepared")
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

func currentDatabaseHealthy(ctx context.Context, stores store.Factory) bool {
	db, err := stores.OpenHealthy(ctx, true)
	if err != nil {
		return false
	}
	defer db.Close()
	return db.QuickCheck(ctx) == nil
}
