package antigravity

import (
	"context"
	"errors"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type AntigravityDetectResult struct {
	ProviderID             string   `json:"provider_id"`
	AdapterID              string   `json:"adapter_id"`
	CredentialStatus       string   `json:"credential_status"`
	ProfileDeckInitialized bool     `json:"profiledeck_initialized"`
	ProviderExists         bool     `json:"provider_exists"`
	ProviderCompatible     bool     `json:"provider_compatible"`
	Warnings               []string `json:"warnings"`
}

func (service *Service) Detect(ctx context.Context) (AntigravityDetectResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityDetectResult{}, err
	}
	result := AntigravityDetectResult{
		ProviderID: agyconfig.ProviderID, AdapterID: agyconfig.AdapterID,
		CredentialStatus: "missing", ProviderCompatible: true, Warnings: []string{},
	}
	status, err := service.runtime.Status(ctx)
	if err != nil {
		return AntigravityDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	if result.ProfileDeckInitialized {
		db, err := service.stores.OpenHealthy(ctx, true)
		if err != nil {
			return AntigravityDetectResult{}, err
		}
		defer db.Close()
		provider, providerErr := db.GetProvider(ctx, agyconfig.ProviderID)
		switch {
		case providerErr == nil:
			result.ProviderExists = true
			if err := providerEnabled(provider); err != nil {
				return AntigravityDetectResult{}, err
			}
			if err := validateAntigravityProvider(provider); err != nil {
				result.ProviderCompatible = false
				result.Warnings = append(result.Warnings, "Existing Antigravity profiles are not compatible with agy v2")
				return result, nil
			}
		case !errors.Is(providerErr, store.ErrNotFound):
			return AntigravityDetectResult{}, mapProviderStoreError(providerErr)
		}
	}
	backend, ok := service.targets.Backend(switchtarget.BackendKeyring)
	if !ok {
		return AntigravityDetectResult{}, apperror.New(apperror.TargetReadFailed, "Antigravity credential backend is unavailable")
	}
	snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
	if err != nil {
		result.CredentialStatus = "unavailable"
		result.Warnings = append(result.Warnings, "Antigravity login could not be read")
	} else if snapshot.Exists {
		if _, _, err := agyauth.Normalize([]byte(snapshot.Content)); err != nil {
			result.CredentialStatus = "invalid"
			result.Warnings = append(result.Warnings, "Antigravity login is not compatible with agy v2")
		} else {
			result.CredentialStatus = "valid"
		}
	}
	return result, nil
}
