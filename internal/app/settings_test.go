package app

import (
	"context"
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
	if err != nil || initial.Language != DesktopLanguageAuto {
		t.Fatalf("expected default desktop settings, settings=%#v err=%v", initial, err)
	}
	language := DesktopLanguageZhCN
	updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &language})
	if err != nil || updated.Language != DesktopLanguageZhCN {
		t.Fatalf("expected language update, settings=%#v err=%v", updated, err)
	}
	reloaded, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil || reloaded.Language != DesktopLanguageZhCN {
		t.Fatalf("expected persisted language, settings=%#v err=%v", reloaded, err)
	}
}

func TestDesktopSettingsRejectsUnsupportedLanguage(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	invalid := "fr-FR"
	_, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &invalid})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported language setting error, got %v", err)
	}
}
