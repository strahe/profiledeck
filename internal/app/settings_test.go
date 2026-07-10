package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestDesktopSettingsDefaultAndUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	initial, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected default settings to load, got %v", err)
	}
	if initial.Language != DesktopLanguageAuto {
		t.Fatalf("expected default language auto, got %q", initial.Language)
	}
	if initial.UsageSyncIntervalSeconds != DesktopUsageSyncIntervalDefault {
		t.Fatalf("expected default usage sync interval %d, got %d", DesktopUsageSyncIntervalDefault, initial.UsageSyncIntervalSeconds)
	}

	language := DesktopLanguageZhCN
	updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &language})
	if err != nil {
		t.Fatalf("expected settings update to succeed, got %v", err)
	}
	if updated.Language != DesktopLanguageZhCN || updated.UsageSyncIntervalSeconds != DesktopUsageSyncIntervalDefault {
		t.Fatalf("expected language-only update to preserve interval, got %#v", updated)
	}

	interval := 30
	updated, err = UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, UsageSyncIntervalSeconds: &interval})
	if err != nil {
		t.Fatalf("expected interval update to succeed, got %v", err)
	}
	if updated.Language != DesktopLanguageZhCN || updated.UsageSyncIntervalSeconds != interval {
		t.Fatalf("expected interval-only update to preserve language, got %#v", updated)
	}

	reloaded, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	if reloaded.Language != DesktopLanguageZhCN || reloaded.UsageSyncIntervalSeconds != interval {
		t.Fatalf("expected persisted settings, got %#v", reloaded)
	}
}

func TestDesktopSettingsAcceptsSupportedUsageSyncIntervals(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	for _, interval := range []int{5, 15, 30, 60} {
		interval := interval
		updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, UsageSyncIntervalSeconds: &interval})
		if err != nil || updated.UsageSyncIntervalSeconds != interval {
			t.Fatalf("expected interval %d to be accepted, settings=%#v err=%v", interval, updated, err)
		}
	}
}

func TestDesktopSettingsRejectsInvalidPartialUpdateAtomically(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	language := DesktopLanguageZhCN
	if _, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &language}); err != nil {
		t.Fatalf("expected initial language update to succeed, got %v", err)
	}

	invalidLanguage := "fr-FR"
	_, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &invalidLanguage})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported language to fail with setting error, got %v", err)
	}

	nextLanguage := DesktopLanguageEnUS
	invalidInterval := 10
	_, err = UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{
		ConfigDir:                configDir,
		Language:                 &nextLanguage,
		UsageSyncIntervalSeconds: &invalidInterval,
	})
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported interval to fail with setting error, got %v", err)
	}
	reloaded, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	if reloaded.Language != DesktopLanguageZhCN || reloaded.UsageSyncIntervalSeconds != DesktopUsageSyncIntervalDefault {
		t.Fatalf("expected invalid partial update to leave settings unchanged, got %#v", reloaded)
	}
}

func TestDesktopSettingsTransactionRollsBackEarlierFieldWrite(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initialized, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("expected fixture database open, got %v", err)
	}
	defer rawDB.Close()
	if _, err := rawDB.ExecContext(ctx, `CREATE TRIGGER fail_usage_sync_interval
		BEFORE INSERT ON settings
		WHEN NEW.key = 'desktop.usage_sync_interval_seconds'
		BEGIN
			SELECT RAISE(FAIL, 'forced settings failure');
		END`); err != nil {
		t.Fatalf("expected rollback fixture trigger, got %v", err)
	}

	language := DesktopLanguageZhCN
	interval := 30
	if _, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{
		ConfigDir:                configDir,
		Language:                 &language,
		UsageSyncIntervalSeconds: &interval,
	}); err == nil {
		t.Fatalf("expected forced second field write failure")
	}
	settings, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload, got %v", err)
	}
	if settings.Language != DesktopLanguageAuto || settings.UsageSyncIntervalSeconds != DesktopUsageSyncIntervalDefault {
		t.Fatalf("expected transaction rollback to preserve defaults, got %#v", settings)
	}
}
