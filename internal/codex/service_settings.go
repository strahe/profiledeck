package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexautomation "github.com/strahe/profiledeck/internal/codex/automation"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	CodexUsageSyncIntervalDefault = codexautomation.UsageSyncIntervalDefault

	codexUsageSyncIntervalSettingKey         = "codex.usage_sync_interval_seconds"
	legacyDesktopUsageSyncIntervalSettingKey = "desktop.usage_sync_interval_seconds"
)

type UpdateCodexSettingsRequest struct {
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

func (service *Service) GetSettings(ctx context.Context) (CodexSettings, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexSettings{}, err
	}
	// The first read performs the one-time legacy key migration. This is local
	// application state only and does not touch Codex-owned files.
	db, err := service.openStore(ctx, false)
	if err != nil {
		return CodexSettings{}, err
	}
	defer db.Close()
	if _, err := requireEnabledProviderIfPresent(ctx, db); err != nil {
		return CodexSettings{}, err
	}
	var result CodexSettings
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		var txErr error
		result, txErr = getCodexSettings(ctx, txStore, true)
		return txErr
	})
	return result, err
}

func (service *Service) UpdateSettings(ctx context.Context, req UpdateCodexSettingsRequest) (CodexSettings, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexSettings{}, err
	}
	db, err := service.openStore(ctx, false)
	if err != nil {
		return CodexSettings{}, err
	}
	defer db.Close()
	if _, err := requireEnabledProviderIfPresent(ctx, db); err != nil {
		return CodexSettings{}, err
	}

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
			if err := upsertCodexSetting(ctx, txStore, codexUsageSyncIntervalSettingKey, interval); err != nil {
				return err
			}
			if err := txStore.DeleteSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey); err != nil && !errors.Is(err, store.ErrNotFound) {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to remove migrated Codex usage sync setting", err)
			}
		}

		profileID := strings.TrimSpace(req.ProfileID)
		if req.QuotaRefreshIntervalSeconds != nil || req.AuthKeepaliveEnabled != nil {
			if profileID == "" {
				return apperror.New(apperror.ProfileInvalid, "Codex profile id is required")
			}
			var profileSettings *CodexProfileSettings
			for i := range current.Profiles {
				if current.Profiles[i].ProfileID == profileID {
					profileSettings = &current.Profiles[i]
					break
				}
			}
			if profileSettings == nil {
				return apperror.New(apperror.ProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
			}
			interval := profileSettings.QuotaRefreshIntervalSeconds
			if req.QuotaRefreshIntervalSeconds != nil {
				var appErr *apperror.Error
				interval, appErr = normalizeCodexQuotaRefreshInterval(*req.QuotaRefreshIntervalSeconds)
				if appErr != nil {
					return appErr
				}
			}
			keepalive := profileSettings.AuthKeepaliveEnabled
			if req.AuthKeepaliveEnabled != nil {
				keepalive = *req.AuthKeepaliveEnabled
			}
			if appErr := codexautomation.ValidateProfileSettings(interval, keepalive, profileSettings.QuotaSupported, profileSettings.AuthKeepaliveSupported); appErr != nil {
				return appErr.WithDetail("profile_id", profileID)
			}
			if _, err := txStore.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
				ProfileID: profileID, ProviderID: codexconfig.ProviderID,
				QuotaRefreshIntervalSeconds: interval, AuthKeepaliveEnabled: keepalive,
			}); err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Codex profile settings", err)
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

func (service *Service) ListAutomationTargets(ctx context.Context) ([]CodexAutomationTarget, error) {
	if err := service.requireAccess(ctx); err != nil {
		return nil, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := requireEnabledProviderIfPresent(ctx, db); err != nil {
		return nil, err
	}
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
		if err := upsertCodexSetting(ctx, db, codexUsageSyncIntervalSettingKey, interval); err != nil {
			return CodexSettings{}, err
		}
		if err := db.DeleteSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey); err != nil && !errors.Is(err, store.ErrNotFound) {
			return CodexSettings{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to remove migrated Codex usage sync setting", err)
		}
	}
	summaries, err := listCodexProfileSummaries(ctx, db)
	if err != nil {
		return CodexSettings{}, err
	}
	stored, err := db.ListProviderProfileSettings(ctx, codexconfig.ProviderID)
	if err != nil {
		return CodexSettings{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to load Codex profile settings", err)
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
		return 0, false, apperror.Wrap(apperror.StoreStatusFailed, "failed to load Codex usage sync interval", err)
	}
	var interval int
	if err := json.Unmarshal([]byte(setting.ValueJSON), &interval); err != nil {
		return 0, false, apperror.Wrap(apperror.SettingInvalid, "Codex usage sync interval is invalid", err)
	}
	normalized, appErr := normalizeCodexUsageSyncInterval(interval)
	if appErr != nil {
		return 0, false, appErr
	}
	return normalized, legacy, nil
}

func normalizeCodexUsageSyncInterval(value int) (int, *apperror.Error) {
	return codexautomation.NormalizeUsageSyncInterval(value)
}

func normalizeCodexQuotaRefreshInterval(value int) (int, *apperror.Error) {
	return codexautomation.NormalizeQuotaRefreshInterval(value)
}

func upsertCodexSetting(ctx context.Context, db *store.Store, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return apperror.Wrap(apperror.SettingInvalid, "failed to encode Codex setting", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: key, ValueJSON: string(raw)}); err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Codex setting", err)
	}
	return nil
}
