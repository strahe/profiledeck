package provider

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type providerTestEnvironment struct {
	runtime *profilesruntime.Service
	agents  *agent.Service
	service *Service
}

func newProviderTestEnvironment(t *testing.T, configDir string, access agent.AccessMode) *providerTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	registry := agent.BuiltinRegistry()
	agentService := agent.NewService(registry, runtimeService.StoreFactory(), access)
	dependencies := switching.NewDependencies(
		switchtarget.MustRegistry(switchtarget.FileBackend{}),
		switchplan.MustRegistry(switchplan.GenericAdapter{}),
	)
	maintenance := switching.NewService(runtimeService.Paths(), runtimeService.StoreFactory(), agentService, dependencies)
	return &providerTestEnvironment{
		runtime: runtimeService,
		agents:  agentService,
		service: NewService(runtimeService.StoreFactory(), maintenance, agentService, registry),
	}
}

func TestServiceRequiresHealthyInitializedStore(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")
	environment := newProviderTestEnvironment(t, configDir, agent.AccessUnrestricted)
	_, err := environment.service.List(ctx, ListRequest{})
	assertProviderErrorCode(t, err, apperror.StoreNotInitialized)
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Provider read created runtime directory: %v", err)
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
	_, err = environment.service.List(ctx, ListRequest{})
	assertProviderErrorCode(t, err, apperror.StoreSchemaInvalid)
}

func TestServiceValidatesAndRedactsMetadata(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessUnrestricted)
	initResult, err := environment.runtime.Init(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	_, err = environment.service.Create(ctx, CreateRequest{ID: "ProviderA", Name: "Provider A", AdapterID: "adapter-a"})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)

	for _, raw := range []string{
		`[]`, `{"openai_api_key":"secret"}`, `{"apiKey":"secret"}`,
		`{"accessToken":"secret"}`, `{"my_secret":"secret"}`,
		`{"nested":{"refreshToken":"secret"}}`, `{"items":[{"password":"secret"}]}`,
	} {
		_, err := environment.service.Create(ctx, CreateRequest{
			ID: "provider-a", Name: "Provider A", AdapterID: "adapter-a", MetadataJSON: &raw,
		})
		assertProviderErrorCode(t, err, apperror.ProviderInvalid)
	}
	large := `{"blob":"` + strings.Repeat("x", 64*1024) + `"}`
	_, err = environment.service.Create(ctx, CreateRequest{
		ID: "provider-a", Name: "Provider A", AdapterID: "adapter-a", MetadataJSON: &large,
	})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)

	db, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `
		INSERT INTO providers (id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('provider-a', 'Provider A', 'adapter-a',
		'{"apiKey":"raw-key","my_secret":"raw-secret","max_tokens":100,"safe":"ok","nested":{"authorization":"Bearer raw"},"items":[{"refreshToken":"raw-refresh"}]}',
		1, 1)
	`)
	if err != nil {
		t.Fatalf("seed Provider: %v", err)
	}
	value, err := environment.service.Get(ctx, "provider-a")
	if err != nil {
		t.Fatalf("get Provider: %v", err)
	}
	for _, key := range []string{"apiKey", "my_secret", "max_tokens"} {
		if value.Metadata[key] != "[REDACTED]" {
			t.Fatalf("metadata key %s was not redacted: %#v", key, value.Metadata[key])
		}
	}
	if value.Metadata["safe"] != "ok" || value.Metadata["nested"].(map[string]any)["authorization"] != "[REDACTED]" {
		t.Fatalf("unexpected redacted metadata: %#v", value.Metadata)
	}
	if value.Metadata["items"].([]any)[0].(map[string]any)["refreshToken"] != "[REDACTED]" {
		t.Fatalf("nested array secret was not redacted: %#v", value.Metadata)
	}
}

func TestServiceCRUDAndSharedLock(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessUnrestricted)
	if _, err := environment.runtime.Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	disabled := false
	created, err := environment.service.Create(ctx, CreateRequest{
		ID: "provider-b", Name: "Provider B", AdapterID: "generic", Enabled: &disabled,
	})
	if err != nil || created.Enabled {
		t.Fatalf("unexpected disabled Provider create: value=%#v err=%v", created, err)
	}
	listed, err := environment.service.List(ctx, ListRequest{})
	if err != nil || len(listed) != 0 {
		t.Fatalf("disabled Provider should be hidden: values=%#v err=%v", listed, err)
	}
	enabled := true
	updated, err := environment.service.Update(ctx, UpdateRequest{ID: "provider-b", Enabled: &enabled})
	if err != nil || !updated.Enabled {
		t.Fatalf("enable Provider: value=%#v err=%v", updated, err)
	}
	_, err = environment.service.Update(ctx, UpdateRequest{ID: "provider-b"})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)

	lock, err := targetfs.AcquireLock(environment.runtime.Paths().Lock, "switch-provider-test")
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	_, err = environment.service.Update(ctx, UpdateRequest{ID: "provider-b", Enabled: &disabled})
	assertProviderErrorCode(t, err, apperror.LockAcquireFailed)
	_, err = environment.service.Delete(ctx, "provider-b", true)
	assertProviderErrorCode(t, err, apperror.LockAcquireFailed)
	lock.Release()
	deleted, err := environment.service.Delete(ctx, "provider-b", true)
	if err != nil || !deleted.Deleted || deleted.ID != "provider-b" {
		t.Fatalf("delete Provider: value=%#v err=%v", deleted, err)
	}
}

func TestServiceRejectsGenericManagedProviderCreate(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessUnrestricted)
	if _, err := environment.runtime.Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	_, err := environment.service.Create(ctx, CreateRequest{
		ID: "codex", Name: "Codex", AdapterID: "codex",
	})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)
}

func TestDesktopAgentPolicyCoversManagedProviderCRUD(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessDesktopPreferences)
	if _, err := environment.runtime.Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", Enabled: true, MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("seed managed Provider: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := environment.service.Create(ctx, CreateRequest{
		ID: "generic-provider", Name: "Generic", AdapterID: "generic",
	}); err != nil {
		t.Fatalf("create generic Provider: %v", err)
	}
	if _, err := environment.agents.SetEnabled(ctx, agent.Codex, false); err != nil {
		t.Fatalf("disable Codex Agent: %v", err)
	}

	_, err = environment.service.Get(ctx, "codex")
	assertProviderErrorCode(t, err, apperror.AgentDisabled)
	disabled := false
	_, err = environment.service.Update(ctx, UpdateRequest{ID: "codex", Enabled: &disabled})
	assertProviderErrorCode(t, err, apperror.AgentDisabled)
	_, err = environment.service.Delete(ctx, "codex", true)
	assertProviderErrorCode(t, err, apperror.AgentDisabled)
	_, err = environment.service.Create(ctx, CreateRequest{ID: "codex", Name: "Codex", AdapterID: "codex"})
	assertProviderErrorCode(t, err, apperror.AgentDisabled)
	listed, err := environment.service.List(ctx, ListRequest{IncludeDisabled: true})
	if err != nil || len(listed) != 1 || listed[0].ID != "generic-provider" {
		t.Fatalf("managed Provider gate was bypassed by list: values=%#v err=%v", listed, err)
	}
	if _, err := environment.service.Get(ctx, "generic-provider"); err != nil {
		t.Fatalf("generic Provider was affected by Agent gate: %v", err)
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	stored, err := db.GetProvider(ctx, "codex")
	if err != nil || !stored.Enabled {
		t.Fatalf("Agent toggle changed Provider enabled state: value=%#v err=%v", stored, err)
	}
}

func assertProviderErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}
