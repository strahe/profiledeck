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
	ConfigDir         string `json:"config_dir"`
	RuntimeRoot       string `json:"runtime_root"`
	DatabasePath      string `json:"database_path"`
	Initialized       bool   `json:"initialized"`
	SchemaHealthy     bool   `json:"schema_healthy"`
	MigrationsApplied int    `json:"migrations_applied"`
}

type StatusResult struct {
	ConfigDir         string `json:"config_dir"`
	RuntimeRoot       string `json:"runtime_root"`
	DatabasePath      string `json:"database_path"`
	Initialized       bool   `json:"initialized"`
	SchemaHealthy     bool   `json:"schema_healthy"`
	PendingOperations int    `json:"pending_operations"`
	FailedOperations  int    `json:"failed_operations"`
}

type Service struct {
	configDir string
	paths     Paths
	stores    store.Factory
	dataLease *DataLease
}

func (service *Service) AttachDataLease(lease *DataLease) {
	service.dataLease = lease
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

func (service *Service) Init(ctx context.Context) (InitResult, error) {
	if err := createDirs(service.paths); err != nil {
		return InitResult{}, apperror.Wrap(apperror.RuntimeInitFailed, "failed to initialize runtime directories", err)
	}
	lease, closeLease, err := service.leaseForOperation()
	if err != nil {
		return InitResult{}, err
	}
	if closeLease {
		defer lease.Close()
	}
	if store.DatabaseSwapPending(service.paths.Database) {
		if err := lease.RunExclusive(ctx, "startup-restore-reconcile", func(ctx context.Context) error {
			return store.ReconcileDatabaseSwap(ctx, service.paths.Database)
		}); err != nil {
			return InitResult{}, apperror.Wrap(apperror.RestoreFailed, "failed to resolve an interrupted application restore", err)
		}
	}
	db, err := service.stores.Open(ctx, false)
	if err != nil {
		return InitResult{}, err
	}
	defer db.Close()
	migrationResult, err := db.Migrate(ctx)
	if err != nil {
		return InitResult{}, apperror.Wrap(apperror.StoreMigrationFailed, "failed to run database migrations", err)
	}
	chmodBestEffort(service.paths.Database, 0o600)
	status, err := db.Status(ctx)
	if err != nil {
		return InitResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	if !status.SchemaHealthy {
		return InitResult{}, apperror.New(apperror.StoreSchemaInvalid, "application database schema is not healthy")
	}
	return InitResult{
		ConfigDir: service.configDir, RuntimeRoot: service.paths.Root, DatabasePath: service.paths.Database,
		Initialized: true, SchemaHealthy: true, MigrationsApplied: migrationResult.Applied,
	}, nil
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
		return StatusResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	result.Initialized = true
	result.SchemaHealthy = status.SchemaHealthy
	result.PendingOperations = status.PendingOperations
	result.FailedOperations = status.FailedOperations
	return result, nil
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
	for _, dir := range []string{paths.Root, paths.Backups, paths.Recovery, paths.Exports, paths.Logs, filepath.Dir(paths.Lock)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		chmodBestEffort(dir, 0o700)
	}
	return nil
}

func chmodBestEffort(path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		log.Printf("profiledeck: failed to chmod %s to %s: %v", path, mode, err)
	}
}
