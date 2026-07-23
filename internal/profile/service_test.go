package profile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type profileTestEnvironment struct {
	runtime *profilesruntime.Service
	service *Service
}

type resourceDeleteParticipant struct {
	providerID string
	failAfter  error
}

func (participant resourceDeleteParticipant) ProviderID() string {
	return participant.providerID
}

func (participant resourceDeleteParticipant) DeleteProfileData(
	ctx context.Context,
	db *store.Store,
	profileID string,
) error {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, participant.providerID)
	if err != nil {
		return err
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, participant.providerID)
	if err != nil {
		return err
	}
	for _, binding := range credentialBindings {
		if err := db.DeleteProfileCredentialBinding(ctx, profileID, participant.providerID, binding.SlotID); err != nil {
			return err
		}
		if participant.failAfter != nil {
			return participant.failAfter
		}
		if err := db.DeleteProviderCredential(ctx, binding.CredentialID); err != nil && !errors.Is(err, store.ErrInUse) {
			return err
		}
	}
	for _, binding := range configBindings {
		if err := db.DeleteProfileConfigSetBinding(ctx, profileID, participant.providerID, binding.SlotID); err != nil {
			return err
		}
		if err := db.DeleteProviderConfigSet(ctx, participant.providerID, binding.ConfigSetID); err != nil && !errors.Is(err, store.ErrInUse) {
			return err
		}
	}
	return nil
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
		INSERT INTO providers (
			id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms
		) VALUES ('provider-a', 'Provider A', 'generic', '{}', 1, 1);
		INSERT INTO operations (
			id, provider_id, operation_type, status, metadata_schema_version,
			metadata_json, created_at_unix_ms, updated_at_unix_ms
		)
		VALUES
			('operation-a', 'provider-a', 'switch', 'pending', 1,
			 '{"provider_id":"provider-a","related_profile_ids":["profile-a"]}', 1, 1),
			('operation-b', 'provider-a', 'switch', 'failed', 1,
			 '{"provider_id":"provider-a","related_profile_ids":["profile-b"]}', 1, 1);
		INSERT INTO operation_profiles (operation_id, profile_id)
		VALUES ('operation-a', 'profile-a'), ('operation-b', 'profile-b')
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
		INSERT INTO providers (
			id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms
		) VALUES ('claude-code', 'Claude Code', 'claude-code', '{}', 1, 1);
		INSERT INTO provider_active_states (
			provider_id, profile_id, revision, updated_at_unix_ms
		) VALUES ('claude-code', 'profile-a', 1, 1)
	`); err != nil {
		t.Fatalf("seed active state: %v", err)
	}

	_, err = environment.service.Delete(ctx, "profile-a", true)
	assertProfileErrorDetail(t, err, apperror.ProfileInUse, "reason", DeleteReasonActive)
}

func TestDeleteRemovesResolvedHistoryAndUnsharedResourcesButPreservesSharedState(t *testing.T) {
	ctx := context.Background()
	environment := newProfileTestEnvironment(t, t.TempDir())
	environment.service = NewService(
		environment.runtime.StoreFactory(),
		environment.service.maintenance,
		MustDeleteRegistry(resourceDeleteParticipant{providerID: "codex"}),
	)
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
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("create Provider: %v", err)
	}
	for _, profileID := range []string{"delete-me", "keep-me"} {
		if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
			ID: profileID, Name: profileID, MetadataJSON: `{}`,
		}); err != nil {
			t.Fatalf("create Profile %q: %v", profileID, err)
		}
	}
	for _, credential := range []store.UpsertProviderCredentialParams{
		{
			ID: "shared-credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
			PayloadJSON:   `{"token":"shared"}`,
			PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(`{"token":"shared"}`))),
			MetadataJSON:  `{}`,
		},
		{
			ID: "unique-credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
			PayloadJSON:   `{"token":"unique"}`,
			PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(`{"token":"unique"}`))),
			MetadataJSON:  `{}`,
		},
	} {
		if _, err := db.UpsertProviderCredential(ctx, credential); err != nil {
			t.Fatalf("create credential %q: %v", credential.ID, err)
		}
	}
	for _, configSet := range []store.UpsertProviderConfigSetParams{
		{
			ID: "shared-config", ProviderID: "codex", ConfigKind: "toml", Name: "Shared",
			PayloadText:   "shared = true\n",
			PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("shared = true\n"))),
			MetadataJSON:  `{}`,
		},
		{
			ID: "unique-config", ProviderID: "codex", ConfigKind: "toml", Name: "Unique",
			PayloadText:   "unique = true\n",
			PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("unique = true\n"))),
			MetadataJSON:  `{}`,
		},
	} {
		if _, err := db.UpsertProviderConfigSet(ctx, configSet); err != nil {
			t.Fatalf("create Config Set %q: %v", configSet.ID, err)
		}
	}
	for _, profileID := range []string{"delete-me", "keep-me"} {
		if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: "codex", SlotID: "shared-auth", CredentialID: "shared-credential",
		}); err != nil {
			t.Fatalf("bind shared credential to %q: %v", profileID, err)
		}
		if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
			ProfileID: profileID, ProviderID: "codex", SlotID: "shared-config", ConfigSetID: "shared-config",
		}); err != nil {
			t.Fatalf("bind shared Config Set to %q: %v", profileID, err)
		}
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "delete-me", ProviderID: "codex", SlotID: "unique-auth", CredentialID: "unique-credential",
	}); err != nil {
		t.Fatalf("bind unique credential: %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "delete-me", ProviderID: "codex", SlotID: "unique-config", ConfigSetID: "unique-config",
	}); err != nil {
		t.Fatalf("bind unique Config Set: %v", err)
	}
	if _, err := db.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
		ProfileID: "delete-me", ProviderID: "codex", SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON: `{}`,
	}); err != nil {
		t.Fatalf("create Profile settings: %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "delete-me", ProviderID: "codex", TargetID: "generic-file",
		Path: externalPath, Format: "text", Strategy: "replace-file",
		ValueJSON: `{"content":"desired-state\n"}`, Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1); err != nil {
		t.Fatalf("create Usage source: %v", err)
	}
	if _, err := db.CreateAppliedImportOperation(ctx, store.CreateAppliedImportOperationParams{
		ID: "multi-profile-import", ProviderID: "codex",
		ProfileIDs:            []string{"delete-me", "keep-me"},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("create multi-Profile operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	db = nil

	if result, err := environment.service.Delete(ctx, "delete-me", true); err != nil || !result.Deleted {
		t.Fatalf("delete Profile: result=%#v err=%v", result, err)
	}
	content, err := os.ReadFile(externalPath)
	if err != nil || string(content) != "external-state\n" {
		t.Fatalf("Profile delete changed an external target: content=%q err=%v", content, err)
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open deleted state: %v", err)
	}
	if _, err := db.GetProfile(ctx, "delete-me"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted Profile remains: %v", err)
	}
	if _, err := db.GetProfile(ctx, "keep-me"); err != nil {
		t.Fatalf("unrelated Profile was removed: %v", err)
	}
	if _, err := db.GetProvider(ctx, "codex"); err != nil {
		t.Fatalf("Provider was removed with Profile: %v", err)
	}
	if _, err := db.GetProviderCredential(ctx, "shared-credential"); err != nil {
		t.Fatalf("shared credential was reaped: %v", err)
	}
	if _, err := db.GetProviderConfigSet(ctx, "codex", "shared-config"); err != nil {
		t.Fatalf("shared Config Set was reaped: %v", err)
	}
	if _, err := db.GetProviderCredential(ctx, "unique-credential"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unshared credential remains: %v", err)
	}
	if _, err := db.GetProviderConfigSet(ctx, "codex", "unique-config"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unshared Config Set remains: %v", err)
	}
	if _, err := db.GetOperation(ctx, "multi-profile-import"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("multi-Profile operation remains: %v", err)
	}
	if _, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl"); err != nil {
		t.Fatalf("Profile delete removed Provider Usage: %v", err)
	}
}

func TestDeleteRollsBackParticipantMutationsOnFailure(t *testing.T) {
	ctx := context.Background()
	environment := newProfileTestEnvironment(t, t.TempDir())
	forced := errors.New("forced participant failure")
	environment.service = NewService(
		environment.runtime.StoreFactory(),
		environment.service.maintenance,
		MustDeleteRegistry(resourceDeleteParticipant{providerID: "codex", failAfter: forced}),
	)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
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
	payload := `{"token":"saved"}`
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: payload, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(payload))),
		MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create credential: %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "work", ProviderID: "codex", SlotID: "auth", CredentialID: "credential",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("bind credential: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	if _, err := environment.service.Delete(ctx, "work", true); !errors.Is(err, forced) {
		t.Fatalf("delete failure = %v, want forced participant failure", err)
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open rolled-back state: %v", err)
	}
	defer db.Close()
	if _, err := db.GetProfile(ctx, "work"); err != nil {
		t.Fatalf("failed delete removed Profile: %v", err)
	}
	if _, err := db.GetProviderCredential(ctx, "credential"); err != nil {
		t.Fatalf("failed delete removed credential: %v", err)
	}
	bindings, err := db.ListProfileCredentialBindings(ctx, "work", "codex")
	if err != nil || len(bindings) != 1 || bindings[0].CredentialID != "credential" {
		t.Fatalf("failed delete removed binding: bindings=%#v err=%v", bindings, err)
	}
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
