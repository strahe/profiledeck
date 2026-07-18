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

func TestStatusRejectsUnsupportedSchema(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	if err := service.EnsureDirectories(); err != nil {
		t.Fatalf("prepare runtime directories: %v", err)
	}
	db, err := service.StoreFactory().Open(ctx, false)
	if err != nil {
		t.Fatalf("open runtime database: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("initialize runtime database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close runtime database: %v", err)
	}

	sqlDB, err := sql.Open("sqlite", service.Paths().Database)
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
