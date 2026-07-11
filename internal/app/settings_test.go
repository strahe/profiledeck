package app

import (
	"context"
	"errors"
	"testing"
)

func TestDesktopSettingsDefaultsAndPartialUpdates(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	initial, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected default desktop settings, got %v", err)
	}
	if initial.Language != DesktopLanguageAuto || initial.Appearance != DesktopAppearanceSystem || initial.SidebarCollapsed {
		t.Fatalf("expected default desktop settings, settings=%#v err=%v", initial, err)
	}

	language := DesktopLanguageZhCN
	updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Language: &language})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceSystem || updated.SidebarCollapsed {
		t.Fatalf("expected language update, settings=%#v err=%v", updated, err)
	}

	appearance := DesktopAppearanceDark
	updated, err = UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, Appearance: &appearance})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceDark || updated.SidebarCollapsed {
		t.Fatalf("expected appearance-only update, settings=%#v err=%v", updated, err)
	}

	collapsed := true
	updated, err = UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{ConfigDir: configDir, SidebarCollapsed: &collapsed})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceDark || !updated.SidebarCollapsed {
		t.Fatalf("expected sidebar-only update, settings=%#v err=%v", updated, err)
	}

	reloaded, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil || reloaded != updated {
		t.Fatalf("expected all desktop settings to persist, settings=%#v err=%v", reloaded, err)
	}
}

func TestDesktopSettingsCombinedUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	language := DesktopLanguageEnUS
	appearance := DesktopAppearanceLight
	collapsed := true
	updated, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{
		ConfigDir:        configDir,
		Language:         &language,
		Appearance:       &appearance,
		SidebarCollapsed: &collapsed,
	})
	if err != nil {
		t.Fatalf("expected combined update to succeed, got %v", err)
	}
	want := DesktopSettings{Language: language, Appearance: appearance, SidebarCollapsed: collapsed}
	if updated != want {
		t.Fatalf("unexpected combined settings: got %#v want %#v", updated, want)
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

func TestDesktopSettingsRejectsUnsupportedAppearanceWithoutPartialUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	language := DesktopLanguageZhCN
	invalid := "sepia"
	_, err := UpdateDesktopSettings(ctx, UpdateDesktopSettingsRequest{
		ConfigDir:  configDir,
		Language:   &language,
		Appearance: &invalid,
	})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSettingInvalid {
		t.Fatalf("expected unsupported appearance setting error, got %v", err)
	}

	settings, err := GetDesktopSettings(ctx, DesktopSettingsRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	if settings.Language != DesktopLanguageAuto || settings.Appearance != DesktopAppearanceSystem || settings.SidebarCollapsed {
		t.Fatalf("expected rejected update to leave settings unchanged, got %#v", settings)
	}
}
