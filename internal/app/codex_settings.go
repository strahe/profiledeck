package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	CodexUsageSyncIntervalDefault = 15

	codexUsageSyncIntervalSettingKey         = "codex.usage_sync_interval_seconds"
	legacyDesktopUsageSyncIntervalSettingKey = "desktop.usage_sync_interval_seconds"
)

type CodexSettingsRequest struct {
	ConfigDir string
}

type UpdateCodexSettingsRequest struct {
	ConfigDir                   string `json:"config_dir"`
	ProfileID                   string `json:"profile_id,omitempty"`
	UsageSyncIntervalSeconds    *int   `json:"usage_sync_interval_seconds,omitempty"`
	QuotaRefreshIntervalSeconds *int   `json:"quota_refresh_interval_seconds,omitempty"`
	AuthKeepaliveEnabled        *bool  `json:"auth_keepalive_enabled,omitempty"`
}

type CodexSettings struct {
	UsageSyncIntervalSeconds int                    `json:"usage_sync_interval_seconds"`
	Profiles                 []CodexProfileSettings `json:"profiles"`
}

type CodexProfileSettings struct {
	ProfileID                   string `json:"profile_id"`
	ProfileName                 string `json:"profile_name"`
	CredentialReferenceCount    int    `json:"credential_reference_count"`
	QuotaRefreshIntervalSeconds int    `json:"quota_refresh_interval_seconds"`
	AuthKeepaliveEnabled        bool   `json:"auth_keepalive_enabled"`
	AuthMode                    string `json:"auth_mode"`
	QuotaSupported              bool   `json:"quota_supported"`
	AuthKeepaliveSupported      bool   `json:"auth_keepalive_supported"`
	UpdatedAtUnixMS             int64  `json:"updated_at_unix_ms"`
}

type CodexAutomationTarget struct {
	ProfileID                   string
	CredentialID                string
	CredentialSHA256            string
	QuotaRefreshIntervalSeconds int
	AuthKeepaliveEnabled        bool
	AuthMode                    codexauth.Mode
	QuotaSupported              bool
	AuthKeepaliveSupported      bool
	AuthRefreshDueAtUnixMS      int64
}

func GetCodexSettings(ctx context.Context, req CodexSettingsRequest) (CodexSettings, error) {
	// The first read performs the one-time legacy key migration. This is local
	// application state only and does not touch Codex-owned files.
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexSettings{}, err
	}
	defer db.Close()
	var result CodexSettings
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		var txErr error
		result, txErr = getCodexSettings(ctx, txStore, true)
		return txErr
	})
	return result, err
}

