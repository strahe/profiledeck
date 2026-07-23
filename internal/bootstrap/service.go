// Package bootstrap owns initialization and live application database upgrades.
package bootstrap

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/recoverycleanup"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type BackupCreator interface {
	Create(context.Context, appbackup.CreateRequest) (appbackup.BackupDetail, error)
}

type Service struct {
	runtime *runtime.Service
	backups BackupCreator
	lease   *runtime.DataLease
	cleanup *recoverycleanup.Service
	locks   SharedLockRunner
}

type SharedLockRunner interface {
	RunWithSharedLock(context.Context, string, func(context.Context) error) error
}

type RecoveryCleanupCoordinator struct {
	Cleanup *recoverycleanup.Service
	Locks   SharedLockRunner
}

func NewService(runtimeService *runtime.Service, backups BackupCreator, lease *runtime.DataLease, coordinators ...RecoveryCleanupCoordinator) *Service {
	service := &Service{runtime: runtimeService, backups: backups, lease: lease}
	if runtimeService != nil {
		service.cleanup = recoverycleanup.NewService(runtimeService.Paths())
		service.locks = directLockRunner{path: runtimeService.Paths().Lock}
	}
	if len(coordinators) > 0 {
		if coordinators[0].Cleanup != nil {
			service.cleanup = coordinators[0].Cleanup
		}
		if coordinators[0].Locks != nil {
			service.locks = coordinators[0].Locks
		}
	}
	return service
}

func (service *Service) Initialize(ctx context.Context) (runtime.InitResult, error) {
	if service == nil || service.runtime == nil {
		return runtime.InitResult{}, apperror.New(apperror.RuntimeInitFailed, "application initialization is unavailable")
	}
	if err := service.runtime.EnsureDirectories(); err != nil {
		return runtime.InitResult{}, err
	}
	paths := service.runtime.Paths()
	stores := service.runtime.StoreFactory()
	lease := service.lease
	if lease == nil {
		var err error
		lease, err = runtime.AcquireDataLease(paths.DataLock, stores.AccessGate())
		if err != nil {
			return runtime.InitResult{}, err
		}
		defer lease.Close()
	}

	exists, err := databaseExists(paths.Database)
	if err != nil {
		return runtime.InitResult{}, databaseInspectionError(err)
	}
	if exists && !store.DatabaseSwapPending(paths.Database) {
		state, err := inspectDatabase(ctx, stores)
		if err != nil {
			return runtime.InitResult{}, databaseInspectionError(err)
		}
		if state.Current {
			return service.finishInitialization(ctx, 0)
		}
	}

	backups := service.backups
	if backups == nil {
		backups = appbackup.NewService(paths, stores, lease)
	}

	var migrationResult store.MigrationResult
	// Live upgrades and interrupted database exchanges require the exclusive
	// data lease so no entrypoint can observe a partially advanced baseline.
	err = lease.RunExclusive(ctx, "database-bootstrap", func(ctx context.Context) error {
		if err := store.ReconcileDatabaseSwap(ctx, paths.Database); err != nil {
			return apperror.New(apperror.RestoreFailed, "an interrupted application restore could not be resolved")
		}
		existing, err := databaseExists(paths.Database)
		if err != nil {
			return databaseInspectionError(err)
		}
		if existing {
			state, err := inspectDatabase(ctx, stores)
			if err != nil {
				return databaseInspectionError(err)
			}
			if state.Current {
				return nil
			}
			// No migration API may run before this encrypted snapshot succeeds;
			// even Bun's migration-table initialization is a schema write.
			if _, err := backups.Create(ctx, appbackup.CreateRequest{
				Kind: appbackup.KindAutomatic, Reason: appbackup.ReasonBeforeMigration,
			}); err != nil {
				return apperror.New(apperror.StoreMigrationFailed, "the encrypted backup required before updating local data could not be created")
			}
		}

		db, err := stores.Open(ctx, false)
		if err != nil {
			return apperror.New(apperror.StoreOpenFailed, "application database could not be opened for initialization")
		}
		defer db.Close()
		migrationResult, err = db.Migrate(ctx)
		if err != nil {
			if errors.Is(err, store.ErrUnsupportedSchema) {
				return unsupportedSchemaError()
			}
			if errors.Is(err, store.ErrInvalidMigrationHistory) {
				return invalidSchemaError()
			}
			return apperror.New(apperror.StoreMigrationFailed, "local data could not be updated; the protected backup was kept")
		}
		// Migration callbacks commit before Bun records their markers. Accept only
		// the fully current integrity baseline before allowing startup to continue.
		report, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
		if err != nil || !report.Healthy {
			return invalidSchemaError()
		}
		return nil
	})
	if err != nil {
		return runtime.InitResult{}, err
	}
	return service.finishInitialization(ctx, migrationResult.Applied)
}

