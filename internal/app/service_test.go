package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusBeforeInitReportsUninitializedWithoutCreatingFiles(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")

	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status before init to succeed, got %v", err)
	}
	if result.Initialized {
		t.Fatalf("expected status before init to report uninitialized")
	}
	if result.SchemaHealthy {
		t.Fatalf("expected schema before init to be unhealthy")
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected status not to create config dir, stat error: %v", err)
	}
}

func TestInitCreatesRuntimeAndDatabase(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	result, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if !result.Initialized || !result.SchemaHealthy {
		t.Fatalf("expected initialized healthy result, got %#v", result)
	}
	if result.MigrationsApplied != 1 {
		t.Fatalf("expected first init to apply one migration, got %d", result.MigrationsApplied)
	}

	for _, path := range []string{
		result.RuntimeRoot,
		filepath.Join(result.RuntimeRoot, "backups"),
		filepath.Join(result.RuntimeRoot, "exports"),
		filepath.Join(result.RuntimeRoot, "logs"),
		filepath.Join(result.RuntimeRoot, "locks"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected runtime path %s to exist, got %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected runtime path %s to be a directory", path)
		}
	}
	if info, err := os.Stat(result.DatabasePath); err != nil {
		t.Fatalf("expected database to exist, got %v", err)
	} else if info.IsDir() {
		t.Fatalf("expected database path to be a file")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected first init to succeed, got %v", err)
	}
	result, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	if result.MigrationsApplied != 0 {
		t.Fatalf("expected second init to apply no migrations, got %d", result.MigrationsApplied)
	}
}

func TestStatusAfterInitReportsHealthy(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status after init to succeed, got %v", err)
	}
	if !result.Initialized || !result.SchemaHealthy {
		t.Fatalf("expected initialized healthy status, got %#v", result)
	}
	if result.PendingOperations != 0 || result.FailedOperations != 0 {
		t.Fatalf("expected no operations, got pending=%d failed=%d", result.PendingOperations, result.FailedOperations)
	}
}

func TestStatusWithMissingSchemaReportsUnhealthy(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "profiledeck", "profiledeck.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("expected database dir setup to succeed, got %v", err)
	}
	if file, err := os.Create(dbPath); err != nil {
		t.Fatalf("expected empty database file setup to succeed, got %v", err)
	} else if err := file.Close(); err != nil {
		t.Fatalf("expected empty database file close to succeed, got %v", err)
	}

	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status to succeed for missing schema, got %v", err)
	}
	if !result.Initialized {
		t.Fatalf("expected existing database file to report initialized")
	}
	if result.SchemaHealthy {
		t.Fatalf("expected missing schema to be unhealthy")
	}
}

func TestStatusWithCorruptDatabaseReturnsStructuredError(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "profiledeck", "profiledeck.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("expected database dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("expected corrupt database setup to succeed, got %v", err)
	}

	_, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err == nil {
		t.Fatalf("expected corrupt database status to fail")
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != ErrorStoreStatusFailed && appErr.Code != ErrorStoreOpenFailed {
		t.Fatalf("expected structured store error, got %s", appErr.Code)
	}
}