func UpdateCodexSettings(ctx context.Context, req UpdateCodexSettingsRequest) (CodexSettings, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexSettings{}, err
	}
	defer db.Close()

	var result CodexSettings
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		current, err := getCodexSettings(ctx, txStore, false)
		if err != nil {
			return err
		}
		if req.UsageSyncIntervalSeconds != nil {
			interval, appErr := normalizeCodexUsageSyncInterval(*req.UsageSyncIntervalSeconds)
			if appErr != nil {
				return appErr
			}
			if err := upsertDesktopSetting(ctx, txStore, codexUsageSyncIntervalSettingKey, interval); err != nil {
				return err
			}
			if err := txStore.DeleteSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey); err != nil && !errors.Is(err, store.ErrNotFound) {
				return WrapError(ErrorStoreStatusFailed, "failed to remove migrated Codex usage sync setting", err)
			}
		}

		profileID := strings.TrimSpace(req.ProfileID)
		if req.QuotaRefreshIntervalSeconds != nil || req.AuthKeepaliveEnabled != nil {
			if profileID == "" {
				return NewError(ErrorProfileInvalid, "Codex profile id is required")
			}
			var profileSettings *CodexProfileSettings
			for i := range current.Profiles {
				if current.Profiles[i].ProfileID == profileID {
					profileSettings = &current.Profiles[i]
					break
				}
			}
			if profileSettings == nil {
				return NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
			}
			interval := profileSettings.QuotaRefreshIntervalSeconds
			if req.QuotaRefreshIntervalSeconds != nil {
				var appErr *AppError
				interval, appErr = normalizeCodexQuotaRefreshInterval(*req.QuotaRefreshIntervalSeconds)
				if appErr != nil {
					return appErr
				}
			}
			if interval > 0 && !profileSettings.QuotaSupported {
				return NewError(ErrorSettingInvalid, "Codex quota refresh is unsupported for this login mode").WithDetail("profile_id", profileID)
			}
			keepalive := profileSettings.AuthKeepaliveEnabled
			if req.AuthKeepaliveEnabled != nil {
				keepalive = *req.AuthKeepaliveEnabled
			}
			if keepalive && !profileSettings.AuthKeepaliveSupported {
				return NewError(ErrorSettingInvalid, "Codex auth keepalive is unsupported for this login mode").WithDetail("profile_id", profileID)
			}
			if _, err := txStore.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
				ProfileID: profileID, ProviderID: codexconfig.ProviderID,
				QuotaRefreshIntervalSeconds: interval, AuthKeepaliveEnabled: keepalive,
			}); err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to save Codex profile settings", err)
			}
		}

		result, err = getCodexSettings(ctx, txStore, false)
		return err
	})
	if err != nil {
		return CodexSettings{}, err
	}
	return result, nil
}

func ListCodexAutomationTargets(ctx context.Context, req CodexSettingsRequest) ([]CodexAutomationTarget, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	settings, err := getCodexSettings(ctx, db, false)
	if err != nil {
		return nil, err
	}
	byProfile := make(map[string]CodexProfileSettings, len(settings.Profiles))
	for _, value := range settings.Profiles {
		byProfile[value.ProfileID] = value
	}
	summaries, err := listCodexProfileSummaries(ctx, db)
	if err != nil {
		return nil, err
	}
	result := make([]CodexAutomationTarget, 0, len(summaries))
	now := time.Now().UTC()
	for _, summary := range summaries {
		setting := byProfile[summary.Profile.ID]
		target := CodexAutomationTarget{
			ProfileID: summary.Profile.ID, CredentialID: summary.CredentialID,
			QuotaRefreshIntervalSeconds: setting.QuotaRefreshIntervalSeconds,
			AuthKeepaliveEnabled:        setting.AuthKeepaliveEnabled,
		}
		if summary.CredentialID != "" {
			credential, err := requireCodexAuthCredential(ctx, db, summary.CredentialID)
			if err == nil {
				target.CredentialSHA256 = credential.PayloadSHA256
				if info, inspectErr := codexauth.Inspect([]byte(credential.PayloadJSON)); inspectErr == nil {
					target.AuthMode = info.Mode
					target.QuotaSupported = info.QuotaSupported
					target.AuthKeepaliveSupported = info.RefreshSupported
					if dueAt, ok := info.RefreshDueAt(now); ok {
						target.AuthRefreshDueAtUnixMS = dueAt.UnixMilli()
					}
				}
			}
		}
		result = append(result, target)
	}
	return result, nil
}

