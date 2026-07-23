package runtime

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

type InitResult struct {
	ConfigDir                        string `json:"config_dir"`
	RuntimeRoot                      string `json:"runtime_root"`
	DatabasePath                     string `json:"database_path"`
	Initialized                      bool   `json:"initialized"`
	SchemaHealthy                    bool   `json:"schema_healthy"`
	MigrationsApplied                int    `json:"migrations_applied"`
	OperationRecoveryCleanupRequired bool   `json:"operation_recovery_cleanup_required"`
}

type StatusResult struct {
	ConfigDir                        string `json:"config_dir"`
	RuntimeRoot                      string `json:"runtime_root"`
	DatabasePath                     string `json:"database_path"`
	Initialized                      bool   `json:"initialized"`
	SchemaHealthy                    bool   `json:"schema_healthy"`
	PendingOperations                int    `json:"pending_operations"`
	FailedOperations                 int    `json:"failed_operations"`
	OperationRecoveryCleanupRequired bool   `json:"operation_recovery_cleanup_required"`
}

type RecoveryCleanupInspector interface {
	CleanupRequired(context.Context, *store.Store) (bool, error)
}

type Service struct {
	configDir       string
	paths           Paths
	stores          store.Factory
	dataLease       *DataLease
	recoveryCleanup RecoveryCleanupInspector
}

func (service *Service) AttachDataLease(lease *DataLease) {
	service.dataLease = lease
}

func (service *Service) AttachRecoveryCleanup(inspector RecoveryCleanupInspector) {
	service.recoveryCleanup = inspector
}

func NewService(configDir string) (*Service, error) {
	resolvedConfigDir, paths, err := ResolveConfig(configDir)
	if err != nil {
		return nil, err
	}
	return &Service{configDir: resolvedConfigDir, paths: paths, stores: store.NewFactory(paths.Database)}, nil
}

func ResolveConfig(configDir string) (string, Paths, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		defaultDir, err := DefaultUserConfigDir()
		if err != nil {
			return "", Paths{}, apperror.Wrap(apperror.InvalidRuntimePath, "failed to resolve user config directory", err)
		}
		configDir = defaultDir
	}
	paths, err := ResolvePaths(configDir)
	if err != nil {
		if errors.Is(err, ErrEmptyUserConfigDir) {
			return "", Paths{}, apperror.New(apperror.InvalidRuntimePath, "user config directory is required")
		}
		return "", Paths{}, apperror.Wrap(apperror.InvalidRuntimePath, "failed to resolve runtime paths", err)
	}
	return configDir, paths, nil
}

func (service *Service) ConfigDir() string {
	return service.configDir
}

func (service *Service) Paths() Paths {
	return service.paths
}

func (service *Service) StoreFactory() store.Factory {
	return service.stores
}

func (service *Service) EnsureDirectories() error {
	if err := createDirs(service.paths); err != nil {
		return apperror.Wrap(apperror.RuntimeInitFailed, "failed to initialize runtime directories", err)
	}
	return nil
}

func (service *Service) SecureDatabaseBestEffort() {
	chmodBestEffort(service.paths.Database, 0o600)
}

func (service *Service) Status(ctx context.Context) (StatusResult, error) {
	result := StatusResult{
		ConfigDir: service.configDir, RuntimeRoot: service.paths.Root, DatabasePath: service.paths.Database,
	}
	if _, err := os.Stat(service.paths.Database); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return StatusResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	lease, closeLease, err := service.leaseForOperation()
	if err != nil {
		return StatusResult{}, err
	}
	if closeLease {
		defer lease.Close()
	}
	db, err := service.stores.Open(ctx, true)
	if err != nil {
		return StatusResult{}, err
	}
	defer db.Close()
	status, err := db.Status(ctx)
	if err != nil {
		if errors.Is(err, store.ErrUnsupportedSchema) {
			return StatusResult{}, unsupportedSchemaError()
		}
		if errors.Is(err, store.ErrInvalidMigrationHistory) {
			return StatusResult{}, apperror.New(apperror.StoreSchemaInvalid, "ProfileDeck local data is not in a valid state; run profiledeck doctor or restore a known-good application backup")
		}
		return StatusResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	result.Initialized = true
	result.SchemaHealthy = status.SchemaHealthy
	result.PendingOperations = status.PendingOperations
	result.FailedOperations = status.FailedOperations
	if status.SchemaHealthy && service.recoveryCleanup != nil {
		required, err := service.recoveryCleanup.CleanupRequired(ctx, db)
		if err != nil {
			if errors.Is(err, store.ErrInvalidSystemState) {
				return StatusResult{}, apperror.New(
					apperror.StoreSchemaInvalid,
					"ProfileDeck local data is not in a valid state; run profiledeck doctor or restore a known-good application backup",
				)
			}
			return StatusResult{}, apperror.New(apperror.StoreStatusFailed, "application recovery state could not be inspected")
		}
		result.OperationRecoveryCleanupRequired = required
	}
	return result, nil
}

func unsupportedSchemaError() *apperror.Error {
	return apperror.New(apperror.StoreSchemaUnsupported, apperror.StoreSchemaUnsupportedMessage)
}

func (service *Service) leaseForOperation() (*DataLease, bool, error) {
	if service.dataLease != nil {
		return service.dataLease, false, nil
	}
	lease, err := AcquireDataLease(service.paths.DataLock, service.stores.AccessGate())
	if err != nil {
		return nil, false, err
	}
	return lease, true, nil
}

func createDirs(paths Paths) error {
	// Recovery cleanup owns the final recovery directory so initialization never
	// follows a substituted symlink before the cleanup safety check runs.
	for _, dir := range []string{paths.Root, paths.Backups, paths.Exports, paths.Logs, filepath.Dir(paths.Lock)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		chmodBestEffort(dir, 0o700)
	}
	return nil
}

func chmodBestEffort(path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		log.Print("profiledeck: private permissions could not be applied; run profiledeck doctor")
	}
}
