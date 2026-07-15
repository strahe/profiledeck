package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/agent"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/settings"
	"github.com/strahe/profiledeck/internal/usage"
)

func newBackendTestApplication(t *testing.T, env Environment) *app.Application {
	t.Helper()
	if env.ConfigDir == "" {
		env.ConfigDir = t.TempDir()
	}
	application, err := app.New(app.Config{
		ConfigDir: env.ConfigDir, CodexDir: env.CodexDir, AgentAccess: agent.AccessDesktopPreferences,
	})
	if err != nil {
		t.Fatalf("create test Application: %v", err)
	}
	t.Cleanup(application.Close)
	return application
}

func newTestServices(t *testing.T, info app.Info, env Environment, startupErr error) Services {
	t.Helper()
	return NewServices(newBackendTestApplication(t, env), info, env, startupErr)
}

func TestBootstrapInitializesOnlyProfileDeckRuntime(t *testing.T) {
	configDir := t.TempDir()
	codexDir := filepath.Join(t.TempDir(), ".codex")

	err := Bootstrap(context.Background(), newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir}))
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
	service := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, apperror.New(apperror.RuntimeInitFailed, "startup failed")).App

	result, err := service.Dashboard(context.Background())
	if err != nil {
		t.Fatalf("expected dashboard to tolerate startup error, got %v", err)
	}
	if result.StartupError == nil || result.StartupError.Code != string(apperror.RuntimeInitFailed) {
		t.Fatalf("expected structured startup error, got %#v", result.StartupError)
	}
}

func TestDashboardKeepsRecoveryAvailableWhenDatabaseIsDamaged(t *testing.T) {
	ctx := context.Background()
	env := Environment{ConfigDir: t.TempDir()}
	application := newBackendTestApplication(t, env)
	if err := Bootstrap(ctx, application); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(application.Runtime().Paths().Database + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(application.Runtime().Paths().Database, []byte("damaged database"), 0o600); err != nil {
		t.Fatal(err)
	}
	services := NewServices(application, app.DefaultInfo(), env, apperror.New(apperror.StoreSchemaInvalid, "application database is damaged"))

	result, err := services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("dashboard blocked startup recovery: %v", err)
	}
	if result.StartupError == nil || result.StartupError.Code != string(apperror.StoreSchemaInvalid) {
		t.Fatalf("startup recovery error is unavailable: %#v", result.StartupError)
	}
	if result.Status.SchemaHealthy {
		t.Fatalf("damaged database was reported healthy: %#v", result.Status)
	}
}

func TestInitializeClearsStartupError(t *testing.T) {
	ctx := context.Background()
	service := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, apperror.New(apperror.RuntimeInitFailed, "startup failed")).App

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