func getCodexSettings(ctx context.Context, db *store.Store, migrateLegacy bool) (CodexSettings, error) {
	interval, legacy, err := getCodexUsageSyncInterval(ctx, db)
	if err != nil {
		return CodexSettings{}, err
	}
	if migrateLegacy && legacy {
		if err := upsertDesktopSetting(ctx, db, codexUsageSyncIntervalSettingKey, interval); err != nil {
			return CodexSettings{}, err
		}
		if err := db.DeleteSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey); err != nil && !errors.Is(err, store.ErrNotFound) {
			return CodexSettings{}, WrapError(ErrorStoreStatusFailed, "failed to remove migrated Codex usage sync setting", err)
		}
	}
	summaries, err := listCodexProfileSummaries(ctx, db)
	if err != nil {
		return CodexSettings{}, err
	}
	stored, err := db.ListProviderProfileSettings(ctx, codexconfig.ProviderID)
	if err != nil {
		return CodexSettings{}, WrapError(ErrorStoreStatusFailed, "failed to load Codex profile settings", err)
	}
	storedByProfile := make(map[string]store.ProviderProfileSetting, len(stored))
	for _, value := range stored {
		storedByProfile[value.ProfileID] = value
	}
	references := make(map[string]int)
	for _, summary := range summaries {
		if summary.CredentialID != "" {
			references[summary.CredentialID]++
		}
	}
	credentials := make(map[string]store.ProviderCredential)
	result := CodexSettings{UsageSyncIntervalSeconds: interval, Profiles: make([]CodexProfileSettings, 0, len(summaries))}
	for _, summary := range summaries {
		value := CodexProfileSettings{
			ProfileID: summary.Profile.ID, ProfileName: summary.Profile.Name,
			CredentialReferenceCount: references[summary.CredentialID],
		}
		if setting, ok := storedByProfile[summary.Profile.ID]; ok {
			value.QuotaRefreshIntervalSeconds = setting.QuotaRefreshIntervalSeconds
			value.AuthKeepaliveEnabled = setting.AuthKeepaliveEnabled
			value.UpdatedAtUnixMS = setting.UpdatedAtUnixMS
		}
		if summary.CredentialID != "" {
			credential, ok := credentials[summary.CredentialID]
			var credentialErr error
			if !ok {
				credential, credentialErr = requireCodexAuthCredential(ctx, db, summary.CredentialID)
				if credentialErr == nil {
					credentials[summary.CredentialID] = credential
				}
			}
			if credentialErr == nil {
				if info, inspectErr := codexauth.Inspect([]byte(credential.PayloadJSON)); inspectErr == nil {
					value.AuthMode = string(info.Mode)
					value.QuotaSupported = info.QuotaSupported
					value.AuthKeepaliveSupported = info.RefreshSupported
				}
			}
		}
		if value.AuthMode == "" {
			value.AuthMode = string(codexauth.ModeUnsupported)
		}
		result.Profiles = append(result.Profiles, value)
	}
	sort.Slice(result.Profiles, func(i, j int) bool { return result.Profiles[i].ProfileID < result.Profiles[j].ProfileID })
	return result, nil
}

func getCodexUsageSyncInterval(ctx context.Context, db *store.Store) (int, bool, error) {
	setting, err := db.GetSetting(ctx, codexUsageSyncIntervalSettingKey)
	legacy := false
	if errors.Is(err, store.ErrNotFound) {
		setting, err = db.GetSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey)
		legacy = err == nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return CodexUsageSyncIntervalDefault, false, nil
	}
	if err != nil {
		return 0, false, WrapError(ErrorStoreStatusFailed, "failed to load Codex usage sync interval", err)
	}
	var interval int
	if err := json.Unmarshal([]byte(setting.ValueJSON), &interval); err != nil {
		return 0, false, WrapError(ErrorSettingInvalid, "Codex usage sync interval is invalid", err)
	}
	normalized, appErr := normalizeCodexUsageSyncInterval(interval)
	if appErr != nil {
		return 0, false, appErr
	}
	return normalized, legacy, nil
}

func normalizeCodexUsageSyncInterval(value int) (int, *AppError) {
	switch value {
	case 5, 15, 30, 60:
		return value, nil
	default:
		return 0, NewError(ErrorSettingInvalid, "unsupported Codex usage sync interval").WithDetail("usage_sync_interval_seconds", value)
	}
}

func normalizeCodexQuotaRefreshInterval(value int) (int, *AppError) {
	switch value {
	case 0, 300, 600, 1800, 3600:
		return value, nil
	default:
		return 0, NewError(ErrorSettingInvalid, "unsupported Codex quota refresh interval").WithDetail("quota_refresh_interval_seconds", value)
	}
}
