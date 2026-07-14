package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func TestRegistryRejectsProviderOwnershipConflict(t *testing.T) {
	_, err := NewRegistry(
		Manifest{ID: "first", DisplayName: "First", ProviderIDs: []string{"shared"}},
		Manifest{ID: "second", DisplayName: "Second", ProviderIDs: []string{"shared"}},
	)
	if err == nil {
		t.Fatal("expected duplicate Provider ownership to fail")
	}
}

func TestRegistryReturnsImmutableManifestCopies(t *testing.T) {
	registry := MustRegistry(Manifest{ID: "sample", DisplayName: "Sample", ProviderIDs: []string{"provider"}})
	manifests := registry.Manifests()
	manifests[0].ProviderIDs[0] = "changed"
	manifest, ok := registry.Manifest("sample")
	if !ok || len(manifest.ProviderIDs) != 1 || manifest.ProviderIDs[0] != "provider" {
		t.Fatalf("registry was mutated through returned manifest: %#v", manifest)
	}
}

func TestUnrestrictedPolicyDoesNotReadDesktopPreferences(t *testing.T) {
	service := NewService(BuiltinRegistry(), store.NewFactory(filepath.Join(t.TempDir(), "missing.db")), AccessUnrestricted)
	states, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("unrestricted Agent list should not open the database: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("expected three built-in Agents, got %d", len(states))
	}
	for _, state := range states {
		if !state.Enabled {
			t.Fatalf("unrestricted Agent %q should be enabled", state.Manifest.ID)
		}
	}
	if err := service.RequireProvider(context.Background(), "codex"); err != nil {
		t.Fatalf("unrestricted managed Provider should not read Desktop preferences: %v", err)
	}
}

func TestDesktopPolicyUsesDefaultsAndIndependentOverrides(t *testing.T) {
	ctx := context.Background()
	factory := migratedFactory(t, ctx)
	service := NewService(BuiltinRegistry(), factory, AccessDesktopPreferences)

	states, err := service.List(ctx)
	if err != nil {
		t.Fatalf("list default Agent states: %v", err)
	}
	if !allEnabled(states) {
		t.Fatalf("built-in Agents should default to enabled: %#v", states)
	}
	if _, err := service.SetEnabled(ctx, Codex, false); err != nil {
		t.Fatalf("disable Codex: %v", err)
	}
	if _, err := service.SetEnabled(ctx, ClaudeCode, false); err != nil {
		t.Fatalf("disable Claude Code: %v", err)
	}
	if _, err := service.SetEnabled(ctx, Codex, true); err != nil {
		t.Fatalf("re-enable Codex independently: %v", err)
	}

	states, err = service.List(ctx)
	if err != nil {
		t.Fatalf("list overridden Agent states: %v", err)
	}
	assertEnabled(t, states, Codex, true)
	assertEnabled(t, states, Antigravity, true)
	assertEnabled(t, states, ClaudeCode, false)
	if err := service.RequireAgent(ctx, ClaudeCode); !isCode(err, apperror.AgentDisabled) {
		t.Fatalf("expected disabled Agent error, got %v", err)
	}
	if err := service.RequireProvider(ctx, "claude-code"); !isCode(err, apperror.AgentDisabled) {
		t.Fatalf("expected managed Provider gate, got %v", err)
	}
	if err := service.RequireProvider(ctx, "custom"); err != nil {
		t.Fatalf("generic Provider must not have an Agent gate: %v", err)
	}
}

func TestUnknownDesktopAgentSettingIsIgnored(t *testing.T) {
	ctx := context.Background()
	factory := migratedFactory(t, ctx)
	db, err := factory.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "desktop.agent.future.enabled", ValueJSON: "false"}); err != nil {
		t.Fatalf("save future Agent setting: %v", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "desktop.agent.codex.unknown", ValueJSON: "false"}); err != nil {
		t.Fatalf("save unrelated Agent setting: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	states, err := NewService(BuiltinRegistry(), factory, AccessDesktopPreferences).List(ctx)
	if err != nil {
		t.Fatalf("list Agent states: %v", err)
	}
	if !allEnabled(states) {
		t.Fatalf("unknown settings must not affect built-in Agents: %#v", states)
	}
}

func TestSetEnabledPublishesStateEventAfterPersistence(t *testing.T) {
	ctx := context.Background()
	service := NewService(BuiltinRegistry(), migratedFactory(t, ctx), AccessDesktopPreferences)
	events := make(chan StateEvent, 1)
	unsubscribe := service.Subscribe(func(event StateEvent) { events <- event })
	defer unsubscribe()

	if _, err := service.SetEnabled(ctx, Antigravity, false); err != nil {
		t.Fatalf("disable Antigravity: %v", err)
	}
	select {
	case event := <-events:
		if event.ID != Antigravity || event.Enabled {
			t.Fatalf("unexpected Agent state event: %#v", event)
		}
	default:
		t.Fatal("expected Agent state event")
	}
}

func migratedFactory(t *testing.T, ctx context.Context) store.Factory {
	t.Helper()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("migrate database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	return factory
}

func allEnabled(states []State) bool {
	for _, state := range states {
		if !state.Enabled {
			return false
		}
	}
	return true
}

func assertEnabled(t *testing.T, states []State, id ID, expected bool) {
	t.Helper()
	for _, state := range states {
		if state.Manifest.ID == id {
			if state.Enabled != expected {
				t.Fatalf("Agent %q enabled=%t, want %t", id, state.Enabled, expected)
			}
			return
		}
	}
	t.Fatalf("Agent %q was not listed", id)
}

func isCode(err error, code apperror.Code) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Code == code
}
