package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

type InitRequest struct {
	ConfigDir string
}

type InitResult struct {
	ConfigDir         string `json:"config_dir"`
	RuntimeRoot       string `json:"runtime_root"`
	DatabasePath      string `json:"database_path"`
	Initialized       bool   `json:"initialized"`
	SchemaHealthy     bool   `json:"schema_healthy"`
	MigrationsApplied int    `json:"migrations_applied"`
}

type StatusRequest struct {
	ConfigDir string
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

func Init(ctx context.Context, req InitRequest) (InitResult, error) {
	configDir, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return InitResult{}, err
	}

	if err := createRuntimeDirs(paths); err != nil {
		return InitResult{}, WrapError(ErrorRuntimeInitFailed, "failed to initialize runtime directories", err)
	}

	db, err := store.Open(ctx, paths.Database, false)
	if err != nil {
		return InitResult{}, WrapError(ErrorStoreOpenFailed, "failed to open application database", err)
	}
	defer db.Close()

	migrationResult, err := db.Migrate(ctx)
	if err != nil {
		return InitResult{}, WrapError(ErrorStoreMigrationFailed, "failed to run database migrations", err)
	}
	chmodBestEffort(paths.Database, 0o600)

	status, err := db.Status(ctx)
	if err != nil {
		return InitResult{}, WrapError(ErrorStoreStatusFailed, "failed to inspect application database", err)
	}
	if !status.SchemaHealthy {
		return InitResult{}, NewError(ErrorStoreSchemaInvalid, "application database schema is not healthy")
	}

	return InitResult{
		ConfigDir:         configDir,
		RuntimeRoot:       paths.Root,
		DatabasePath:      paths.Database,
		Initialized:       true,
		SchemaHealthy:     true,
		MigrationsApplied: migrationResult.Applied,
	}, nil
}

func Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	configDir, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return StatusResult{}, err
	}

	result := StatusResult{
		ConfigDir:     configDir,
		RuntimeRoot:   paths.Root,
		DatabasePath:  paths.Database,
		Initialized:   false,
		SchemaHealthy: false,
	}

	if _, err := os.Stat(paths.Database); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return StatusResult{}, WrapError(ErrorStoreStatusFailed, "failed to inspect application database", err)
	}

	db, err := store.Open(ctx, paths.Database, true)
	if err != nil {
		return StatusResult{}, WrapError(ErrorStoreOpenFailed, "failed to open application database", err)
	}
	defer db.Close()

	status, err := db.Status(ctx)
	if err != nil {
		return StatusResult{}, WrapError(ErrorStoreStatusFailed, "failed to inspect application database", err)
	}

	result.Initialized = true
	result.SchemaHealthy = status.SchemaHealthy
	result.PendingOperations = status.PendingOperations
	result.FailedOperations = status.FailedOperations
	return result, nil
}

func resolveRuntime(configDir string) (string, runtime.Paths, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		defaultDir, err := runtime.DefaultUserConfigDir()
		if err != nil {
			return "", runtime.Paths{}, WrapError(ErrorInvalidRuntimePath, "failed to resolve user config directory", err)
		}
		configDir = defaultDir
	}

	paths, err := runtime.ResolvePaths(configDir)
	if err != nil {
		if errors.Is(err, runtime.ErrEmptyUserConfigDir) {
			return "", runtime.Paths{}, NewError(ErrorInvalidRuntimePath, "user config directory is required")
		}
		return "", runtime.Paths{}, WrapError(ErrorInvalidRuntimePath, "failed to resolve runtime paths", err)
	}
	return configDir, paths, nil
}

func createRuntimeDirs(paths runtime.Paths) error {
	dirs := []string{
		paths.Root,
		paths.Backups,
		paths.Exports,
		paths.Logs,
		filepath.Dir(paths.Lock),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		chmodBestEffort(dir, 0o700)
	}
	return nil
}

func chmodBestEffort(path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		log.Printf("profiledeck: failed to chmod %s to %s: %v", path, fileModeString(mode), err)
	}
}
