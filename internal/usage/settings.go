package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

const UsageSyncIntervalDefault = 15

type ProviderSyncSettings struct {
	UsageSyncIntervalSeconds int `json:"usage_sync_interval_seconds"`
}

func NormalizeUsageSyncInterval(value int) (int, *apperror.Error) {
	switch value {
	case 5, 15, 30, 60:
		return value, nil
	default:
		return 0, apperror.New(
			apperror.SettingInvalid,
			"Unsupported usage sync interval",
		).WithDetail("usage_sync_interval_seconds", value)
	}
}

func LoadProviderSyncSettings(
	ctx context.Context,
	db *store.Store,
	providerID string,
	providerName string,
) (ProviderSyncSettings, error) {
	setting, err := db.GetProviderSetting(ctx, strings.TrimSpace(providerID))
	if errors.Is(err, store.ErrNotFound) {
		return ProviderSyncSettings{
			UsageSyncIntervalSeconds: UsageSyncIntervalDefault,
		}, nil
	}
	if err != nil {
		return ProviderSyncSettings{}, apperror.Wrap(
			apperror.StoreStatusFailed,
			"Failed to load "+providerName+" usage sync settings",
			err,
		)
	}
	if setting.SchemaVersion != store.ProviderSettingsSchemaVersion {
		return ProviderSyncSettings{}, apperror.New(
			apperror.SettingInvalid,
			providerName+" settings version is unsupported",
		)
	}
	var payload ProviderSyncSettings
	if err := decodeProviderSyncSettings(setting.SettingsJSON, &payload); err != nil {
		return ProviderSyncSettings{}, apperror.Wrap(
			apperror.SettingInvalid,
			providerName+" usage sync interval is invalid",
			err,
		)
	}
	interval, appErr := NormalizeUsageSyncInterval(payload.UsageSyncIntervalSeconds)
	if appErr != nil {
		return ProviderSyncSettings{}, appErr
	}
	payload.UsageSyncIntervalSeconds = interval
	return payload, nil
}

func SaveProviderSyncSettings(
	ctx context.Context,
	db *store.Store,
	providerID string,
	providerName string,
	value ProviderSyncSettings,
) error {
	interval, appErr := NormalizeUsageSyncInterval(value.UsageSyncIntervalSeconds)
	if appErr != nil {
		return appErr
	}
	value.UsageSyncIntervalSeconds = interval
	raw, err := json.Marshal(value)
	if err != nil {
		return apperror.Wrap(
			apperror.SettingInvalid,
			"Failed to encode "+providerName+" settings",
			err,
		)
	}
	if _, err := db.UpsertProviderSetting(ctx, store.UpsertProviderSettingParams{
		ProviderID:    strings.TrimSpace(providerID),
		SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON:  string(raw),
	}); err != nil {
		return apperror.Wrap(
			apperror.StoreStatusFailed,
			"Failed to save "+providerName+" settings",
			err,
		)
	}
	return nil
}

func decodeProviderSyncSettings(raw string, value any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("settings contain extra JSON data")
	}
	return nil
}