func (service *Service) finishInitialization(ctx context.Context, applied int) (runtime.InitResult, error) {
	service.runtime.SecureDatabaseBestEffort()
	cleanupRequired := false
	if service.cleanup != nil && service.locks != nil {
		db, err := service.runtime.StoreFactory().OpenHealthy(ctx, false)
		if err != nil {
			return runtime.InitResult{}, err
		}
		defer db.Close()
		inspection, err := service.cleanup.Inspect(ctx, db)
		if err != nil {
			if errors.Is(err, store.ErrInvalidSystemState) {
				return runtime.InitResult{}, invalidSchemaError()
			}
			return runtime.InitResult{}, apperror.New(apperror.StoreStatusFailed, "application recovery state could not be inspected")
		}
		cleanupRequired = inspection.CleanupRequired()
		if cleanupRequired {
			err = service.locks.RunWithSharedLock(ctx, "startup-recovery-cleanup", func(ctx context.Context) error {
				_, cleanupErr := service.cleanup.ReconcileLocked(ctx, db)
				return cleanupErr
			})
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return runtime.InitResult{}, err
			}
			var appErr *apperror.Error
			if errors.As(err, &appErr) && appErr.Code == apperror.StoreSchemaInvalid {
				return runtime.InitResult{}, invalidSchemaError()
			}
			cleanupRequired = err != nil
		}
	}
	return service.result(applied, cleanupRequired), nil
}

func (service *Service) result(applied int, cleanupRequired bool) runtime.InitResult {
	paths := service.runtime.Paths()
	return runtime.InitResult{
		ConfigDir: service.runtime.ConfigDir(), RuntimeRoot: paths.Root, DatabasePath: paths.Database,
		Initialized: true, SchemaHealthy: true, MigrationsApplied: applied,
		OperationRecoveryCleanupRequired: cleanupRequired,
	}
}

type directLockRunner struct {
	path string
}

func (runner directLockRunner) RunWithSharedLock(ctx context.Context, owner string, run func(context.Context) error) error {
	lock, err := targetfs.AcquireLock(runner.path, owner)
	if err != nil {
		return apperror.New(apperror.LockAcquireFailed, "another ProfileDeck operation is in progress")
	}
	defer lock.ReleaseAndRemoveBestEffort()
	return run(ctx)
}

func inspectDatabase(ctx context.Context, stores store.Factory) (store.MigrationState, error) {
	db, err := stores.Open(ctx, true)
	if err != nil {
		return store.MigrationState{}, err
	}
	defer db.Close()
	if err := db.CheckMigrationCompatibility(ctx); err != nil {
		return store.MigrationState{}, err
	}
	report, err := db.InspectIntegrity(ctx, store.IntegrityAppliedBaseline)
	if err != nil {
		return store.MigrationState{}, err
	}
	if !report.Healthy {
		return store.MigrationState{}, errIntegrityInvalid
	}
	if report.Migration.Current {
		report, err = db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
		if err != nil {
			return store.MigrationState{}, err
		}
		if !report.Healthy {
			return store.MigrationState{}, errIntegrityInvalid
		}
	}
	return report.Migration, nil
}

func databaseExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("application database is not a regular file")
	}
	return true, nil
}

var errIntegrityInvalid = errors.New("application database integrity is invalid")

func databaseInspectionError(err error) error {
	switch {
	case errors.Is(err, store.ErrUnsupportedSchema):
		return unsupportedSchemaError()
	case errors.Is(err, store.ErrInvalidMigrationHistory), errors.Is(err, errIntegrityInvalid):
		return invalidSchemaError()
	default:
		return apperror.New(apperror.StoreStatusFailed, "application database could not be inspected")
	}
}

func unsupportedSchemaError() *apperror.Error {
	return apperror.New(apperror.StoreSchemaUnsupported, apperror.StoreSchemaUnsupportedMessage)
}

func invalidSchemaError() *apperror.Error {
	return apperror.New(apperror.StoreSchemaInvalid, "ProfileDeck local data could not be verified; restore a known-good application backup and try again")
}