func TestDisabledAgentsRejectStaticServiceCallsButKeepSafetyServices(t *testing.T) {
	ctx := context.Background()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}
	state, err := services.Agent.SetEnabled(ctx, string(agent.Codex), false)
	if err != nil || state.Enabled {
		t.Fatalf("expected Codex Agent disable, state=%#v err=%v", state, err)
	}

	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Codex.Detect(ctx)
		return err
	}(), apperror.AgentDisabled)
	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Codex.QuotaRuntimeStatus(ctx)
		return err
	}(), apperror.AgentDisabled)
	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Usage.Summary(ctx, codexconfig.ProviderID)
		return err
	}(), apperror.AgentDisabled)
	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Usage.AutoSyncStatus(ctx)
		return err
	}(), apperror.AgentDisabled)
	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Switch.BuildPlan(ctx, codexconfig.ProviderID, "work")
		return err
	}(), apperror.AgentDisabled)
	assertDesktopServiceErrorCode(t, func() error {
		_, err := services.Switch.Apply(ctx, SwitchApplyRequest{
			ProviderID:              codexconfig.ProviderID,
			ProfileID:               "work",
			ExpectedPlanFingerprint: "fingerprint",
			Confirm:                 true,
		})
		return err
	}(), apperror.AgentDisabled)

	dashboard, err := services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected Dashboard to remain available, got %v", err)
	}
	if dashboard.CodexProfiles != nil || dashboard.CodexConfigSets != nil || dashboard.Usage != nil {
		t.Fatalf("disabled Codex data remained in Dashboard: %#v", dashboard)
	}
	if len(dashboard.Agents) != 3 || agentEnabled(dashboard.Agents, agent.Codex) {
		t.Fatalf("Dashboard did not expose resolved disabled state: %#v", dashboard.Agents)
	}
	for _, id := range []agent.ID{agent.Antigravity, agent.ClaudeCode} {
		if _, err := services.Agent.SetEnabled(ctx, string(id), false); err != nil {
			t.Fatalf("disable %s Agent: %v", id, err)
		}
	}
	dashboard, err = services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected all-disabled Dashboard to remain available, got %v", err)
	}
	for _, state := range dashboard.Agents {
		if state.Enabled {
			t.Fatalf("Dashboard retained enabled Agent after all were disabled: %#v", dashboard.Agents)
		}
	}
	if len(dashboard.Providers) != 0 || len(dashboard.ActiveStates) != 0 ||
		dashboard.CodexProfiles != nil || dashboard.AntigravityProfiles != nil || dashboard.ClaudeCodeProfiles != nil {
		t.Fatalf("all-disabled Dashboard exposed managed Agent data: %#v", dashboard)
	}
	if _, err := services.Backup.List(ctx); err != nil {
		t.Fatalf("Agent gate blocked backup inspection: %v", err)
	}
	if _, err := services.Doctor.Run(ctx); err != nil {
		t.Fatalf("Agent gate blocked Doctor: %v", err)
	}
	_, recoveryErr := services.Doctor.RecoverOperation(ctx, "missing-operation", true)
	var recoveryAppErr *apperror.Error
	if !errors.As(recoveryErr, &recoveryAppErr) || recoveryAppErr.Code == apperror.AgentDisabled {
		t.Fatalf("recovery was blocked by Agent gate: %v", recoveryErr)
	}
}

func TestSwitchApplyRequiresExpectedPlanFingerprint(t *testing.T) {
	service := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil).Switch

	_, err := service.Apply(context.Background(), SwitchApplyRequest{
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.ConfirmationRequired {
		t.Fatalf("expected missing fingerprint to fail with confirmation error, got %v", err)
	}
}

func assertDesktopServiceErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}

func TestAntigravityServiceCreatesSafeDashboardProfile(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	ctx := context.Background()
	configDir := t.TempDir()
	payload := `{"token":{"access_token":"desktop-access-secret","token_type":"Bearer","refresh_token":"desktop-refresh-secret","expiry":"2026-07-12T04:00:00Z"},"auth_method":"consumer"}`
	if err := keyring.Set(agyconfig.KeyringService, agyconfig.KeyringAccount, payload); err != nil {
		t.Fatalf("expected mock keyring setup to succeed, got %v", err)
	}
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) { events = append(events, event) })
	created, err := services.Antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected Antigravity create to succeed, got %v", err)
	}
	if !created.Summary.Active || created.Summary.Profile.ID != "work" {
		t.Fatalf("unexpected Antigravity create result: %#v", created)
	}
	if len(events) != 1 || events[0].Kind != DesktopChangeAntigravityProfileChanged || !events[0].ProfileChanged || !events[0].ActiveStateChanged {
		t.Fatalf("expected Antigravity desktop change event, got %#v", events)
	}
	dashboard, err := services.App.Dashboard(ctx)
	if err != nil {
		t.Fatalf("expected dashboard to succeed, got %v", err)
	}
	if dashboard.AntigravityProfiles == nil || len(dashboard.AntigravityProfiles.Profiles) != 1 {
		t.Fatalf("expected Antigravity dashboard Profile, got %#v", dashboard.AntigravityProfiles)
	}
	raw, err := json.Marshal(dashboard.AntigravityProfiles)
	if err != nil {
		t.Fatalf("expected dashboard JSON, got %v", err)
	}
	for _, secret := range []string{"desktop-access-secret", "desktop-refresh-secret", "access_token", "refresh_token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("expected Antigravity dashboard to hide %q, got %s", secret, raw)
		}
	}
}

