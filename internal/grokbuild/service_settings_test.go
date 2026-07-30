package grokbuild_test

import (
	"context"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/grokbuild"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/usage"
)

func TestGrokBuildUsageSyncSettingsShareStrictProviderPolicy(t *testing.T) {
	ctx := context.Background()
	application := newApplication(t, t.TempDir())
	initial, err := application.GrokBuild().GetSettings(ctx)
	if err != nil || initial.UsageSyncIntervalSeconds != usage.UsageSyncIntervalDefault {
		t.Fatalf("initial settings = %#v, err = %v", initial, err)
	}
	interval := 30
	if _, err := application.GrokBuild().UpdateSettings(ctx, grokbuild.UpdateSettingsRequest{
		UsageSyncIntervalSeconds: &interval,
	}); err == nil {
		t.Fatal("settings update created a missing Provider")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.ProviderNotFound {
			t.Fatalf("missing Provider error = %v", err)
		}
	}
	if _, err := application.Usage().SyncGrokBuild(ctx); err != nil {
		t.Fatalf("explicit usage sync did not provision Provider: %v", err)
	}
	updated, err := application.GrokBuild().UpdateSettings(ctx, grokbuild.UpdateSettingsRequest{
		UsageSyncIntervalSeconds: &interval,
	})
	if err != nil || updated.UsageSyncIntervalSeconds != 30 {
		t.Fatalf("updated settings = %#v, err = %v", updated, err)
	}

	invalid := 10
	if _, err := application.GrokBuild().UpdateSettings(ctx, grokbuild.UpdateSettingsRequest{
		UsageSyncIntervalSeconds: &invalid,
	}); err == nil {
		t.Fatal("unsupported interval was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unsupported interval error = %v", err)
		}
	}

	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	setting, err := db.GetProviderSetting(ctx, grokconfig.ProviderID)
	if err != nil ||
		setting.SchemaVersion != store.ProviderSettingsSchemaVersion ||
		setting.SettingsJSON != `{"usage_sync_interval_seconds":30}` {
		_ = db.Close()
		t.Fatalf("stored settings = %#v, err = %v", setting, err)
	}
	if _, err := db.UpsertProviderSetting(ctx, store.UpsertProviderSettingParams{
		ProviderID: grokconfig.ProviderID, SchemaVersion: store.ProviderSettingsSchemaVersion,
		SettingsJSON: `{"usage_sync_interval_seconds":30,"future_field":true}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("write unknown-field fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}
	if _, err := application.GrokBuild().GetSettings(ctx); err == nil {
		t.Fatal("unknown Provider settings field was accepted")
	} else {
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.SettingInvalid {
			t.Fatalf("unknown field error = %v", err)
		}
	}
}
