package usage

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

type usageTestEnvironment struct {
	runtime *profilesruntime.Service
	service *Service
}

func usageTestEventKey(value string) store.UsageKey {
	sum := sha256.Sum256([]byte(value))
	return store.UsageKey(sum)
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
	return &usageTestEnvironment{
		runtime: runtimeService,
		service: NewService(
			runtimeService.StoreFactory(),
			MustRegistry(NewCodexIntegration(codexDir)),
		),
	}
}