func TestSettingsServicePersistsDesktopPreferences(t *testing.T) {
	ctx := context.Background()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}

	initial, err := services.Settings.Get(ctx)
	if err != nil {
		t.Fatalf("expected settings get to succeed, got %v", err)
	}
	if initial.Language != settings.DesktopLanguageAuto || initial.Appearance != settings.DesktopAppearanceSystem || initial.SidebarCollapsed || !initial.AutomaticUpdates {
		t.Fatalf("unexpected default settings: %#v", initial)
	}

	language := settings.DesktopLanguageEnUS
	appearance := settings.DesktopAppearanceDark
	collapsed := true
	updated, err := services.Settings.Update(ctx, settings.UpdateRequest{
		Language:         &language,
		Appearance:       &appearance,
		SidebarCollapsed: &collapsed,
	})
	if err != nil {
		t.Fatalf("expected settings update to succeed, got %v", err)
	}
	if updated.Language != settings.DesktopLanguageEnUS || updated.Appearance != settings.DesktopAppearanceDark || !updated.SidebarCollapsed {
		t.Fatalf("unexpected language update: %#v", updated)
	}
}

func TestCodexSettingsServiceKeepsConcurrentUsageIntervalUpdatesConsistent(t *testing.T) {
	ctx := context.Background()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("expected initialize to succeed, got %v", err)
	}

	start := make(chan struct{})
	errorsByUpdate := make(chan error, 4)
	var wg sync.WaitGroup
	for _, interval := range []int{5, 15, 30, 60} {
		interval := interval
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := services.Codex.UpdateSettings(ctx, codex.UpdateCodexSettingsRequest{UsageSyncIntervalSeconds: &interval})
			errorsByUpdate <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsByUpdate)
	for err := range errorsByUpdate {
		if err != nil {
			t.Fatalf("expected concurrent interval update to succeed, got %v", err)
		}
	}

	persisted, err := services.Codex.GetSettings(ctx)
	if err != nil {
		t.Fatalf("expected settings reload to succeed, got %v", err)
	}
	runtime, err := services.Usage.AutoSyncStatus(ctx)
	if err != nil {
		t.Fatalf("expected runtime status to succeed, got %v", err)
	}
	if runtime.IntervalSeconds != persisted.UsageSyncIntervalSeconds {
		t.Fatalf("expected persisted and runtime intervals to match, persisted=%#v runtime=%#v", persisted, runtime)
	}
}

func TestServicesNotifyDesktopChanges(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir}, nil)
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

func TestApplicationBackupMutationsNotifyDesktopChanges(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	ctx := context.Background()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) { events = append(events, event) })

	backup, err := services.Backup.Create(ctx)
	if err != nil {
		t.Fatalf("create application backup: %v", err)
	}
	if len(events) != 1 || events[0].Kind != DesktopChangeApplicationBackupChanged ||
		events[0].Source != "backup.create" || events[0].Status != DesktopChangeStatusSuccess {
		t.Fatalf("unexpected backup creation event: %#v", events)
	}
	if err := services.Backup.Delete(ctx, appbackup.DeleteRequest{BackupID: backup.ID, Confirm: true}); err != nil {
		t.Fatalf("delete application backup: %v", err)
	}
	if len(events) != 2 || events[1].Kind != DesktopChangeApplicationBackupChanged ||
		events[1].Source != "backup.delete" || events[1].Status != DesktopChangeStatusSuccess {
		t.Fatalf("unexpected backup deletion event: %#v", events)
	}
}

func TestApplicationBackupRestoreRequestsDesktopRestart(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	ctx := context.Background()
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
	if _, err := services.App.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	backup, err := services.Backup.Create(ctx)
	if err != nil {
		t.Fatalf("create application backup: %v", err)
	}
	preview, err := services.Backup.PreviewRestore(ctx, appbackup.RestoreSource{BackupID: backup.ID})
	if err != nil {
		t.Fatalf("preview application restore: %v", err)
	}
	restarted := false
	ConfigureBackupRestarter(services.Backup, func() error {
		restarted = true
		return nil
	})

	result, err := services.Backup.Restore(ctx, appbackup.RestoreRequest{
		Source: appbackup.RestoreSource{BackupID: backup.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("restore application backup: %v", err)
	}
	if !result.RestartRequired || !restarted {
		t.Fatalf("desktop restart was not requested: result=%#v restarted=%t", result, restarted)
	}

	preview, err = services.Backup.PreviewRestore(ctx, appbackup.RestoreSource{BackupID: backup.ID})
	if err != nil {
		t.Fatalf("preview second application restore: %v", err)
	}
	ConfigureBackupRestarter(services.Backup, func() error { return errors.New("restart unavailable") })
	_, err = services.Backup.Restore(ctx, appbackup.RestoreRequest{
		Source: appbackup.RestoreSource{BackupID: backup.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	})
	assertDesktopServiceErrorCode(t, err, apperror.ApplicationRestartFailed)
}

func TestSwitchApplyMissingFingerprintDoesNotNotify(t *testing.T) {
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: t.TempDir()}, nil)
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
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir}, nil)
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
		event.Error == nil ||
		!event.ProfileChanged || !event.ConfigSetsChanged || !event.ActiveStateChanged {
		t.Fatalf("unexpected failed switch event: %#v", event)
	}
}

