package app

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
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
)

func TestApplicationExposesOneTypedServiceGraph(t *testing.T) {
	application, err := New(Config{ConfigDir: t.TempDir(), CodexDir: t.TempDir(), AgentAccess: agent.AccessUnrestricted})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	if application.Runtime() == nil || application.Agents() == nil || application.Providers() == nil ||
		application.Profiles() == nil || application.Targets() == nil || application.Switching() == nil ||
		application.Doctor() == nil || application.Usage() == nil || application.Settings() == nil ||
		application.Codex() == nil || application.Antigravity() == nil || application.ClaudeCode() == nil {
		t.Fatal("application composition returned a nil typed service")
	}
}

func TestApplicationRejectsInvalidConstruction(t *testing.T) {
	if _, err := New(Config{ConfigDir: t.TempDir(), AgentAccess: agent.AccessMode("invalid")}); err == nil {
		t.Fatal("invalid Agent access mode was accepted")
	}
	if _, err := NewWithDependencies(Config{ConfigDir: t.TempDir()}, Dependencies{}); err == nil {
		t.Fatal("missing explicit dependencies were accepted")
	}
}

func TestApplicationRejectsDivergentAgentAndAdapterOwnership(t *testing.T) {
	t.Run("Agent Provider without managed adapter", func(t *testing.T) {
		dependencies := defaultDependencies()
		dependencies.agents = agent.MustRegistry(agent.Manifest{
			ID: "future", DisplayName: "Future", ProviderIDs: []string{"future"}, DefaultDesktopEnabled: true,
		})
		if _, err := NewWithDependencies(Config{ConfigDir: t.TempDir()}, dependencies); err == nil {
			t.Fatal("application accepted an Agent-owned Provider without a managed plan adapter")
		}
	})

	t.Run("managed adapter Provider without Agent", func(t *testing.T) {
		dependencies := defaultDependencies()
		dependencies.agents = agent.MustRegistry()
		if _, err := NewWithDependencies(Config{ConfigDir: t.TempDir()}, dependencies); err == nil {
			t.Fatal("application accepted a managed plan adapter without an owning Agent")
		}
	})

	t.Run("usage Provider without Agent", func(t *testing.T) {
		dependencies := defaultDependencies()
		dependencies.agents = agent.MustRegistry()
		dependencies.switching.Adapters = switchplan.MustRegistry(switchplan.GenericAdapter{})
		_, err := NewWithDependencies(Config{ConfigDir: t.TempDir()}, dependencies)
		if err == nil || !strings.Contains(err.Error(), "usage Provider \"codex\" has no owning Agent") {
			t.Fatalf("application accepted a usage Provider without an owning Agent: %v", err)
		}
	})
}

func TestApplicationCoordinatesGenericProfileSwitchAcrossServices(t *testing.T) {
	ctx := context.Background()
	application, err := New(Config{ConfigDir: t.TempDir(), AgentAccess: agent.AccessUnrestricted})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	if _, err := application.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := application.Providers().Create(ctx, provider.CreateRequest{
		ID: "provider-a", Name: "Provider A", AdapterID: "generic",
	}); err != nil {
		t.Fatalf("create Provider: %v", err)
	}
	if _, err := application.Profiles().Create(ctx, profile.CreateRequest{ID: "profile-a", Name: "Profile A"}); err != nil {
		t.Fatalf("create Profile: %v", err)
	}
	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := application.Targets().Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "settings",
		Path: targetPath, Format: profiletarget.FormatText, Strategy: profiletarget.StrategyReplaceFile,
		ValueJSON: `{"content":"applied\n"}`,
	}); err != nil {
		t.Fatalf("create Profile target: %v", err)
	}
	plan, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: "provider-a", ProfileID: "profile-a"})
	if err != nil || len(plan.Operations) != 1 {
		t.Fatalf("build switch plan: plan=%#v err=%v", plan, err)
	}
	result, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
		ExpectedPlanFingerprint: plan.PlanFingerprint,
	})
	if err != nil || result.Status != "applied" {
		t.Fatalf("apply switch: result=%#v err=%v", result, err)
	}
	active, err := application.Providers().ListActiveStates(ctx)
	if err != nil || len(active) != 1 || active[0].ProfileID != "profile-a" {
		t.Fatalf("read active state: values=%#v err=%v", active, err)
	}
}

