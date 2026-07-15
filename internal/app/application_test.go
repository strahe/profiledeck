package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/switching"
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
}

func TestApplicationCoordinatesGenericProfileSwitchAcrossServices(t *testing.T) {
	ctx := context.Background()
	application, err := New(Config{ConfigDir: t.TempDir(), AgentAccess: agent.AccessUnrestricted})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	if _, err := application.Runtime().Init(ctx); err != nil {
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
	if _, err := application.Runtime().Init(ctx); err != nil {
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
