package usage

import (
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
)

type usageTestEnvironment struct {
	runtime *profilesruntime.Service
	service *Service
}

func assertAppErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}

func newUsageTestEnvironment(t *testing.T, configDir, codexDir string) *usageTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	registry := agent.BuiltinRegistry()
	policy := agent.NewService(registry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	return &usageTestEnvironment{
		runtime: runtimeService,
		service: NewService(runtimeService.StoreFactory(), codexDir, policy),
	}
}
