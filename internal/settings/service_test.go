package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
)

func TestDesktopSettingsDefaultsAndPartialUpdates(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	service := newTestService(t, ctx, configDir)

	initial, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("expected default desktop settings, got %v", err)
	}
	if initial.Language != DesktopLanguageAuto ||
		initial.Appearance != DesktopAppearanceSystem ||
		initial.SidebarCollapsed ||
		!initial.AutomaticUpdates ||
		initial.UpdateChannel != DesktopUpdateChannelStable ||
		!initial.AutomaticBackups {
		t.Fatalf("expected default desktop settings, settings=%#v err=%v", initial, err)
	}

	language := DesktopLanguageZhCN
	updated, err := service.Update(ctx, UpdateRequest{Language: &language})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceSystem || updated.SidebarCollapsed || !updated.AutomaticUpdates {
		t.Fatalf("expected language update, settings=%#v err=%v", updated, err)
	}

	appearance := DesktopAppearanceDark
	updated, err = service.Update(ctx, UpdateRequest{Appearance: &appearance})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceDark || updated.SidebarCollapsed {
		t.Fatalf("expected appearance-only update, settings=%#v err=%v", updated, err)
	}

	collapsed := true
	updated, err = service.Update(ctx, UpdateRequest{SidebarCollapsed: &collapsed})
	if err != nil || updated.Language != DesktopLanguageZhCN || updated.Appearance != DesktopAppearanceDark || !updated.SidebarCollapsed {
		t.Fatalf("expected sidebar-only update, settings=%#v err=%v", updated, err)
	}

	reloaded, err := service.Get(ctx)
	if err != nil || reloaded != updated {
		t.Fatalf("expected all desktop settings to persist, settings=%#v err=%v", reloaded, err)
	}
}

func TestDesktopSettingsCombinedUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	service := newTestService(t, ctx, configDir)

	language := DesktopLanguageEnUS
	appearance := DesktopAppearanceLight
	collapsed := true
	updated, err := service.Update(ctx, UpdateRequest{
		Language:         &language,
		Appearance:       &appearance,
		SidebarCollapsed: &collapsed,
	})
	if err != nil {
		t.Fatalf("expected combined update to succeed, got %v", err)
	}
	want := Desktop{
		Language: language, Appearance: appearance, SidebarCollapsed: collapsed,
		AutomaticUpdates: true, UpdateChannel: DesktopUpdateChannelStable, AutomaticBackups: true,
	}
	if updated != want {
		t.Fatalf("unexpected combined settings: got %#v want %#v", updated, want)
	}
}

func TestDesktopAutomaticUpdatesUsesDedicatedSetter(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, ctx, t.TempDir())

	updated, err := service.SetAutomaticUpdates(ctx, false)
	if err != nil {
		t.Fatalf("disable automatic updates: %v", err)
	}
	if updated.AutomaticUpdates {
		t.Fatalf("expected automatic updates to be disabled: %#v", updated)
	}

	reloaded, err := service.Get(ctx)
	if err != nil || reloaded.AutomaticUpdates {
		t.Fatalf("expected automatic updates setting to persist: settings=%#v err=%v", reloaded, err)
	}
}

func TestDesktopUpdateChannelUsesBuildDefaultUntilUserChangesIt(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, ctx, t.TempDir())

	initialized, err := service.EnsureUpdateChannel(ctx, DesktopUpdateChannelBeta)
	if err != nil || initialized.UpdateChannel != DesktopUpdateChannelBeta {
		t.Fatalf("initialize beta channel: settings=%#v err=%v", initialized, err)
	}
	stillBeta, err := service.EnsureUpdateChannel(ctx, DesktopUpdateChannelStable)
	if err != nil || stillBeta.UpdateChannel != DesktopUpdateChannelBeta {
		t.Fatalf("build default replaced persisted channel: settings=%#v err=%v", stillBeta, err)
	}
	stable, err := service.SetUpdateChannel(ctx, DesktopUpdateChannelStable)
	if err != nil || stable.UpdateChannel != DesktopUpdateChannelStable {
		t.Fatalf("switch stable channel: settings=%#v err=%v", stable, err)
	}
	reloaded, err := service.Get(ctx)
	if err != nil || reloaded.UpdateChannel != DesktopUpdateChannelStable {
		t.Fatalf("reload stable channel: settings=%#v err=%v", reloaded, err)
	}
}

func TestDesktopUpdateChannelRejectsUnsupportedValue(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, ctx, t.TempDir())

	_, err := service.SetUpdateChannel(ctx, "dev")
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
		t.Fatalf("expected unsupported update channel error, got %v", err)
	}
}

func TestDesktopSettingsRejectsUnsupportedLanguage(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	service := newTestService(t, ctx, configDir)
	invalid := "fr-FR"
	_, err := service.Update(ctx, UpdateRequest{Language: &invalid})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
		t.Fatalf("expected unsupported language setting error, got %v", err)
	}
}

func TestDesktopSettingsRejectsUnsupportedAppearanceWithoutPartialUpdate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	service := newTestService(t, ctx, configDir)

	language := DesktopLanguageZhCN
	invalid := "sepia"
	_, err := service.Update(ctx, UpdateRequest{
		Language:   &language,
		Appearance: &invalid,
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
		t.Fatalf("expected unsupported appearance setting error, got %v", err)
	}

	settings, err := service.Get(ctx)
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	if settings.Language != DesktopLanguageAuto || settings.Appearance != DesktopAppearanceSystem || settings.SidebarCollapsed || !settings.AutomaticUpdates {
		t.Fatalf("expected rejected update to leave settings unchanged, got %#v", settings)
	}
}

func newTestService(t *testing.T, ctx context.Context, configDir string) *Service {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	if _, err := bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	return NewService(runtimeService.StoreFactory())
}