func TestDesktopApplicationRejectsDisabledAgentButKeepsRecoveryServicesAvailable(t *testing.T) {
	ctx := context.Background()
	application, err := New(Config{ConfigDir: t.TempDir(), CodexDir: t.TempDir(), AgentAccess: agent.AccessDesktopPreferences})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	if _, err := application.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := application.Agents().SetEnabled(ctx, agent.Codex, false); err != nil {
		t.Fatalf("disable Codex Agent: %v", err)
	}
	_, err = application.Codex().ListProfiles(ctx)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.AgentDisabled {
		t.Fatalf("disabled Codex call returned %v", err)
	}
	if _, err := application.Backups().List(ctx); err != nil {
		t.Fatalf("Agent gate blocked backup inspection: %v", err)
	}
	if _, err := application.Doctor().Run(ctx); err != nil {
		t.Fatalf("Agent gate blocked Doctor: %v", err)
	}
}

func TestApplicationDeletesGlobalProfileDataWithoutChangingExternalState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	application, err := New(Config{ConfigDir: configDir, CodexDir: t.TempDir(), AgentAccess: agent.AccessUnrestricted})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	defer application.Close()
	if _, err := application.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, fixture := range []store.CreateProviderParams{
		{ID: codexconfig.ProviderID, Name: codexpreset.ProviderName, AdapterID: codexconfig.AdapterID, Enabled: true, MetadataJSON: `{}`},
		{ID: agyconfig.ProviderID, Name: agyconfig.ProviderName, AdapterID: agyconfig.AdapterID, Enabled: false, MetadataJSON: `{}`},
		{ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName, AdapterID: claudecodeconfig.AdapterID, Enabled: true, MetadataJSON: `{}`},
		{ID: "generic", Name: "Generic", AdapterID: "generic", Enabled: true, MetadataJSON: `{}`},
	} {
		if _, err := db.CreateProvider(ctx, fixture); err != nil {
			_ = db.Close()
			t.Fatalf("create Provider %q: %v", fixture.ID, err)
		}
	}
	for _, fixture := range []store.CreateProfileParams{
		{ID: "doomed", Name: "Doomed", MetadataJSON: `{}`},
		{ID: "shared", Name: "Shared", MetadataJSON: `{}`},
	} {
		if _, err := db.CreateProfile(ctx, fixture); err != nil {
			_ = db.Close()
			t.Fatalf("create Profile %q: %v", fixture.ID, err)
		}
	}

	credentials := []store.UpsertProviderCredentialParams{
		testCredential("codex-exclusive", codexconfig.ProviderID, codexpreset.CredentialKindAuthJSON, `{"tokens":{"access_token":"private"}}`),
		testCredential("agy-shared", agyconfig.ProviderID, agyconfig.CredentialKind, `{}`),
		testCredential("claude-exclusive", claudecodeconfig.ProviderID, claudecodeconfig.CredentialKind, `{}`),
		testCredential("unrelated-orphan", codexconfig.ProviderID, codexpreset.CredentialKindAuthJSON, `{}`),
	}
	for _, fixture := range credentials {
		if _, err := db.UpsertProviderCredential(ctx, fixture); err != nil {
			_ = db.Close()
			t.Fatalf("create credential %q: %v", fixture.ID, err)
		}
	}
	configPayload := "model = \"gpt-5\"\n"
	for _, fixture := range []store.UpsertProviderConfigSetParams{
		{
			ID: "codex-exclusive-config", ProviderID: codexconfig.ProviderID, ConfigKind: codexpreset.ConfigSetKindTOML,
			Name: "Exclusive", PayloadText: configPayload, PayloadSHA256: testSHA256(configPayload), MetadataJSON: `{}`,
		},
		{
			ID: "unrelated-config", ProviderID: codexconfig.ProviderID, ConfigKind: codexpreset.ConfigSetKindTOML,
			Name: "Unrelated", PayloadText: configPayload, PayloadSHA256: testSHA256(configPayload), MetadataJSON: `{}`,
		},
	} {
		if _, err := db.UpsertProviderConfigSet(ctx, fixture); err != nil {
			_ = db.Close()
			t.Fatalf("create Config Set %q: %v", fixture.ID, err)
		}
	}

	for _, fixture := range []store.UpsertProfileCredentialBindingParams{
		{ProfileID: "doomed", ProviderID: codexconfig.ProviderID, SlotID: codexpreset.CredentialSlotAuth, CredentialID: "codex-exclusive"},
		{ProfileID: "doomed", ProviderID: agyconfig.ProviderID, SlotID: agyconfig.CredentialSlot, CredentialID: "agy-shared"},
		{ProfileID: "shared", ProviderID: agyconfig.ProviderID, SlotID: agyconfig.CredentialSlot, CredentialID: "agy-shared"},
		{ProfileID: "doomed", ProviderID: claudecodeconfig.ProviderID, SlotID: claudecodeconfig.CredentialSlot, CredentialID: "claude-exclusive"},
	} {
		if _, err := db.UpsertProfileCredentialBinding(ctx, fixture); err != nil {
			_ = db.Close()
			t.Fatalf("create credential binding %#v: %v", fixture, err)
		}
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "doomed", ProviderID: codexconfig.ProviderID,
		SlotID: codexpreset.ConfigSetSlotUserConfig, ConfigSetID: "codex-exclusive-config",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Config Set binding: %v", err)
	}
	if _, err := db.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
		ProfileID: "doomed", ProviderID: codexconfig.ProviderID, QuotaRefreshIntervalSeconds: 600, AuthKeepaliveEnabled: true,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create Profile setting: %v", err)
	}
	externalPath := filepath.Join(t.TempDir(), "tool-owned.json")
	const externalContent = "tool-owned-state\n"
	if err := os.WriteFile(externalPath, []byte(externalContent), 0o600); err != nil {
		_ = db.Close()
		t.Fatalf("write external fixture: %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "doomed", ProviderID: "generic", TargetID: "settings", Path: externalPath, PathKey: externalPath,
		Format: "json", Strategy: "replace-file", ValueJSON: `{"content":"managed"}`, Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create generic target: %v", err)
	}
	if _, err := db.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
		ID: "history-applied", ProfileID: "doomed", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create applied history: %v", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "history-resolved", ProfileID: "doomed", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create resolved history: %v", err)
	}
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{ID: "history-resolved", ErrorCode: "TEST", ErrorMessage: "fixture"}); err != nil {
		_ = db.Close()
		t.Fatalf("mark resolved history failed: %v", err)
	}
	if err := db.ResolveOperation(ctx, "history-resolved", "fixture-resolved"); err != nil {
		_ = db.Close()
		t.Fatalf("resolve history: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	raw, err := sql.Open("sqlite", application.Runtime().StoreFactory().DatabasePath())
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE provider_credentials SET payload_json = 'damaged', payload_sha256 = 'damaged' WHERE id = 'codex-exclusive'`); err != nil {
		_ = raw.Close()
		t.Fatalf("damage credential payload fixture: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	result, err := application.Profiles().Delete(ctx, "doomed", true)
	if err != nil || !result.Deleted || result.ID != "doomed" {
		t.Fatalf("delete global Profile: result=%#v err=%v", result, err)
	}
	db, err = application.Runtime().StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db.Close()
	if _, err := db.GetProfile(ctx, "doomed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted Profile still exists: %v", err)
	}
	if _, err := db.GetProfile(ctx, "shared"); err != nil {
		t.Fatalf("unrelated Profile was removed: %v", err)
	}
	for _, credentialID := range []string{"codex-exclusive", "claude-exclusive"} {
		if _, err := db.GetProviderCredential(ctx, credentialID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("exclusive credential %q still exists: %v", credentialID, err)
		}
	}
	for _, credentialID := range []string{"agy-shared", "unrelated-orphan"} {
		if _, err := db.GetProviderCredential(ctx, credentialID); err != nil {
			t.Fatalf("preserved credential %q missing: %v", credentialID, err)
		}
	}
	if _, err := db.GetProviderConfigSet(ctx, "codex-exclusive-config"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("exclusive Config Set still exists: %v", err)
	}
	if _, err := db.GetProviderConfigSet(ctx, "unrelated-config"); err != nil {
		t.Fatalf("unrelated Config Set missing: %v", err)
	}
	if _, err := db.GetProviderProfileSetting(ctx, "doomed", codexconfig.ProviderID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted Profile setting still exists: %v", err)
	}
	if targets, err := db.ListProfileTargets(ctx, "doomed", "", true); err != nil || len(targets) != 0 {
		t.Fatalf("deleted Profile targets = %#v, err=%v", targets, err)
	}
	for _, operationID := range []string{"history-applied", "history-resolved"} {
		if _, err := db.GetOperation(ctx, operationID); err != nil {
			t.Fatalf("historical operation %q missing: %v", operationID, err)
		}
	}
	for _, providerID := range []string{codexconfig.ProviderID, agyconfig.ProviderID, claudecodeconfig.ProviderID, "generic"} {
		if _, err := db.GetProvider(ctx, providerID); err != nil {
			t.Fatalf("Provider %q missing: %v", providerID, err)
		}
	}
	if provider, err := db.GetProvider(ctx, agyconfig.ProviderID); err != nil || provider.Enabled {
		t.Fatalf("disabled Provider changed: provider=%#v err=%v", provider, err)
	}
	contents, err := os.ReadFile(externalPath)
	if err != nil || string(contents) != externalContent {
		t.Fatalf("external tool state changed: content=%q err=%v", contents, err)
	}
	raw, err = sql.Open("sqlite", application.Runtime().StoreFactory().DatabasePath())
	if err != nil {
		t.Fatalf("reopen raw database: %v", err)
	}
	defer raw.Close()
	var operationCount int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(1) FROM operations`).Scan(&operationCount); err != nil || operationCount != 2 {
		t.Fatalf("operations after delete = %d, err=%v, want preserved history only", operationCount, err)
	}
}

