package antigravity

import (
	"context"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	agyadapter "github.com/strahe/profiledeck/internal/antigravity/adapter"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	planActionCreate = "create"
	planActionUpdate = "update"
	planActionNoop   = "noop"
)

type antigravityTestEnvironment struct {
	runtime     *profilesruntime.Service
	antigravity *Service
	providers   *provider.Service
	profiles    *profile.Service
	targets     *profiletarget.Service
	switching   *switching.Service
	doctor      *doctor.Service
}

func newAntigravityTestEnvironment(t *testing.T, configDir string, client switchtarget.KeyringClient) *antigravityTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	agentRegistry := agent.BuiltinRegistry()
	agentService := agent.NewService(agentRegistry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	targetRegistry := switchtarget.MustRegistry(
		switchtarget.FileBackend{}, switchtarget.NewKeyringBackend(client),
	)
	adapterRegistry := switchplan.MustRegistry(switchplan.GenericAdapter{}, agyadapter.Adapter{})
	switchingService := switching.NewService(
		runtimeService.Paths(), runtimeService.StoreFactory(), agentService,
		switching.NewDependencies(targetRegistry, adapterRegistry),
	)
	antigravityService := NewService(
		runtimeService, runtimeService.StoreFactory(), switchingService, switchingService, agentService, targetRegistry,
	)
	doctorService := doctor.NewService(
		runtimeService,
		agentService,
		[]doctor.ProviderCheck{{AgentID: agent.Antigravity, Check: antigravityService.HealthCheck}},
		func(ctx context.Context, db *store.Store, paths profilesruntime.Paths, operation store.Operation) (string, string, string) {
			inspection := switchingService.InspectRecoveryFromOperation(ctx, db, paths, operation)
			return inspection.Status, inspection.Action, inspection.Reason
		},
		nil,
	)
	return &antigravityTestEnvironment{
		runtime: runtimeService, antigravity: antigravityService,
		providers: provider.NewService(runtimeService.StoreFactory(), switchingService, agentRegistry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), switchingService, profile.DeleteRegistry{}),
		targets:   profiletarget.NewService(runtimeService.StoreFactory(), switchingService, agentService, agentRegistry),
		switching: switchingService, doctor: doctorService,
	}
}

func initAntigravityTestRuntime(ctx context.Context, configDir string) (profilesruntime.InitResult, error) {
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

func assertErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected error code %q, got %v", code, err)
	}
}
