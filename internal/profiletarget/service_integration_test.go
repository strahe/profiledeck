package profiletarget_test

import (
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type profileTargetTestEnvironment struct {
	runtime   *profilesruntime.Service
	providers *provider.Service
	profiles  *profile.Service
	targets   *profiletarget.Service
	switching *switching.Service
}

func newProfileTargetTestEnvironment(t *testing.T, configDir string) *profileTargetTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	agentRegistry := agent.BuiltinRegistry()
	agentService := agent.NewService(agentRegistry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	dependencies := switching.NewDependencies(
		switchtarget.MustRegistry(switchtarget.FileBackend{}),
		switchplan.MustRegistry(switchplan.GenericAdapter{}),
	)
	switchingService := switching.NewService(runtimeService.Paths(), runtimeService.StoreFactory(), agentService, dependencies)
	return &profileTargetTestEnvironment{
		runtime:   runtimeService,
		providers: provider.NewService(runtimeService.StoreFactory(), switchingService, agentRegistry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), switchingService, profile.DeleteRegistry{}),
		targets: profiletarget.NewService(
			runtimeService.StoreFactory(), switchingService, agentService, agentRegistry, loadTestManagedPaths,
		),
		switching: switchingService,
	}
}
