package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/app"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
)

func TestBootstrapInitializesOnlyProfileDeckRuntime(t *testing.T) {
	configDir := t.TempDir()
	codexDir := filepath.Join(t.TempDir(), ".codex")

	err := Bootstrap(context.Background(), Environment{ConfigDir: configDir, CodexDir: codexDir})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(configDir, "profiledeck", "profiledeck.db")); err != nil {
		t.Fatalf("expected profiledeck database to exist, got %v", err)
	}
	if _, err := os.Stat(codexDir); !os.IsNotExist(err) {
		t.Fatalf("expected desktop bootstrap not to create Codex home, got %v", err)
	}
}

func TestDashboardReportsStartupError(t *testing.T) {
	service := NewServices(app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, app.NewError(app.ErrorRuntimeInitFailed, "startup failed")).App

	result, err := service.Dashboard(context.Background())
	if err != nil {
		t.Fatalf("expected dashboard to tolerate startup error, got %v", err)
	}
	if result.StartupError == nil || result.StartupError.Code != string(app.ErrorRuntimeInitFailed) {
		t.Fatalf("expected structured startup error, got %#v", result.StartupError)
	}
}

func TestInitializeClearsStartupError(t *testing.T) {
	ctx := context.Background()
	service := NewServices(app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, app.NewError(app.ErrorRuntimeInitFailed, "startup failed")).App

	before, err := service.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected dashboard to tolerate startup error, got %v", err)
	}
	if before.StartupError == nil {
		t.Fatalf("expected startup error before initialize")
	}

	if _, err := service.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}
	after, err := service.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected dashboard after initialize to succeed, got %v", err)
	}
	if after.StartupError != nil {
		t.Fatalf("expected startup error to be cleared, got %#v", after.StartupError)
	}
}

func TestSwitchApplyRequiresExpectedPlanFingerprint(t *testing.T) {
	service := NewServices(app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil).Switch

	_, err := service.Apply(context.Background(), SwitchApplyRequest{
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	})
	var appErr *app.AppError
	if !errors.As(err, &appErr) || appErr.Code != app.ErrorConfirmationRequired {
		t.Fatalf("expected missing fingerprint to fail with confirmation error, got %v", err)
	}
}

func TestSettingsServicePersistsLanguage(t *testing.T) {
	ctx := context.Background()
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}

	initial, err := services.Settings.Get(ctx)
	if err != nil {
		t.Fatalf("expected settings get to succeed, got %v", err)
	}
	if initial.Language != app.DesktopLanguageAuto {
		t.Fatalf("expected default language auto, got %q", initial.Language)
	}

	updated, err := services.Settings.Update(ctx, app.UpdateDesktopSettingsRequest{Language: app.DesktopLanguageEnUS})
	if err != nil {
		t.Fatalf("expected settings update to succeed, got %v", err)
	}
	if updated.Language != app.DesktopLanguageEnUS {
		t.Fatalf("expected en-US, got %q", updated.Language)
	}
}

func TestServicesNotifyDesktopChanges(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir}, nil)
	events := []DesktopChangeEvent{}
	cancel := services.SubscribeChanges(func(event DesktopChangeEvent) {
		events = append(events, event)
	})

	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}
	if len(events) != 1 || events[0].Kind != DesktopChangeInitialized || events[0].Status != DesktopChangeStatusSuccess {
		t.Fatalf("expected initialized event, got %#v", events)
	}
	if events[0].OperationID != "" {
		t.Fatalf("expected initialize event not to leak local paths in operation_id, got %q", events[0].OperationID)
	}

	cancel()
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected second initialize to succeed, got %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected canceled listener not to receive events, got %#v", events)
	}
}

func TestSwitchApplyMissingFingerprintDoesNotNotify(t *testing.T) {
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) {
		events = append(events, event)
	})

	_, err := services.Switch.Apply(context.Background(), SwitchApplyRequest{
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	})
	if err == nil {
		t.Fatalf("expected missing fingerprint to fail")
	}
	if len(events) != 0 {
		t.Fatalf("expected pure desktop validation error not to notify, got %#v", events)
	}
}

