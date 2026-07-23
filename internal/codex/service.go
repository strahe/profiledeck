package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

// Service owns Codex-specific use cases. Mechanism packages below codex remain
// responsible for payload parsing, plan construction, automation, and storage.
type Service struct {
	runtime     *runtime.Service
	stores      store.Factory
	maintenance maintenance.Runner
	sharedLock  maintenance.SharedLockRunner
	policy      agent.Policy
	codexDir    string
}

func NewService(
	runtimeService *runtime.Service,
	maintenanceRunner maintenance.Runner,
	sharedLockRunner maintenance.SharedLockRunner,
	policy agent.Policy,
	codexDir string,
) *Service {
	return &Service{
		runtime: runtimeService, stores: runtimeService.StoreFactory(), maintenance: maintenanceRunner,
		sharedLock: sharedLockRunner, policy: policy, codexDir: codexDir,
	}
}

func (service *Service) requireAccess(ctx context.Context) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireAgent(ctx, agent.Codex)
}

func (service *Service) openStore(ctx context.Context, readOnly bool) (*store.Store, error) {
	return service.stores.OpenHealthy(ctx, readOnly)
}

func (service *Service) resolveHome() (codexconfig.Home, error) {
	home, err := codexconfig.ResolveHome(service.codexDir)
	if err != nil {
		return codexconfig.Home{}, apperror.Wrap(apperror.CodexInvalid, "failed to resolve Codex home", err)
	}
	return home, nil
}

func (service *Service) resolveExistingHome() (codexconfig.Home, error) {
	home, err := service.resolveHome()
	if err != nil {
		return codexconfig.Home{}, err
	}
	if appErr := requireExistingCodexHome(home); appErr != nil {
		return codexconfig.Home{}, appErr
	}
	return home, nil
}

func requireCodexProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	stored, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	return stored, nil
}

func requireCodexProviderIfPresent(ctx context.Context, db *store.Store) (store.Provider, error) {
	stored, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Provider{}, nil
	}
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	return stored, nil
}

func providerFromStore(stored store.Provider) (provider.Provider, error) {
	return provider.FromStore(stored)
}

func profileFromStore(stored store.Profile) (profile.Profile, error) {
	return profile.FromStore(stored)
}

func mapProviderStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Provider already exists")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Profile store operation failed", err)
	}
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

func sha256HexString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
