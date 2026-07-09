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
	if err != nil {
		t.Fatalf("expected default settings to load, got %v", err)
	}
	if initial.Language != DesktopLanguageAuto {
		t.Fatalf("expected default language auto, got %q", initial.Language)
	}

	updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: DesktopLanguageZhCN})
	if err != nil {
		t.Fatalf("expected settings update to succeed, got %v", err)
	}
	if updated.Language != DesktopLanguageZhCN {
		t.Fatalf("expected zh-CN, got %q", updated.Language)
	}

	reloaded, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	if reloaded.Language != DesktopLanguageZhCN {
		t.Fatalf("expected persisted zh-CN, got %q", reloaded.Language)
	}
}

func TestDesktopSettingsRejectsUnsupportedLanguage(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: "fr-FR"})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported language to fail with setting error, got %v", err)
	}
}
