package grokbuild

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokquota "github.com/strahe/profiledeck/internal/grokbuild/quota"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

// Service owns Grok Build profile and Config Set use cases.
type Service struct {
	runtime     *runtime.Service
	stores      store.Factory
	maintenance maintenance.Runner
	sharedLock  maintenance.SharedLockRunner
	policy      agent.Policy
	grokHome    string
	quotaReader grokquota.Reader
}

func NewService(
	runtimeService *runtime.Service,
	maintenanceRunner maintenance.Runner,
	sharedLockRunner maintenance.SharedLockRunner,
	policy agent.Policy,
	grokHome string,
) *Service {
	return &Service{
		runtime: runtimeService, stores: runtimeService.StoreFactory(),
		maintenance: maintenanceRunner, sharedLock: sharedLockRunner, policy: policy,
		grokHome: grokHome, quotaReader: grokquota.NewACPClient(),
	}
}

func (service *Service) requireAccess(ctx context.Context) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireAgent(ctx, agent.GrokBuild)
}

func (service *Service) openStore(ctx context.Context, readOnly bool) (*store.Store, error) {
	return service.stores.OpenHealthy(ctx, readOnly)
}

func (service *Service) resolveHome() (grokconfig.Home, error) {
	home, err := grokconfig.ResolveHome(service.grokHome)
	if err != nil {
		return grokconfig.Home{}, apperror.Wrap(apperror.GrokBuildInvalid, "failed to resolve Grok Build Home", err)
	}
	return home, nil
}

func (service *Service) resolveExistingHome() (grokconfig.Home, error) {
	home, err := service.resolveHome()
	if err != nil {
		return grokconfig.Home{}, err
	}
	info, err := os.Stat(home.Dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return grokconfig.Home{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Home does not exist")
	case err != nil:
		return grokconfig.Home{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Home could not be inspected")
	case !info.IsDir():
		return grokconfig.Home{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Home is not a directory")
	default:
		return home, nil
	}
}

func requireProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	value, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Provider{}, apperror.New(apperror.ProviderNotFound, "Grok Build Provider was not found")
	}
	if err != nil {
		return store.Provider{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Grok Build Provider", err)
	}
	return value, nil
}

func publicProvider(value store.Provider) (provider.Provider, error) {
	return provider.FromStore(value)
}

func publicProfile(value store.Profile) (profile.Profile, error) {
	return profile.FromStore(value)
}

func validateID(raw string, code apperror.Code) (string, *apperror.Error) {
	return validate.ID(raw, code)
}

func validateName(raw string, code apperror.Code) (string, *apperror.Error) {
	return validate.Name(raw, code)
}

func validateDescription(raw string, code apperror.Code) (string, *apperror.Error) {
	return validate.Description(raw, code)
}
