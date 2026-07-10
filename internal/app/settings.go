package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	DesktopLanguageAuto             = "auto"
	DesktopLanguageZhCN             = "zh-CN"
	DesktopLanguageEnUS             = "en-US"
	DesktopUsageSyncIntervalDefault = 15

	desktopLanguageSettingKey          = "desktop.language"
	desktopUsageSyncIntervalSettingKey = "desktop.usage_sync_interval_seconds"
)

type DesktopSettingsRequest struct {
	ConfigDir string
}

type UpdateDesktopSettingsRequest struct {
	ConfigDir                string  `json:"config_dir"`
	Language                 *string `json:"language,omitempty"`
	UsageSyncIntervalSeconds *int    `json:"usage_sync_interval_seconds,omitempty"`
}

type DesktopSettings struct {
	Language                 string `json:"language"`
	UsageSyncIntervalSeconds int    `json:"usage_sync_interval_seconds"`
}

func GetDesktopSettings(ctx context.Context, req DesktopSettingsRequest) (DesktopSettings, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return DesktopSettings{}, err
	}
	defer db.Close()

	return getDesktopSettings(ctx, db)
}

func UpdateDesktopSettings(ctx context.Context, req UpdateDesktopSettingsRequest) (DesktopSettings, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return DesktopSettings{}, err
	}
	defer db.Close()

	var updated DesktopSettings
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		current, err := getDesktopSettings(ctx, txStore)
		if err != nil {
			return err
		}
		if req.Language != nil {
			language, appErr := normalizeDesktopLanguage(*req.Language)
			if appErr != nil {
				return appErr
			}
			current.Language = language
		}
		if req.UsageSyncIntervalSeconds != nil {
			interval, appErr := normalizeDesktopUsageSyncInterval(*req.UsageSyncIntervalSeconds)
			if appErr != nil {
				return appErr
			}
			current.UsageSyncIntervalSeconds = interval
		}

		// Partial desktop setting updates share one transaction so independent
		// controls cannot overwrite each other with a stale full-settings value.
		if req.Language != nil {
			if err := upsertDesktopSetting(ctx, txStore, desktopLanguageSettingKey, current.Language); err != nil {
				return err
			}
		}
		if req.UsageSyncIntervalSeconds != nil {
			if err := upsertDesktopSetting(ctx, txStore, desktopUsageSyncIntervalSettingKey, current.UsageSyncIntervalSeconds); err != nil {
				return err
			}
		}
		updated = current
		return nil
	})
	if err != nil {
		return DesktopSettings{}, err
	}
	return updated, nil
}

func getDesktopSettings(ctx context.Context, db *store.Store) (DesktopSettings, error) {
	language, err := getDesktopLanguage(ctx, db)
	if err != nil {
		return DesktopSettings{}, err
	}
	interval, err := getDesktopUsageSyncInterval(ctx, db)
	if err != nil {
		return DesktopSettings{}, err
	}
	return DesktopSettings{Language: language, UsageSyncIntervalSeconds: interval}, nil
}

func getDesktopLanguage(ctx context.Context, db *store.Store) (string, error) {
	setting, err := db.GetSetting(ctx, desktopLanguageSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return DesktopLanguageAuto, nil
	}
	if err != nil {
		return "", WrapError(ErrorStoreStatusFailed, "failed to load desktop settings", err)
	}

	var language string
	if err := json.Unmarshal([]byte(setting.ValueJSON), &language); err != nil {
		return "", WrapError(ErrorSettingInvalid, "desktop language setting is invalid", err)
	}
	normalized, appErr := normalizeDesktopLanguage(language)
	if appErr != nil {
		return "", appErr
	}
	return normalized, nil
}

func normalizeDesktopLanguage(value string) (string, *AppError) {
	switch value {
	case "", DesktopLanguageAuto:
		return DesktopLanguageAuto, nil
	case DesktopLanguageZhCN, DesktopLanguageEnUS:
		return value, nil
	default:
		return "", NewError(ErrorSettingInvalid, "unsupported desktop language").WithDetail("language", value)
	}
}

func getDesktopUsageSyncInterval(ctx context.Context, db *store.Store) (int, error) {
	setting, err := db.GetSetting(ctx, desktopUsageSyncIntervalSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return DesktopUsageSyncIntervalDefault, nil
	}
	if err != nil {
		return 0, WrapError(ErrorStoreStatusFailed, "failed to load desktop usage sync interval", err)
	}

	var interval int
	if err := json.Unmarshal([]byte(setting.ValueJSON), &interval); err != nil {
		return 0, WrapError(ErrorSettingInvalid, "desktop usage sync interval is invalid", err)
	}
	normalized, appErr := normalizeDesktopUsageSyncInterval(interval)
	if appErr != nil {
		return 0, appErr
	}
	return normalized, nil
}

func normalizeDesktopUsageSyncInterval(value int) (int, *AppError) {
	switch value {
	case 5, 15, 30, 60:
		return value, nil
	default:
		return 0, NewError(ErrorSettingInvalid, "unsupported desktop usage sync interval").WithDetail("usage_sync_interval_seconds", value)
	}
}

func upsertDesktopSetting(ctx context.Context, db *store.Store, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return WrapError(ErrorSettingInvalid, "failed to encode desktop setting", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: key, ValueJSON: string(raw)}); err != nil {
		return WrapError(ErrorStoreStatusFailed, "failed to save desktop setting", err)
	}
	return nil
}
