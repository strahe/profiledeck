package provider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
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
		service: NewService(runtimeService.StoreFactory(), maintenance, registry),
	}
}

func TestServiceRequiresHealthyInitializedStore(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")
	environment := newProviderTestEnvironment(t, configDir, agent.AccessUnrestricted)
	_, err := environment.service.List(ctx)
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
	_, err = environment.service.List(ctx)
	assertProviderErrorCode(t, err, apperror.StoreSchemaInvalid)
}

func TestServiceValidatesAndRedactsMetadata(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessUnrestricted)
	initResult, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
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
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	created, err := environment.service.Create(ctx, CreateRequest{
		ID: "provider-b", Name: "Provider B", AdapterID: "generic",
	})
	if err != nil || created.ID != "provider-b" {
		t.Fatalf("create Provider: value=%#v err=%v", created, err)
	}
	listed, err := environment.service.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != "provider-b" {
		t.Fatalf("list Provider: values=%#v err=%v", listed, err)
	}
	name := "Provider B Updated"
	updated, err := environment.service.Update(ctx, UpdateRequest{ID: "provider-b", Name: &name})
	if err != nil || updated.Name != name {
		t.Fatalf("update Provider: value=%#v err=%v", updated, err)
	}
	_, err = environment.service.Update(ctx, UpdateRequest{ID: "provider-b"})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)

	lock, err := targetfs.AcquireLock(environment.runtime.Paths().Lock, "switch-provider-test")
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	_, err = environment.service.Update(ctx, UpdateRequest{ID: "provider-b", Name: &name})
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
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	_, err := environment.service.Create(ctx, CreateRequest{
		ID: "codex", Name: "Codex", AdapterID: "codex",
	})
	assertProviderErrorCode(t, err, apperror.ProviderInvalid)
}

func TestDesktopAgentPreferenceDoesNotGateProviderData(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessDesktopPreferences)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", MetadataJSON: "{}",
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

	if _, err := environment.service.Get(ctx, "codex"); err != nil {
		t.Fatalf("Desktop preference gated Provider get: %v", err)
	}
	listed, err := environment.service.List(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("Desktop preference filtered Provider data: values=%#v err=%v", listed, err)
	}
	if _, err := environment.service.Get(ctx, "generic-provider"); err != nil {
		t.Fatalf("get generic Provider: %v", err)
	}
	deleted, err := environment.service.Delete(ctx, "codex", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("Desktop preference gated Provider delete: value=%#v err=%v", deleted, err)
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	if _, err := db.GetProvider(ctx, "codex"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted Provider remains in the store: %v", err)
	}
}

func TestDeleteBlocksUnresolvedRecoveryStateThenRemovesOwnedDataAndHistory(t *testing.T) {
	ctx := context.Background()
	environment := newProviderTestEnvironment(t, t.TempDir(), agent.AccessDesktopPreferences)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	externalPath := filepath.Join(t.TempDir(), "tool-owned.json")
	if err := os.WriteFile(externalPath, []byte("external-state\n"), 0o600); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Provider: %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID: "work", Name: "Work", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Profile: %v", err)
	}
	if _, err := db.UpsertProviderSetting(ctx, store.UpsertProviderSettingParams{
		ProviderID: "codex", SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON: `{"usage_sync_interval_seconds":30}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Provider settings: %v", err)
	}
	if _, err := db.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
		ProfileID: "work", ProviderID: "codex", SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON: `{"quota_refresh_interval_seconds":0,"auth_keepalive_enabled":false}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Provider Profile settings: %v", err)
	}
	credentialPayload := `{"token":"saved"}`
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: credentialPayload, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(credentialPayload))),
		MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create credential: %v", err)
	}
	configPayload := "model = \"saved\"\n"
	if _, err := db.UpsertProviderConfigSet(ctx, store.UpsertProviderConfigSetParams{
		ID: "config", ProviderID: "codex", ConfigKind: "codex-config-toml", Name: "Saved",
		PayloadText: configPayload, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(configPayload))),
		MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Config Set: %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "work", ProviderID: "codex", SlotID: "auth", CredentialID: "credential",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("bind credential: %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "work", ProviderID: "codex", SlotID: "user-config", ConfigSetID: "config",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("bind Config Set: %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "work", ProviderID: "codex", TargetID: "generic-file",
		Path: externalPath, Format: "text", Strategy: "replace-file",
		ValueJSON: `{"content":"desired-state\n"}`, Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create target: %v", err)
	}
	if _, err := db.SetProviderActiveState(ctx, "codex", "work"); err != nil {
		_ = db.Close()
		t.Fatalf("create active state: %v", err)
	}
	if _, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1); err != nil {
		_ = db.Close()
		t.Fatalf("create Usage source: %v", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "unfinished-switch", ProviderID: "codex", ProfileIDs: []string{"work"},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion, MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create unfinished operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	if _, err := environment.agents.SetEnabled(ctx, agent.Codex, false); err != nil {
		t.Fatalf("disable Desktop Agent: %v", err)
	}

	_, err = environment.service.Delete(ctx, "codex", true)
	assertProviderErrorCode(t, err, apperror.ProviderInUse)

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if err := db.ResolveOperation(ctx, "unfinished-switch", "closed_before_target_writes"); err != nil {
		_ = db.Close()
		t.Fatalf("resolve operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close resolved store: %v", err)
	}
	if result, err := environment.service.Delete(ctx, "codex", true); err != nil || !result.Deleted {
		t.Fatalf("delete Provider: result=%#v err=%v", result, err)
	}
	content, err := os.ReadFile(externalPath)
	if err != nil || string(content) != "external-state\n" {
		t.Fatalf("Provider delete changed an external target: content=%q err=%v", content, err)
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open deleted state: %v", err)
	}
	defer db.Close()
	if _, err := db.GetProvider(ctx, "codex"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Provider remains after delete: %v", err)
	}
	if _, err := db.GetProfile(ctx, "work"); err != nil {
		t.Fatalf("global Profile was deleted with Provider: %v", err)
	}
	for label, err := range map[string]error{
		"Provider settings":         func() error { _, err := db.GetProviderSetting(ctx, "codex"); return err }(),
		"Provider Profile settings": func() error { _, err := db.GetProviderProfileSetting(ctx, "work", "codex"); return err }(),
		"credential":                func() error { _, err := db.GetProviderCredential(ctx, "credential"); return err }(),
		"Config Set":                func() error { _, err := db.GetProviderConfigSet(ctx, "codex", "config"); return err }(),
		"target":                    func() error { _, err := db.GetProfileTarget(ctx, "work", "codex", "generic-file"); return err }(),
		"active state":              func() error { _, err := db.GetActiveState(ctx, "codex"); return err }(),
		"Usage source":              func() error { _, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl"); return err }(),
		"resolved operation":        func() error { _, err := db.GetOperation(ctx, "unfinished-switch"); return err }(),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s remains after Provider delete: %v", label, err)
		}
	}
	states, err := environment.agents.List(ctx)
	if err != nil {
		t.Fatalf("list Desktop Agent preferences: %v", err)
	}
	for _, state := range states {
		if state.Manifest.ID == agent.Codex && state.Enabled {
			t.Fatalf("Provider delete reset the Desktop Agent preference: %#v", state)
		}
	}
}

func assertProviderErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}
