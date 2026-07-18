package runtime

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
)

func TestStatusBeforeInitDoesNotCreateRuntime(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")
	service, err := NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	result, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("status before init: %v", err)
	}
	if result.Initialized || result.SchemaHealthy {
		t.Fatalf("unexpected pre-init status: %#v", result)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created runtime directory: %v", err)
	}
}

func TestInitCreatesRuntimeAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	first, err := service.Init(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if !first.Initialized || !first.SchemaHealthy || first.MigrationsApplied != 3 {
		t.Fatalf("unexpected first init result: %#v", first)
	}
	for _, path := range []string{
		first.RuntimeRoot,
		filepath.Join(first.RuntimeRoot, "backups"),
		filepath.Join(first.RuntimeRoot, "recovery"),
		filepath.Join(first.RuntimeRoot, "exports"),
		filepath.Join(first.RuntimeRoot, "logs"),
		filepath.Join(first.RuntimeRoot, "locks"),
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("runtime directory %s is invalid: info=%#v err=%v", path, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(first.RuntimeRoot, "updates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy update backup directory should not exist: %v", err)
	}
	if info, err := os.Stat(first.DatabasePath); err != nil || info.IsDir() {
		t.Fatalf("runtime database is invalid: info=%#v err=%v", info, err)
	}

	second, err := service.Init(ctx)
	if err != nil {
		t.Fatalf("initialize runtime again: %v", err)
	}
	if second.MigrationsApplied != 0 {
		t.Fatalf("second init applied migrations: %#v", second)
	}
	status, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("status after init: %v", err)
	}
	if !status.Initialized || !status.SchemaHealthy || status.PendingOperations != 0 || status.FailedOperations != 0 {
		t.Fatalf("unexpected initialized status: %#v", status)
	}
}

func TestStatusHandlesMissingAndCorruptSchema(t *testing.T) {
	ctx := context.Background()

	t.Run("missing schema", func(t *testing.T) {
		service, err := NewService(t.TempDir())
		if err != nil {
			t.Fatalf("create runtime service: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(service.Paths().Database), 0o700); err != nil {
			t.Fatalf("create database directory: %v", err)
		}
		file, err := os.Create(service.Paths().Database)
		if err != nil {
			t.Fatalf("create empty database: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close empty database: %v", err)
		}
		result, err := service.Status(ctx)
		if err != nil {
			t.Fatalf("status missing schema: %v", err)
		}
		if !result.Initialized || result.SchemaHealthy {
			t.Fatalf("unexpected missing-schema status: %#v", result)
		}
	})

	t.Run("corrupt database", func(t *testing.T) {
		service, err := NewService(t.TempDir())
		if err != nil {
			t.Fatalf("create runtime service: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(service.Paths().Database), 0o700); err != nil {
			t.Fatalf("create database directory: %v", err)
		}
		if err := os.WriteFile(service.Paths().Database, []byte("not a sqlite database"), 0o600); err != nil {
			t.Fatalf("write corrupt database: %v", err)
		}
		_, err = service.Status(ctx)
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || (appErr.Code != apperror.StoreStatusFailed && appErr.Code != apperror.StoreOpenFailed) {
			t.Fatalf("unexpected corrupt database error: %v", err)
		}
	})
}

func TestInitAndStatusRejectUnsupportedSchema(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	initialized, err := service.Init(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	sqlDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open runtime database: %v", err)
	}
	unknownName := "209912310001"
	_, insertErr := sqlDB.ExecContext(ctx, `
		INSERT INTO bun_migrations (name, group_id, migrated_at)
		VALUES (?, 99, CURRENT_TIMESTAMP)
	`, unknownName)
	closeErr := sqlDB.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatalf("insert unsupported migration: %v", err)
	}

	_, initErr := service.Init(ctx)
	assertUnsupportedSchemaError(t, initErr, unknownName)
	_, statusErr := service.Status(ctx)
	assertUnsupportedSchemaError(t, statusErr, unknownName)
}

func assertUnsupportedSchemaError(t *testing.T, err error, sensitiveValue string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.StoreSchemaUnsupported {
		t.Fatalf("error = %v, want %s", err, apperror.StoreSchemaUnsupported)
	}
	if strings.Contains(err.Error(), sensitiveValue) {
		t.Fatalf("error exposed migration name: %v", err)
	}
}
