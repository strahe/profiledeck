package grokbuild

import (
	"context"
	"errors"

	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/usage"
)

type UpdateSettingsRequest struct {
	UsageSyncIntervalSeconds *int `json:"usage_sync_interval_seconds,omitempty"`
}

type Settings struct {
	UsageSyncIntervalSeconds int `json:"usage_sync_interval_seconds"`
}

func (service *Service) GetSettings(ctx context.Context) (Settings, error) {
	if err := service.requireAccess(ctx); err != nil {
		return Settings{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return Settings{}, err
	}
	defer db.Close()
	if _, err := db.GetProvider(ctx, grokconfig.ProviderID); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		return Settings{}, err
	}
	return getSettings(ctx, db)
}

func (service *Service) UpdateSettings(
	ctx context.Context,
	req UpdateSettingsRequest,
) (Settings, error) {
	if err := service.requireAccess(ctx); err != nil {
		return Settings{}, err
	}
	db, err := service.openStore(ctx, false)
	if err != nil {
		return Settings{}, err
	}
	defer db.Close()
	var result Settings
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		if _, err := requireProvider(ctx, txStore); err != nil {
			return err
		}
		if req.UsageSyncIntervalSeconds != nil {
			interval, appErr := usage.NormalizeUsageSyncInterval(
				*req.UsageSyncIntervalSeconds,
			)
			if appErr != nil {
				return appErr
			}
			if err := usage.SaveProviderSyncSettings(
				ctx,
				txStore,
				grokconfig.ProviderID,
				"Grok Build",
				usage.ProviderSyncSettings{UsageSyncIntervalSeconds: interval},
			); err != nil {
				return err
			}
		}
		var err error
		result, err = getSettings(ctx, txStore)
		return err
	})
	return result, err
}

func getSettings(ctx context.Context, db *store.Store) (Settings, error) {
	value, err := usage.LoadProviderSyncSettings(
		ctx,
		db,
		grokconfig.ProviderID,
		"Grok Build",
	)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		UsageSyncIntervalSeconds: value.UsageSyncIntervalSeconds,
	}, nil
}
