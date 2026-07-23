package codex

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCodexSettingsDefaultsAndProfileUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}

	initial, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx)
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
	updated, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{
		ProfileID: "work", UsageSyncIntervalSeconds: &usageInterval,
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
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected Provider fixture, got %v", err)
	}
	valid := 30
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{UsageSyncIntervalSeconds: &valid}); err != nil {
		t.Fatalf("expected initial update, got %v", err)
	}
	invalid := 900
	_, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{UsageSyncIntervalSeconds: &invalid})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
		t.Fatalf("expected invalid setting error, got %v", err)
	}
	reloaded, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx)
	if err != nil || reloaded.UsageSyncIntervalSeconds != valid {
		t.Fatalf("expected previous setting to remain, settings=%#v err=%v", reloaded, err)
	}
}

func TestCodexSettingsRejectsUnsupportedKeepaliveMode(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgptAuthTokens","tokens":{"account_id":"display","access_token":"token","refresh_token":"external"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "external"}); err != nil {
		t.Fatalf("expected external auth Profile, got %v", err)
	}
	enabled := true
	_, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{ProfileID: "external", AuthKeepaliveEnabled: &enabled})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
		t.Fatalf("expected unsupported keepalive setting error, got %v", err)
	}
	quotaInterval := 300
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{ProfileID: "external", QuotaRefreshIntervalSeconds: &quotaInterval}); err != nil {
		t.Fatalf("expected external auth quota automation to remain supported, got %v", err)
	}
}

func TestCodexProviderSettingsFailClosedForUnknownVersionAndFields(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	initialized, err := initCodexTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected fixture store close, got %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open raw fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		INSERT INTO provider_settings (
			provider_id, schema_version, settings_json, updated_at_unix_ms
		) VALUES ('codex', 2, '{"usage_sync_interval_seconds":30}', 1)
	`); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected unknown-version fixture, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw fixture database: %v", err)
	}

	if _, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx); err == nil {
		t.Fatal("unknown Provider settings version was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unknown version error = %v", err)
		}
	}
	db, err = openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	if _, err := db.UpsertProviderSetting(ctx, store.UpsertProviderSettingParams{
		ProviderID: "codex", SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON: `{"usage_sync_interval_seconds":30,"future_field":true}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected unknown-field fixture, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close unknown-field fixture: %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx); err == nil {
		t.Fatal("unknown Provider settings field was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unknown field error = %v", err)
		}
	}
}

func TestCodexProfileSettingsFailClosedForUnknownVersionAndFields(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	initialized, err := initCodexTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open raw fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		INSERT INTO provider_profile_settings (
			profile_id, provider_id, schema_version, settings_json, updated_at_unix_ms
		) VALUES (
			'work', 'codex', 2,
			'{"quota_refresh_interval_seconds":0,"auth_keepalive_enabled":false}',
			1
		)
	`); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected unknown-version fixture, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw fixture database: %v", err)
	}

	if _, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx); err == nil {
		t.Fatal("unknown Profile settings version was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unknown version error = %v", err)
		}
	}
	rawDB, err = sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen raw fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		UPDATE provider_profile_settings
		SET schema_version = 1,
			settings_json = '{"quota_refresh_interval_seconds":0,"auth_keepalive_enabled":false,"future_field":true}'
		WHERE profile_id = 'work' AND provider_id = 'codex'
	`); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected unknown-field fixture, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close unknown-field fixture: %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx); err == nil {
		t.Fatal("unknown Profile settings field was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unknown field error = %v", err)
		}
	}
}

func TestCodexSettingsUpdateRollsBackAcrossUsageAndProfileWrites(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	initialized, err := initCodexTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"display","access_token":"token","refresh_token":"refresh"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
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
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateSettings(ctx, UpdateCodexSettingsRequest{
		ProfileID: "work", UsageSyncIntervalSeconds: &usageInterval,
		QuotaRefreshIntervalSeconds: &quotaInterval,
	}); err == nil {
		t.Fatal("expected forced profile setting failure")
	}
	settings, err := newCodexTestEnvironment(t, configDir, "").codex.GetSettings(ctx)
	if err != nil {
		t.Fatalf("expected settings reload, got %v", err)
	}
	if settings.UsageSyncIntervalSeconds != CodexUsageSyncIntervalDefault || settings.Profiles[0].QuotaRefreshIntervalSeconds != 0 {
		t.Fatalf("expected transaction rollback, got %#v", settings)
	}
}
