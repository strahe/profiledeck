package app

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/store"
)

type ListActiveProviderStatesRequest struct {
	ConfigDir string
}

type ActiveProviderState struct {
	ProviderID       string `json:"provider_id"`
	ProviderName     string `json:"provider_name"`
	ProfileID        string `json:"profile_id"`
	ProfileName      string `json:"profile_name"`
	OperationID      string `json:"operation_id"`
	UpdatedAtUnixMS  int64  `json:"updated_at_unix_ms"`
	ProfileAvailable bool   `json:"profile_available"`
}

func ListActiveProviderStates(ctx context.Context, req ListActiveProviderStatesRequest) ([]ActiveProviderState, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	providers, err := db.ListProviders(ctx, true)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list providers", err)
	}

	result := make([]ActiveProviderState, 0, len(providers))
	for _, provider := range providers {
		activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, provider.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, WrapError(ErrorStoreStatusFailed, "failed to read active provider state", err).
				WithDetail("provider_id", provider.ID)
		}

		state := ActiveProviderState{
			ProviderID:      provider.ID,
			ProviderName:    provider.Name,
			ProfileID:       activeState.ProfileID,
			OperationID:     activeState.OperationID,
			UpdatedAtUnixMS: activeState.UpdatedAtUnixMS,
		}
		profile, err := db.GetProfile(ctx, activeState.ProfileID)
		if errors.Is(err, store.ErrNotFound) {
			state.ProfileAvailable = false
		} else if err != nil {
			return nil, WrapError(ErrorStoreStatusFailed, "failed to read active profile", err).
				WithDetail("profile_id", activeState.ProfileID)
		} else {
			state.ProfileName = profile.Name
			state.ProfileAvailable = true
		}
		result = append(result, state)
	}
	return result, nil
}
