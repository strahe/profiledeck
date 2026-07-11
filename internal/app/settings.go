package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	DesktopLanguageAuto     = "auto"
	DesktopLanguageZhCN     = "zh-CN"
	DesktopLanguageEnUS     = "en-US"
	DesktopAppearanceSystem = "system"
	DesktopAppearanceLight  = "light"
	DesktopAppearanceDark   = "dark"

	desktopLanguageSettingKey         = "desktop.language"
	desktopAppearanceSettingKey       = "desktop.appearance"
	desktopSidebarCollapsedSettingKey = "desktop.sidebar_collapsed"
)

type DesktopSettingsRequest struct {
	ConfigDir string
}

type UpdateDesktopSettingsRequest struct {
	ConfigDir        string  `json:"config_dir"`
	Language         *string `json:"language,omitempty"`
	Appearance       *string `json:"appearance,omitempty"`
	SidebarCollapsed *bool   `json:"sidebar_collapsed,omitempty"`
}

type DesktopSettings struct {
	Language         string `json:"language"`
	Appearance       string `json:"appearance"`
	SidebarCollapsed bool   `json:"sidebar_collapsed"`
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
		if req.Appearance != nil {
			appearance, appErr := normalizeDesktopAppearance(*req.Appearance)
			if appErr != nil {
				return appErr
			}
			current.Appearance = appearance
		}
		if req.SidebarCollapsed != nil {
			current.SidebarCollapsed = *req.SidebarCollapsed
		}
		if req.Language != nil {
			if err := upsertDesktopSetting(ctx, txStore, desktopLanguageSettingKey, current.Language); err != nil {
				return err
			}
		}
		if req.Appearance != nil {
			if err := upsertDesktopSetting(ctx, txStore, desktopAppearanceSettingKey, current.Appearance); err != nil {
				return err
			}
		}
		if req.SidebarCollapsed != nil {
			if err := upsertDesktopSetting(ctx, txStore, desktopSidebarCollapsedSettingKey, current.SidebarCollapsed); err != nil {
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
	appearance, err := getDesktopAppearance(ctx, db)
	if err != nil {
		return DesktopSettings{}, err
	}
	sidebarCollapsed, err := getDesktopSidebarCollapsed(ctx, db)
	if err != nil {
		return DesktopSettings{}, err
	}
	return DesktopSettings{
		Language:         language,
		Appearance:       appearance,
		SidebarCollapsed: sidebarCollapsed,
	}, nil
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

func getDesktopAppearance(ctx context.Context, db *store.Store) (string, error) {
	setting, err := db.GetSetting(ctx, desktopAppearanceSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return DesktopAppearanceSystem, nil
	}
	if err != nil {
		return "", WrapError(ErrorStoreStatusFailed, "failed to load desktop settings", err)
	}

	var appearance string
	if err := json.Unmarshal([]byte(setting.ValueJSON), &appearance); err != nil {
		return "", WrapError(ErrorSettingInvalid, "desktop appearance setting is invalid", err)
	}
	normalized, appErr := normalizeDesktopAppearance(appearance)
	if appErr != nil {
		return "", appErr
	}
	return normalized, nil
}

func normalizeDesktopAppearance(value string) (string, *AppError) {
	switch value {
	case "", DesktopAppearanceSystem:
		return DesktopAppearanceSystem, nil
	case DesktopAppearanceLight, DesktopAppearanceDark:
		return value, nil
	default:
		return "", NewError(ErrorSettingInvalid, "unsupported desktop appearance").WithDetail("appearance", value)
	}
}

func getDesktopSidebarCollapsed(ctx context.Context, db *store.Store) (bool, error) {
	setting, err := db.GetSetting(ctx, desktopSidebarCollapsedSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, WrapError(ErrorStoreStatusFailed, "failed to load desktop settings", err)
	}

	var collapsed bool
	if err := json.Unmarshal([]byte(setting.ValueJSON), &collapsed); err != nil {
		return false, WrapError(ErrorSettingInvalid, "desktop sidebar setting is invalid", err)
	}
	return collapsed, nil
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
