package switching

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type switchingTestEnvironment struct {
	runtime   *profilesruntime.Service
	service   *Service
	providers *provider.Service
	profiles  *profile.Service
	targets   *profiletarget.Service
}

func newSwitchingTestEnvironment(t *testing.T, configDir string) *switchingTestEnvironment {
	return newSwitchingTestEnvironmentWithTargets(t, configDir, switchtarget.MustRegistry(switchtarget.FileBackend{}))
}

func newSwitchingTestEnvironmentWithTargets(t *testing.T, configDir string, targets switchtarget.Registry) *switchingTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	agentRegistry := agent.BuiltinRegistry()
	agentService := agent.NewService(agentRegistry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	dependencies := NewDependencies(
		targets,
		switchplan.MustRegistry(switchplan.GenericAdapter{}),
	)
	service := NewService(runtimeService.Paths(), runtimeService.StoreFactory(), agentService, dependencies)
	return &switchingTestEnvironment{
		runtime:   runtimeService,
		service:   service,
		providers: provider.NewService(runtimeService.StoreFactory(), service, agentService, agentRegistry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), service, profile.DeleteRegistry{}),
		targets:   profiletarget.NewService(runtimeService.StoreFactory(), service, agentService, agentRegistry),
	}
}

func initSwitchingTestRuntime(ctx context.Context, configDir string) (profilesruntime.InitResult, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return profilesruntime.InitResult{}, err
	}
	return bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx)
}

func openHealthyStore(ctx context.Context, configDir string, readOnly bool) (*store.Store, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return nil, err
	}
	return runtimeService.StoreFactory().OpenHealthy(ctx, readOnly)
}

func createGenericProviderAndProfile(t *testing.T, ctx context.Context, configDir string, providerEnabled bool) {
	t.Helper()
	environment := newSwitchingTestEnvironment(t, configDir)
	if _, err := environment.providers.Create(ctx, provider.CreateRequest{
		ID: "provider-a", Name: "Provider A", AdapterID: "generic", Enabled: &providerEnabled,
	}); err != nil {
		t.Fatalf("expected Provider create, got %v", err)
	}
	if _, err := environment.profiles.Create(ctx, profile.CreateRequest{ID: "profile-a", Name: "Profile A"}); err != nil {
		t.Fatalf("expected Profile create, got %v", err)
	}
}

func resolveTestRuntime(configDir string) (string, profilesruntime.Paths, error) {
	return profilesruntime.ResolveConfig(configDir)
}

func contentValueJSON(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		t.Fatalf("expected content JSON, got %v", err)
	}
	return string(raw)
}
