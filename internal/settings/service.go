// Package settings owns Desktop presentation settings.
package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
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
	desktopAutomaticUpdatesSettingKey = "desktop.automatic_updates"
	desktopAutomaticBackupsSettingKey = "desktop.automatic_backups"
)

type UpdateRequest struct {
	Language         *string `json:"language,omitempty"`
	Appearance       *string `json:"appearance,omitempty"`
	SidebarCollapsed *bool   `json:"sidebar_collapsed,omitempty"`
}

type Desktop struct {
	Language         string `json:"language"`
	Appearance       string `json:"appearance"`
	SidebarCollapsed bool   `json:"sidebar_collapsed"`
	AutomaticUpdates bool   `json:"automatic_updates"`
	AutomaticBackups bool   `json:"automatic_backups"`
}

type Service struct {
	stores store.Factory
}

func NewService(stores store.Factory) *Service {
	return &Service{stores: stores}
}

func (service *Service) Get(ctx context.Context) (Desktop, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Desktop{}, err
	}
	defer db.Close()
	return get(ctx, db)
}

func (service *Service) Update(ctx context.Context, req UpdateRequest) (Desktop, error) {
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return Desktop{}, err
	}
	defer db.Close()
	var updated Desktop
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		current, err := get(ctx, tx)
		if err != nil {
			return err
		}
		if req.Language != nil {
			current.Language, err = normalizeLanguage(*req.Language)
			if err != nil {
				return err
			}
		}
		if req.Appearance != nil {
			current.Appearance, err = normalizeAppearance(*req.Appearance)
			if err != nil {
				return err
			}
		}
		if req.SidebarCollapsed != nil {
			current.SidebarCollapsed = *req.SidebarCollapsed
		}
		if req.Language != nil {
			if err := upsert(ctx, tx, desktopLanguageSettingKey, current.Language); err != nil {
				return err
			}
		}
		if req.Appearance != nil {
			if err := upsert(ctx, tx, desktopAppearanceSettingKey, current.Appearance); err != nil {
				return err
			}
		}
		if req.SidebarCollapsed != nil {
			if err := upsert(ctx, tx, desktopSidebarCollapsedSettingKey, current.SidebarCollapsed); err != nil {
				return err
			}
		}
		updated = current
		return nil
	})
	return updated, err
}

// SetAutomaticUpdates is intentionally separate from UpdateRequest so only
// the Desktop update runtime can persist this value and synchronise its timer.
func (service *Service) SetAutomaticUpdates(ctx context.Context, enabled bool) (Desktop, error) {
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return Desktop{}, err
	}
	defer db.Close()
	if err := upsert(ctx, db, desktopAutomaticUpdatesSettingKey, enabled); err != nil {
		return Desktop{}, err
	}
	return get(ctx, db)
}

// SetAutomaticBackups is separate from UpdateRequest so the Desktop backup
// runtime can synchronize the persisted preference with its scheduler.
func (service *Service) SetAutomaticBackups(ctx context.Context, enabled bool) (Desktop, error) {
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return Desktop{}, err
	}
	defer db.Close()
	if err := upsert(ctx, db, desktopAutomaticBackupsSettingKey, enabled); err != nil {
		return Desktop{}, err
	}
	return get(ctx, db)
}

func get(ctx context.Context, db *store.Store) (Desktop, error) {
	language, err := readString(ctx, db, desktopLanguageSettingKey, DesktopLanguageAuto, normalizeLanguage, "desktop language")
	if err != nil {
		return Desktop{}, err
	}
	appearance, err := readString(ctx, db, desktopAppearanceSettingKey, DesktopAppearanceSystem, normalizeAppearance, "desktop appearance")
	if err != nil {
		return Desktop{}, err
	}
	collapsed, err := readBool(ctx, db, desktopSidebarCollapsedSettingKey, false, "desktop sidebar")
	if err != nil {
		return Desktop{}, err
	}
	automaticUpdates, err := readBool(ctx, db, desktopAutomaticUpdatesSettingKey, true, "automatic updates")
	if err != nil {
		return Desktop{}, err
	}
	automaticBackups, err := readBool(ctx, db, desktopAutomaticBackupsSettingKey, true, "automatic backups")
	if err != nil {
		return Desktop{}, err
	}
	return Desktop{
		Language: language, Appearance: appearance, SidebarCollapsed: collapsed,
		AutomaticUpdates: automaticUpdates, AutomaticBackups: automaticBackups,
	}, nil
}

func readString(ctx context.Context, db *store.Store, key, fallback string, normalize func(string) (string, error), label string) (string, error) {
	setting, err := db.GetSetting(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return fallback, nil
	}
	if err != nil {
		return "", apperror.Wrap(apperror.StoreStatusFailed, "failed to load Desktop settings", err)
	}
	var value string
	if err := json.Unmarshal([]byte(setting.ValueJSON), &value); err != nil {
		return "", apperror.Wrap(apperror.SettingInvalid, label+" setting is invalid", err)
	}
	return normalize(value)
}

func readBool(ctx context.Context, db *store.Store, key string, fallback bool, label string) (bool, error) {
	setting, err := db.GetSetting(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return fallback, nil
	}
	if err != nil {
		return false, apperror.Wrap(apperror.StoreStatusFailed, "failed to load Desktop settings", err)
	}
	var value bool
	if err := json.Unmarshal([]byte(setting.ValueJSON), &value); err != nil {
		return false, apperror.Wrap(apperror.SettingInvalid, label+" setting is invalid", err)
	}
	return value, nil
}

func normalizeLanguage(value string) (string, error) {
	switch value {
	case "", DesktopLanguageAuto:
		return DesktopLanguageAuto, nil
	case DesktopLanguageZhCN, DesktopLanguageEnUS:
		return value, nil
	default:
		return "", apperror.New(apperror.SettingInvalid, "unsupported Desktop language").WithDetail("language", value)
	}
}

func normalizeAppearance(value string) (string, error) {
	switch value {
	case "", DesktopAppearanceSystem:
		return DesktopAppearanceSystem, nil
	case DesktopAppearanceLight, DesktopAppearanceDark:
		return value, nil
	default:
		return "", apperror.New(apperror.SettingInvalid, "unsupported Desktop appearance").WithDetail("appearance", value)
	}
}

func upsert(ctx context.Context, db *store.Store, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return apperror.Wrap(apperror.SettingInvalid, "failed to encode Desktop setting", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: key, ValueJSON: string(raw)}); err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Desktop setting", err)
	}
	return nil
}