func TestDashboardHonorsCanceledContext(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir}, nil).App.Dashboard(canceled)
	if err == nil {
		t.Fatalf("expected dashboard to return a cancellation error")
	}
}

func TestFormatDesktopError(t *testing.T) {
	err := apperror.New(apperror.RuntimeInitFailed, "runtime failed").WithDetail("path", "/tmp/profiledeck")

	result := FormatDesktopError(err)
	if result.Code != string(apperror.RuntimeInitFailed) || result.Message != "runtime failed" || result.Details["path"] != "/tmp/profiledeck" {
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
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	rawSecret := "raw-access-token"
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"work","access_token":"`+rawSecret+`"}}`), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
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
	if err := Bootstrap(context.Background(), newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
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
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
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
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	accessToken := "desktop-list-access-token"
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"work-account","access_token":"`+accessToken+`"}}`), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
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

func TestCodexProfileTransferServicesUseSharedCoreAndNotifyImport(t *testing.T) {
	ctx := context.Background()
	sourceConfigDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: sourceConfigDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected source bootstrap to succeed, got %v", err)
	}
	rawSecret := "desktop-transfer-test-value"
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-test"`+"\n", `{"tokens":{"account_id":"work","access_token":"`+rawSecret+`"}}`)
	source := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: sourceConfigDir, CodexDir: codexDir}, nil)
	if _, err := source.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected source profile create, got %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), "profiles.json")
	exported, err := source.Codex.ExportProfiles(ctx, ExportCodexProfilesRequest{OutputPath: bundlePath})
	if err != nil || exported.ProfileCount != 1 {
		t.Fatalf("expected desktop export service, result=%#v err=%v", exported, err)
	}
	exportedJSON, _ := json.Marshal(exported)
	if strings.Contains(string(exportedJSON), rawSecret) {
		t.Fatalf("expected desktop export DTO to remain metadata-only, got %s", exportedJSON)
	}

	targetConfigDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: targetConfigDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected target bootstrap to succeed, got %v", err)
	}
	target := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: targetConfigDir, CodexDir: codexDir}, nil)
	events := []DesktopChangeEvent{}
	target.SubscribeChanges(func(event DesktopChangeEvent) { events = append(events, event) })
	plan, err := target.Codex.InspectProfileImport(ctx, bundlePath)
	if err != nil || !plan.CanApply {
		t.Fatalf("expected desktop import inspection, plan=%#v err=%v", plan, err)
	}
	planJSON, _ := json.Marshal(plan)
	if strings.Contains(string(planJSON), rawSecret) {
		t.Fatalf("expected desktop import plan to remain metadata-only, got %s", planJSON)
	}
	result, err := target.Codex.ApplyProfileImport(ctx, ApplyCodexProfileImportRequest{
		InputPath: bundlePath, ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil || !result.Changed {
		t.Fatalf("expected desktop import apply, result=%#v err=%v", result, err)
	}
	if len(events) != 1 || events[0].Kind != DesktopChangeCodexProfileChanged || events[0].Source != "codex.importProfiles" ||
		!events[0].ProfileChanged || !events[0].ConfigSetsChanged || events[0].ActiveStateChanged {
		t.Fatalf("expected imported profile/config event without active-state change, got %#v", events)
	}
}

func TestCodexSaveActiveProfileStateReadsCurrentFilesBehindDesktopBoundary(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"work","access_token":"initial"}}`)

	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}

	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5.1-codex"`+"\n", `{"tokens":{"account_id":"updated","access_token":"changed"}}`)
	if _, err := services.Codex.SaveActiveProfileState(ctx); err != nil {
		t.Fatalf("expected save-current to read current Codex files, got %v", err)
	}

	detail, err := services.Codex.ShowProfile(ctx, "work")
	if err != nil {
		t.Fatalf("expected saved profile detail, got %v", err)
	}
	if detail.Summary.Model != "gpt-5.1-codex" || detail.Summary.CodexAccountID != "updated" {
		t.Fatalf("expected disk state to be synced, got %#v", detail.Summary)
	}
}

func TestCodexUpdateProfileMetadataPersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"work","access_token":"initial"}}`)

	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
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
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.ProfileInvalid {
		t.Fatalf("expected metadata validation error, got %v", err)
	}
	if len(events) != 2 || events[1].Status != DesktopChangeStatusFailure || events[1].Error == nil || events[1].Error.Code != string(apperror.ProfileInvalid) {
		t.Fatalf("expected failed metadata notification, got %#v", events)
	}
}

func TestCodexConfigSetServiceCRUDAndNotifications(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir, CodexDir: codexDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	writeDesktopCodexFiles(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"shared","access_token":"initial"}}`)

	services := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir, CodexDir: codexDir}, nil)
	if _, err := services.Codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	events := []DesktopChangeEvent{}
	services.SubscribeChanges(func(event DesktopChangeEvent) { events = append(events, event) })
	created, err := services.Codex.CreateConfigSet(ctx, CreateCodexConfigSetRequest{ConfigSetID: "other", Name: "Other"})
	if err != nil || created.ID != "other" {
		t.Fatalf("expected Config Set create, got %#v err=%v", created, err)
	}
	name := "Renamed"
	updated, err := services.Codex.UpdateConfigSet(ctx, UpdateCodexConfigSetRequest{ConfigSetID: "other", Name: &name})
	if err != nil || updated.Name != name {
		t.Fatalf("expected Config Set update, got %#v err=%v", updated, err)
	}
	list, err := services.Codex.ListConfigSets(ctx)
	if err != nil || len(list.ConfigSets) != 2 {
		t.Fatalf("expected two Config Sets, got %#v err=%v", list, err)
	}
	dashboard, err := services.App.Dashboard(ctx)
	if err != nil || dashboard.CodexConfigSets == nil || len(dashboard.CodexConfigSets.ConfigSets) != 2 {
		t.Fatalf("expected dashboard Config Sets, got %#v err=%v", dashboard.CodexConfigSets, err)
	}
	if err := services.Codex.DeleteConfigSet(ctx, "other"); err != nil {
		t.Fatalf("expected Config Set delete, got %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected create/update/delete notifications, got %#v", events)
	}
	for _, event := range events {
		if event.Kind != DesktopChangeCodexConfigSetChanged || event.Status != DesktopChangeStatusSuccess || !event.ConfigSetsChanged || event.ProfileChanged || event.ActiveStateChanged {
			t.Fatalf("unexpected Config Set event: %#v", event)
		}
	}
}

func TestUsageReportServiceDefaultsAndValidatesRange(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if err := Bootstrap(ctx, newBackendTestApplication(t, Environment{ConfigDir: configDir})); err != nil {
		t.Fatalf("expected bootstrap to succeed, got %v", err)
	}
	service := newTestServices(t, app.DefaultInfo(), Environment{ConfigDir: configDir}, nil).Usage

	report, err := service.Report(ctx, "", "")
	if err != nil {
		t.Fatalf("expected default usage report, got %v", err)
	}
	if report.ProviderID != codexconfig.ProviderID || report.Range.Preset != usage.UsageRange7Days || len(report.Trend) != 7 {
		t.Fatalf("unexpected default usage report: %#v", report)
	}
	if _, err := service.Report(ctx, codexconfig.ProviderID, "14d"); err == nil {
		t.Fatalf("expected invalid usage report range to fail")
	}
}

func writeDesktopCodexFiles(t *testing.T, codexDir, config, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("expected config fixture to write, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("expected auth fixture to write, got %v", err)
	}
}