func TestNotifyMutationResultMarksCanceled(t *testing.T) {
	changes := NewChangeNotifier()
	events := []DesktopChangeEvent{}
	changes.Subscribe(func(event DesktopChangeEvent) {
		events = append(events, event)
	})

	notifyMutationResult(changes, DesktopChangeSwitchApplied, "switch.apply", codexconfig.ProviderID, "work", "op-1", context.Canceled)

	if len(events) != 1 {
		t.Fatalf("expected canceled event, got %#v", events)
	}
	event := events[0]
	if event.Status != DesktopChangeStatusCanceled || event.Error == nil || event.Error.Code != "CANCELED" {
		t.Fatalf("unexpected canceled event: %#v", event)
	}
	if event.ProviderID != codexconfig.ProviderID || event.ProfileID != "work" || event.OperationID != "op-1" {
		t.Fatalf("expected event context to be preserved, got %#v", event)
	}
}

func TestSwitchApplyStateFailureNotifies(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir}, nil)
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) {
		events = append(events, event)
	})

	_, err := services.Switch.Apply(ctx, SwitchApplyRequest{
		ProviderID:              codexconfig.ProviderID,
		ProfileID:               "missing",
		ExpectedPlanFingerprint: "stale",
		Confirm:                 true,
	})
	if err == nil {
		t.Fatalf("expected switch apply to fail")
	}
	if len(events) != 1 {
		t.Fatalf("expected failed switch event, got %#v", events)
	}
	event := events[0]
	if event.Kind != DesktopChangeSwitchApplied ||
		event.Status != DesktopChangeStatusFailure ||
		event.ProviderID != codexconfig.ProviderID ||
		event.ProfileID != "missing" ||
		event.Error == nil {
		t.Fatalf("unexpected failed switch event: %#v", event)
	}
}

func TestDashboardHonorsCanceledContext(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir}, nil).App.Dashboard(canceled)
	if err == nil {
		t.Fatalf("expected dashboard to return a cancellation error")
	}
}

func TestFormatDesktopError(t *testing.T) {
	err := app.NewError(app.ErrorRuntimeInitFailed, "runtime failed").WithDetail("path", "/tmp/profiledeck")

	result := FormatDesktopError(err)
	if result.Code != string(app.ErrorRuntimeInitFailed) || result.Message != "runtime failed" || result.Details["path"] != "/tmp/profiledeck" {
		t.Fatalf("unexpected app error format: %#v", result)
	}
}

func TestFormatDesktopErrorCanceled(t *testing.T) {
	result := FormatDesktopError(context.Canceled)

	if result.Code != "CANCELED" || result.Message == "" {
		t.Fatalf("unexpected cancellation format: %#v", result)
	}
}

func TestFormatDesktopErrorDoesNotExposeRawUnknownError(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "profiledeck.db")

	result := FormatDesktopError(errors.New("open " + rawPath + ": permission denied"))
	if result.Code != "DESKTOP_ERROR" || result.Message == "" {
		t.Fatalf("unexpected unknown error format: %#v", result)
	}
	if strings.Contains(result.Message, rawPath) {
		t.Fatalf("expected unknown desktop error to omit raw path, got %#v", result)
	}
}

func TestDashboardDoesNotExposeRawCodexCredential(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	ctx := context.Background()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	rawSecret := "raw-access-token"
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"work","access_token":"`+rawSecret+`"}}`), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	dashboard, err := services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected dashboard to succeed, got %v", err)
	}
	raw, err := json.Marshal(dashboard)
	if err != nil {
		t.Fatalf("expected dashboard to marshal, got %v", err)
	}
	if strings.Contains(string(raw), rawSecret) || strings.Contains(string(raw), "access_token") {
		t.Fatalf("expected desktop dashboard to omit raw credential payload, got %s", raw)
	}
}

func TestCodexCreateProfileDoesNotExposeRawAuthPayload(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(context.Background(), Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	accessToken := "desktop-access-token"
	refreshToken := "desktop-refresh-token"
	authPayload := `{"tokens":{"account_id":"work-account","access_token":"` + accessToken + `","refresh_token":"` + refreshToken + `"}}`
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(authPayload), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}

	name := "Work"
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	result, err := services.Codex.CreateProfile(context.Background(), CreateCodexProfileRequest{
		ProfileID: "work",
		Name:      &name,
	})
	if err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("expected create result to marshal, got %v", err)
	}
	for _, leaked := range []string{accessToken, refreshToken, "access_token", "refresh_token"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("expected desktop create DTO to omit raw auth payload %q, got %s", leaked, raw)
		}
	}
	detail, err := services.Codex.ShowProfile(context.Background(), "work")
	if err != nil {
		t.Fatalf("expected created disk-backed profile detail, got %v", err)
	}
	if detail.Summary.Model != "gpt-5-codex" || detail.Summary.CodexAccountID != "work-account" {
		t.Fatalf("expected create to read current Codex files, got %#v", detail.Summary)
	}
}

func TestCodexProfileListAndShowUseSharedAppSemantics(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	accessToken := "desktop-list-access-token"
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"work-account","access_token":"`+accessToken+`"}}`), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}

	list, err := services.Codex.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	if len(list.Profiles) != 1 || list.Profiles[0].Profile.ID != "work" {
		t.Fatalf("unexpected profile list: %#v", list)
	}
	dashboard, err := services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected dashboard to succeed, got %v", err)
	}
	if dashboard.CodexProfiles == nil || len(dashboard.CodexProfiles.Profiles) != 1 || dashboard.CodexProfiles.Profiles[0].Profile.ID != "work" {
		t.Fatalf("expected dashboard to include Codex profiles, got %#v", dashboard.CodexProfiles)
	}
	detail, err := services.Codex.ShowProfile(ctx, "work")
	if err != nil {
		t.Fatalf("expected profile show to succeed, got %v", err)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("expected detail to marshal, got %v", err)
	}
	if strings.Contains(string(raw), accessToken) || strings.Contains(string(raw), "access_token") {
		t.Fatalf("expected desktop profile detail to omit raw auth, got %s", raw)
	}
}