func TestApplicationProfileDeleteRollsBackUnsupportedManagedData(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name       string
		providerID string
		slotID     string
		kind       string
	}{
		{name: "unknown Provider", providerID: "future", slotID: "auth", kind: "future-auth"},
		{name: "unsupported Codex slot", providerID: codexconfig.ProviderID, slotID: "future-slot", kind: codexpreset.CredentialKindAuthJSON},
		{name: "unsupported Codex resource kind", providerID: codexconfig.ProviderID, slotID: codexpreset.CredentialSlotAuth, kind: "future-auth"},
	} {
		t.Run(test.name, func(t *testing.T) {
			application, err := New(Config{ConfigDir: t.TempDir(), CodexDir: t.TempDir(), AgentAccess: agent.AccessUnrestricted})
			if err != nil {
				t.Fatalf("create application: %v", err)
			}
			defer application.Close()
			if _, err := application.Initialize(ctx); err != nil {
				t.Fatalf("initialize runtime: %v", err)
			}
			db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			providers := []store.CreateProviderParams{
				{ID: agyconfig.ProviderID, Name: agyconfig.ProviderName, AdapterID: agyconfig.AdapterID, Enabled: true, MetadataJSON: `{}`},
				{ID: test.providerID, Name: "Test", AdapterID: test.providerID, Enabled: true, MetadataJSON: `{}`},
			}
			if test.providerID == codexconfig.ProviderID {
				providers[1].Name = codexpreset.ProviderName
				providers[1].AdapterID = codexconfig.AdapterID
			}
			for _, fixture := range providers {
				if _, err := db.CreateProvider(ctx, fixture); err != nil {
					_ = db.Close()
					t.Fatalf("create Provider %q: %v", fixture.ID, err)
				}
			}
			if _, err := db.CreateProfile(ctx, store.CreateProfileParams{ID: "doomed", Name: "Doomed", MetadataJSON: `{}`}); err != nil {
				_ = db.Close()
				t.Fatalf("create Profile: %v", err)
			}
			for _, fixture := range []store.UpsertProviderCredentialParams{
				testCredential("agy-exclusive", agyconfig.ProviderID, agyconfig.CredentialKind, `{}`),
				testCredential("unsupported", test.providerID, test.kind, `{}`),
			} {
				if _, err := db.UpsertProviderCredential(ctx, fixture); err != nil {
					_ = db.Close()
					t.Fatalf("create credential %q: %v", fixture.ID, err)
				}
			}
			for _, fixture := range []store.UpsertProfileCredentialBindingParams{
				{ProfileID: "doomed", ProviderID: agyconfig.ProviderID, SlotID: agyconfig.CredentialSlot, CredentialID: "agy-exclusive"},
				{ProfileID: "doomed", ProviderID: test.providerID, SlotID: test.slotID, CredentialID: "unsupported"},
			} {
				if _, err := db.UpsertProfileCredentialBinding(ctx, fixture); err != nil {
					_ = db.Close()
					t.Fatalf("create binding: %v", err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close seed store: %v", err)
			}

			_, err = application.Profiles().Delete(ctx, "doomed", true)
			assertApplicationErrorDetail(t, err, apperror.ProfileInUse, "reason", profile.DeleteReasonUnsupportedManagedData)
			db, err = application.Runtime().StoreFactory().OpenHealthy(ctx, true)
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			defer db.Close()
			if _, err := db.GetProfile(ctx, "doomed"); err != nil {
				t.Fatalf("Profile was partially deleted: %v", err)
			}
			for _, credentialID := range []string{"agy-exclusive", "unsupported"} {
				if _, err := db.GetProviderCredential(ctx, credentialID); err != nil {
					t.Fatalf("credential %q was partially deleted: %v", credentialID, err)
				}
			}
			if bindings, err := db.ListProfileCredentialBindings(ctx, "doomed", ""); err != nil || len(bindings) != 2 {
				t.Fatalf("bindings after rollback = %#v, err=%v", bindings, err)
			}
		})
	}
}

func testCredential(id, providerID, kind, payload string) store.UpsertProviderCredentialParams {
	return store.UpsertProviderCredentialParams{
		ID: id, ProviderID: providerID, CredentialKind: kind,
		PayloadJSON: payload, PayloadSHA256: testSHA256(payload), MetadataJSON: `{}`,
	}
}

func testSHA256(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func assertApplicationErrorDetail(t *testing.T, err error, code apperror.Code, key string, want any) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code || appErr.Details[key] != want {
		t.Fatalf("error = %v, want code %q with %s=%v", err, code, key, want)
	}
}
