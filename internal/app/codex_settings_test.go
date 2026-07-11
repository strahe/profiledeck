package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

func TestCodexSettingsDefaultsAndProfileUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}

	initial, err := GetCodexSettings(ctx, CodexSettingsRequest{ConfigDir: configDir})
	if err != nil || initial.UsageSyncIntervalSeconds != CodexUsageSyncIntervalDefault || len(initial.Profiles) != 1 {
		t.Fatalf("unexpected initial Codex settings: %#v, %v", initial, err)
	}
	if initial.Profiles[0].QuotaRefreshIntervalSeconds != 0 || initial.Profiles[0].AuthKeepaliveEnabled {
		t.Fatalf("expected profile automation off by default, got %#v", initial.Profiles[0])
	}
	if !initial.Profiles[0].QuotaSupported || !initial.Profiles[0].AuthKeepaliveSupported {
		t.Fatalf("expected managed ChatGPT auth support, got %#v", initial.Profiles[0])
	}

	usageInterval := 30
	quotaInterval := 600
	keepalive := true
	updated, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{
		ConfigDir: configDir, ProfileID: "work", UsageSyncIntervalSeconds: &usageInterval,
		QuotaRefreshIntervalSeconds: &quotaInterval, AuthKeepaliveEnabled: &keepalive,
	})
	if err != nil {
		t.Fatalf("expected Codex settings update, got %v", err)
	}
	if updated.UsageSyncIntervalSeconds != usageInterval || updated.Profiles[0].QuotaRefreshIntervalSeconds != quotaInterval || !updated.Profiles[0].AuthKeepaliveEnabled {
		t.Fatalf("unexpected updated settings: %#v", updated)
	}
}

func TestCodexSettingsRejectInvalidIntervalAtomically(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	valid := 30
	if _, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{ConfigDir: configDir, UsageSyncIntervalSeconds: &valid}); err != nil {
		t.Fatalf("expected initial update, got %v", err)
	}
	invalid := 900
	_, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{ConfigDir: configDir, UsageSyncIntervalSeconds: &invalid})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected invalid setting error, got %v", err)
	}
	reloaded, err := GetCodexSettings(ctx, CodexSettingsRequest{ConfigDir: configDir})
	if err != nil || reloaded.UsageSyncIntervalSeconds != valid {
		t.Fatalf("expected previous setting to remain, settings=%#v err=%v", reloaded, err)
	}
}

func TestCodexSettingsRejectsUnsupportedKeepaliveMode(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgptAuthTokens","tokens":{"account_id":"display","access_token":"token","refresh_token":"external"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "external"}); err != nil {
		t.Fatalf("expected external auth Profile, got %v", err)
	}
	enabled := true
	_, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{ConfigDir: configDir, ProfileID: "external", AuthKeepaliveEnabled: &enabled})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported keepalive setting error, got %v", err)
	}
	quotaInterval := 300
	if _, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{ConfigDir: configDir, ProfileID: "external", QuotaRefreshIntervalSeconds: &quotaInterval}); err != nil {
		t.Fatalf("expected external auth quota automation to remain supported, got %v", err)
	}
}

func TestCodexSettingsMigratesLegacyUsageInterval(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: legacyDesktopUsageSyncIntervalSettingKey, ValueJSON: "30"}); err != nil {
		_ = db.Close()
		t.Fatalf("expected legacy fixture, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected legacy fixture store close, got %v", err)
	}

	settings, err := GetCodexSettings(ctx, CodexSettingsRequest{ConfigDir: configDir})
	if err != nil || settings.UsageSyncIntervalSeconds != 30 {
		t.Fatalf("expected migrated interval, settings=%#v err=%v", settings, err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	defer db.Close()
	if _, err := db.GetSetting(ctx, codexUsageSyncIntervalSettingKey); err != nil {
		t.Fatalf("expected new setting key, got %v", err)
	}
	if _, err := db.GetSetting(ctx, legacyDesktopUsageSyncIntervalSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected legacy key removal, got %v", err)
	}
}

func TestCodexSettingsUpdateRollsBackAcrossUsageAndProfileWrites(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	initialized, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"display","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("expected fixture database, got %v", err)
	}
	defer rawDB.Close()
	if _, err := rawDB.ExecContext(ctx, `CREATE TRIGGER fail_profile_automation
		BEFORE INSERT ON provider_profile_settings
		BEGIN SELECT RAISE(FAIL, 'forced settings failure'); END`); err != nil {
		t.Fatalf("expected failure trigger, got %v", err)
	}
	usageInterval := 30
	quotaInterval := 600
	if _, err := UpdateCodexSettings(ctx, UpdateCodexSettingsRequest{
		ConfigDir: configDir, ProfileID: "work", UsageSyncIntervalSeconds: &usageInterval,
		QuotaRefreshIntervalSeconds: &quotaInterval,
	}); err == nil {
		t.Fatal("expected forced profile setting failure")
	}
	settings, err := GetCodexSettings(ctx, CodexSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload, got %v", err)
	}
	if settings.UsageSyncIntervalSeconds != CodexUsageSyncIntervalDefault || settings.Profiles[0].QuotaRefreshIntervalSeconds != 0 {
		t.Fatalf("expected transaction rollback, got %#v", settings)
	}
}