func TestCodexSyncProfileReadsCurrentFilesBehindDesktopBoundary(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"work","access_token":"initial"}}`)

	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}

	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5.1-codex"`+"\n", `{"tokens":{"account_id":"updated","access_token":"changed"}}`)
	if _, err := services.Codex.SyncProfile(ctx, SyncCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected sync to read current Codex files, got %v", err)
	}

	detail, err := services.Codex.ShowProfile(ctx, "work")
	if err != nil {
		t.Fatalf("expected synced profile detail, got %v", err)
	}
	if detail.Summary.Model != "gpt-5.1-codex" || detail.Summary.CodexAccountID != "updated" {
		t.Fatalf("expected disk state to be synced, got %#v", detail.Summary)
	}
}

func TestCodexUpdateProfileMetadataPersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"work","access_token":"initial"}}`)

	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) {
		events = append(events, event)
	})

	name := "Work account"
	description := "Primary Codex profile"
	updated, err := services.Codex.UpdateProfileMetadata(ctx, UpdateCodexProfileMetadataRequest{
		ProfileID:   "work",
		Name:        &name,
		Description: &description,
	})
	if err != nil {
		t.Fatalf("expected metadata update to succeed, got %v", err)
	}
	if updated.Name != name || updated.Description != description {
		t.Fatalf("unexpected updated profile: %#v", updated)
	}
	if len(events) != 1 || events[0].Kind != DesktopChangeCodexProfileChanged || events[0].Source != "codex.updateProfileMetadata" || events[0].ProfileID != "work" {
		t.Fatalf("expected Codex profile change notification, got %#v", events)
	}

	emptyName := " "
	_, err = services.Codex.UpdateProfileMetadata(ctx, UpdateCodexProfileMetadataRequest{ProfileID: "work", Name: &emptyName})
	var appErr *app.AppError
	if !errors.As(err, &appErr) || appErr.Code != app.ErrorProfileInvalid {
		t.Fatalf("expected metadata validation error, got %v", err)
	}
	if len(events) != 2 || events[1].Status != DesktopChangeStatusFailure || events[1].Error == nil || events[1].Error.Code != string(app.ErrorProfileInvalid) {
		t.Fatalf("expected failed metadata notification, got %#v", events)
	}
}

func TestCodexSyncSharedCredentialConflictKeepsSupportedUpdates(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, Environment{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"shared","access_token":"initial"}}`)

	services := NewServices(app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	if _, err := services.Codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work",
		ProfileID:       "work-copy",
		AuthBinding:     app.CodexForkAuthBindingShareParent,
	}); err != nil {
		t.Fatalf("expected shared fork to succeed, got %v", err)
	}

	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"shared","access_token":"changed"}}`)
	_, err := services.Codex.SyncProfile(ctx, SyncCodexProfileRequest{ProfileID: "work"})
	if err == nil {
		t.Fatalf("expected shared credential conflict")
	}
	desktopErr := FormatDesktopError(err)
	updates, ok := desktopErr.Details["supported_auth_updates"].([]string)
	if desktopErr.Code != string(app.ErrorCodexInvalid) || !ok || len(updates) != 2 || updates[0] != app.CodexSyncAuthUpdateShared || updates[1] != app.CodexSyncAuthUpdateForkNew {
		t.Fatalf("expected supported auth updates in desktop error, got %#v", desktopErr)
	}
}

func writeDesktopCodexFiles(t *testing.T, codexDir string, config string, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
}
