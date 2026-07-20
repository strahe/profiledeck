package profile

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type profileTestEnvironment struct {
	runtime *profilesruntime.Service
	service *Service
}

func newProfileTestEnvironment(t *testing.T, configDir string) *profileTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	registry := agent.BuiltinRegistry()
	policy := agent.NewService(registry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	dependencies := switching.NewDependencies(
		switchtarget.MustRegistry(switchtarget.FileBackend{}),
		switchplan.MustRegistry(switchplan.GenericAdapter{}),
	)
	maintenance := switching.NewService(runtimeService.Paths(), runtimeService.StoreFactory(), policy, dependencies)
	return &profileTestEnvironment{runtime: runtimeService, service: NewService(runtimeService.StoreFactory(), maintenance, DeleteRegistry{})}
}

func TestServiceRequiresHealthyInitializedStore(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")
	environment := newProfileTestEnvironment(t, configDir)
	_, err := environment.service.List(ctx)
	assertProfileErrorCode(t, err, apperror.StoreNotInitialized)
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Profile read created runtime directory: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(environment.runtime.Paths().Database), 0o700); err != nil {
		t.Fatalf("create database directory: %v", err)
	}
	file, err := os.Create(environment.runtime.Paths().Database)
	if err != nil {
		t.Fatalf("create empty database: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close empty database: %v", err)
	}
	_, err = environment.service.List(ctx)
	assertProfileErrorCode(t, err, apperror.StoreSchemaInvalid)
}

func TestServiceAcceptsNonCredentialTokenMetadata(t *testing.T) {
	ctx := context.Background()
	environment := newProfileTestEnvironment(t, t.TempDir())
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	metadata := `{"max_tokens":100,"nested":{"token_budget":1000}}`
	created, err := environment.service.Create(ctx, CreateRequest{
		ID: "profile-a", Name: "Profile A", MetadataJSON: &metadata,
	})
	if err != nil {
		t.Fatalf("create Profile: %v", err)
	}
	if created.Metadata["max_tokens"] == nil {
		t.Fatalf("token budget metadata was not preserved: %#v", created.Metadata)
	}
}

func TestDeleteRequiresConfirmationAndRejectsReferencedProfile(t *testing.T) {
	ctx := context.Background()
	environment := newProfileTestEnvironment(t, t.TempDir())
	initResult, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.Create(ctx, CreateRequest{ID: "profile-a", Name: "Profile A"}); err != nil {
		t.Fatalf("create Profile: %v", err)
	}
	if _, err := environment.service.Create(ctx, CreateRequest{ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("create second Profile: %v", err)
	}
	_, err = environment.service.Delete(ctx, "profile-a", false)
	assertProfileErrorCode(t, err, apperror.ConfirmationRequired)

	db, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, profile_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES
			('operation-a', 'switch', 'pending', 'profile-a', 1, 1),
			('operation-b', 'switch', 'failed', 'profile-b', 1, 1)
	`)
	if err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	_, err = environment.service.Delete(ctx, "profile-a", true)
	assertProfileErrorDetail(t, err, apperror.ProfileInUse, "reason", DeleteReasonUnresolvedOperation)
	_, err = environment.service.Delete(ctx, "profile-b", true)
	assertProfileErrorDetail(t, err, apperror.ProfileInUse, "reason", DeleteReasonUnresolvedOperation)
}

func TestDeleteReportsActiveProfileReason(t *testing.T) {
	ctx := context.Background()
	environment := newProfileTestEnvironment(t, t.TempDir())
	initResult, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.Create(ctx, CreateRequest{ID: "profile-a", Name: "Profile A"}); err != nil {
		t.Fatalf("create Profile: %v", err)
	}
	db, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
		VALUES ('provider', 'claude-code', 'profile-a', 'history', 1)
	`); err != nil {
		t.Fatalf("seed active state: %v", err)
	}

	_, err = environment.service.Delete(ctx, "profile-a", true)
	assertProfileErrorDetail(t, err, apperror.ProfileInUse, "reason", DeleteReasonActive)
}

func assertProfileErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}

func assertProfileErrorDetail(t *testing.T, err error, code apperror.Code, key string, want any) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code || appErr.Details[key] != want {
		t.Fatalf("error = %v, want code %q with %s=%v", err, code, key, want)
	}
}
