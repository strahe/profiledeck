package codex

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	codexadapter "github.com/strahe/profiledeck/internal/codex/adapter"
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

type codexTestEnvironment struct {
	runtime   *profilesruntime.Service
	codex     *Service
	providers *provider.Service
	profiles  *profile.Service
	targets   *profiletarget.Service
	switching *switching.Service
}

func newCodexTestEnvironment(t *testing.T, configDir, codexDir string) *codexTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	agentRegistry := agent.BuiltinRegistry()
	agentService := agent.NewService(agentRegistry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	targetRegistry := switchtarget.MustRegistry(switchtarget.FileBackend{})
	adapterRegistry := switchplan.MustRegistry(switchplan.GenericAdapter{}, codexadapter.Adapter{})
	switchingService := switching.NewService(
		runtimeService.Paths(), runtimeService.StoreFactory(), agentService,
		switching.NewDependencies(targetRegistry, adapterRegistry),
	)
	codexService := NewService(
		runtimeService, switchingService, switchingService, agentService, codexDir,
	)
	return &codexTestEnvironment{
		runtime:   runtimeService,
		codex:     codexService,
		providers: provider.NewService(runtimeService.StoreFactory(), switchingService, agentService, agentRegistry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), switchingService),
		targets: profiletarget.NewService(
			runtimeService.StoreFactory(), switchingService, agentService, agentRegistry, codexService.ReservedPaths,
		),
		switching: switchingService,
	}
}

func initCodexTestRuntime(ctx context.Context, configDir string) (profilesruntime.InitResult, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return profilesruntime.InitResult{}, err
	}
	return runtimeService.Init(ctx)
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

func readFileString(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file read to succeed, got %v", err)
	}
	return string(content)
}
