package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	keyring "github.com/zalando/go-keyring"

	coreapp "github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
)

func TestServiceDisablesUnconfiguredBuilds(t *testing.T) {
	application := newUpdateTestApplication(t)
	for _, config := range []BuildConfig{
		{CurrentVersion: "dev"},
		{CurrentVersion: "not-semver"},
		{CurrentVersion: "0.1.0-alpha.1"},
		{CurrentVersion: "0.1.0-beta.0"},
	} {
		status := NewService(context.Background(), application, config).Status(context.Background())
		if status.Configured || status.State != StateUnavailable {
			t.Fatalf("build should be unavailable: config=%#v status=%#v", config, status)
		}
	}
}

func TestServiceConfiguresStableAndPrereleaseBuilds(t *testing.T) {
	for _, test := range []struct {
		version string
		channel string
	}{
		{version: "0.1.0", channel: ChannelStable},
		{version: "0.1.0-beta.1", channel: ChannelBeta},
	} {
		application := newUpdateTestApplication(t)
		version := test.version
		status := NewService(context.Background(), application, BuildConfig{CurrentVersion: version}).Status(context.Background())
		if !status.Configured || status.State != StateIdle || status.Channel != test.channel {
			t.Fatalf("build should be configured: version=%q status=%#v", version, status)
		}
	}
}

func TestServicePersistsChannelInsteadOfReplacingItFromBuildVersion(t *testing.T) {
	ctx := context.Background()
	application := newUpdateTestApplication(t)
	service := NewService(ctx, application, BuildConfig{CurrentVersion: "0.1.0-beta.1"})
	if status := service.Status(ctx); status.Channel != ChannelBeta {
		t.Fatalf("beta build default channel = %q", status.Channel)
	}
	status, err := service.SetChannel(ctx, ChannelStable)
	if err != nil || status.Channel != ChannelStable {
		t.Fatalf("switch stable: status=%#v err=%v", status, err)
	}
	restarted := NewService(ctx, application, BuildConfig{CurrentVersion: "0.2.0-beta.1"})
	if status := restarted.Status(ctx); status.Channel != ChannelStable {
		t.Fatalf("new beta build replaced persisted channel: %#v", status)
	}
}

func TestServiceSwitchesChannelOnlyWhileUpdateIsIdle(t *testing.T) {
	ctx := context.Background()
	service, engine := newUpdateTestService(t, time.Hour)
	status, err := service.SetChannel(ctx, ChannelStable)
	if err != nil || status.Channel != ChannelStable {
		t.Fatalf("switch stable: status=%#v err=%v", status, err)
	}
	status.LastCheckedAtUnixMS = 123
	service.status = status
	status, err = service.SetChannel(ctx, ChannelBeta)
	if err != nil {
		t.Fatalf("switch beta: %v", err)
	}
	if status.Channel != ChannelBeta ||
		status.State != StateIdle ||
		status.LastCheckedAtUnixMS != 0 ||
		status.AvailableVersion != "" {
		t.Fatalf("channel switch did not reset channel-specific state: %#v", status)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	engine.check = func() (*updater.Release, error) {
		close(started)
		<-release
		return nil, nil
	}
	done := make(chan UpdateStatus, 1)
	go func() { done <- service.CheckAndDownload(ctx) }()
	<-started
	_, err = service.SetChannel(ctx, ChannelStable)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.UpdateChannelBusy {
		t.Fatalf("channel switch during check error = %v", err)
	}
	close(release)
	<-done
	if status := service.Status(ctx); status.Channel != ChannelBeta {
		t.Fatalf("busy channel switch changed provider: %#v", status)
	}

	service.status.State = StateReady
	_, err = service.SetChannel(ctx, ChannelStable)
	if !errors.As(err, &appErr) || appErr.Code != apperror.UpdateChannelBusy {
		t.Fatalf("channel switch with ready update error = %v", err)
	}
}

func TestServiceCheckStateMachineAndReadyState(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	var states []string
	service.emit = func(status UpdateStatus) { states = append(states, status.State) }
	engine.release = &updater.Release{
		Version:  "0.1.0-beta.2",
		Artifact: updater.Artifact{Filename: "ProfileDeck_0.1.0-beta.2_macos_universal.zip", Size: 128},
	}
	engine.download = func() error {
		service.setStatus(func(status *UpdateStatus) {
			status.State = StateDownloading
			status.DownloadedBytes = 64
			status.TotalBytes = 128
		})
		service.setState(StateVerifying)
		service.setState(StatePreparing)
		return nil
	}

	status := service.CheckAndDownload(context.Background())
	if status.State != StateReady || status.AvailableVersion != "0.1.0-beta.2" || status.DownloadedBytes != 128 {
		t.Fatalf("unexpected ready status: %#v", status)
	}
	wantStates := []string{StateChecking, StateDownloading, StateVerifying, StatePreparing, StateReady}
	states = compactStates(states)
	if len(states) != len(wantStates) {
		t.Fatalf("state sequence = %#v, want %#v", states, wantStates)
	}
	for index := range wantStates {
		if states[index] != wantStates[index] {
			t.Fatalf("state sequence = %#v, want %#v", states, wantStates)
		}
	}

	service.CheckAndDownload(context.Background())
	if engine.checks.Load() != 1 {
		t.Fatalf("ready update should be retained until restart, checks=%d", engine.checks.Load())
	}
}

func TestServiceSerialisesConcurrentChecksAndAllowsRetry(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	started := make(chan struct{})
	release := make(chan struct{})
	engine.check = func() (*updater.Release, error) {
		close(started)
		<-release
		return nil, updateError(ErrorFeedUnavailable, errors.New("offline"))
	}
	done := make(chan UpdateStatus, 1)
	go func() { done <- service.CheckAndDownload(context.Background()) }()
	<-started
	concurrent := service.CheckAndDownload(context.Background())
	if concurrent.State != StateChecking || engine.checks.Load() != 1 {
		t.Fatalf("concurrent check was not coalesced: status=%#v checks=%d", concurrent, engine.checks.Load())
	}
	close(release)
	failed := <-done
	if failed.State != StateError || failed.ErrorCode != ErrorFeedUnavailable {
		t.Fatalf("unexpected failure status: %#v", failed)
	}

	engine.check = nil
	retried := service.CheckAndDownload(context.Background())
	if retried.State != StateUpToDate || engine.checks.Load() != 2 {
		t.Fatalf("retry did not complete: status=%#v checks=%d", retried, engine.checks.Load())
	}
}

func TestServiceReportsArtifactVerificationFailure(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	engine.release = &updater.Release{
		Version:  "0.1.0-beta.2",
		Artifact: updater.Artifact{Filename: "ProfileDeck_0.1.0-beta.2_macos_universal.zip", Size: 128},
	}
	engine.download = func() error {
		service.setState(StateVerifying)
		return errors.New("digest mismatch")
	}

	status := service.CheckAndDownload(context.Background())
	if status.State != StateError || status.ErrorCode != ErrorArtifactVerificationFailed {
		t.Fatalf("unexpected verification failure status: %#v", status)
	}
}

func TestServiceSchedulerStartsImmediatelyRunsPeriodicallyAndStopsWhenDisabled(t *testing.T) {
	service, engine := newUpdateTestService(t, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Start(ctx, service)
	waitFor(t, time.Second, func() bool { return engine.checks.Load() >= 2 })

	status := service.SetAutomatic(context.Background(), false)
	if status.Automatic {
		t.Fatalf("automatic updates remained enabled: %#v", status)
	}
	time.Sleep(35 * time.Millisecond)
	stoppedAt := engine.checks.Load()
	time.Sleep(60 * time.Millisecond)
	if engine.checks.Load() != stoppedAt {
		t.Fatalf("scheduler continued while disabled: before=%d after=%d", stoppedAt, engine.checks.Load())
	}

	status = service.SetAutomatic(context.Background(), true)
	if !status.Automatic {
		t.Fatalf("automatic updates remained disabled: %#v", status)
	}
	waitFor(t, time.Second, func() bool { return engine.checks.Load() > stoppedAt })
	Stop(service)

	persisted, err := service.application.Settings().Get(context.Background())
	if err != nil || !persisted.AutomaticUpdates {
		t.Fatalf("automatic setting was not persisted: settings=%#v err=%v", persisted, err)
	}
}

func TestRestartCreatesEncryptedApplicationBackup(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	root := t.TempDir()
	bundle := filepath.Join(root, "Applications", "ProfileDeck.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "profiledeck-desktop")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	service.executable = func() (string, error) { return executable, nil }
	engine.downloadedPath = filepath.Join(os.TempDir(), "wails-update-test", "ProfileDeck.app")
	service.status.State = StateReady
	service.status.AvailableVersion = "0.1.0-beta.2"

	if err := service.Restart(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if engine.restarts.Load() != 1 {
		t.Fatalf("expected one updater restart, got %d", engine.restarts.Load())
	}
	list, err := service.application.Backups().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Backups) != 1 || list.Backups[0].Kind != appbackup.KindAutomatic {
		t.Fatalf("unexpected application backups after update restart: %#v", list.Backups)
	}
	detail, err := service.application.Backups().Show(context.Background(), list.Backups[0].ID)
	if err != nil || detail.Reason != appbackup.ReasonBeforeUpdate {
		t.Fatalf("unexpected update backup detail: %#v error=%v", detail, err)
	}
	info, err := os.Stat(filepath.Join(service.application.Runtime().Paths().Backups, detail.ID+appbackup.Extension))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("application backup is not private: info=%#v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(service.application.Runtime().Paths().Root, "updates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy update backup directory exists: %v", err)
	}
}

func TestRestartStopsBeforeUpdaterWhenSnapshotFails(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	bundle := filepath.Join(t.TempDir(), "ProfileDeck.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "profiledeck-desktop")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	service.executable = func() (string, error) { return executable, nil }
	service.status.State = StateReady
	service.status.AvailableVersion = "0.1.0-beta.2"
	engine.downloadedPath = "staged"
	if err := os.Remove(service.application.Runtime().Paths().Database); err != nil {
		t.Fatal(err)
	}

	if err := service.Restart(context.Background()); err == nil {
		t.Fatal("expected snapshot failure to block restart")
	}
	if engine.restarts.Load() != 0 {
		t.Fatalf("updater restarted without a snapshot: %d", engine.restarts.Load())
	}
	if service.Status(context.Background()).State != StateReady {
		t.Fatalf("ready update should remain retryable: %#v", service.Status(context.Background()))
	}
}

func TestRestartRejectsConcurrentAndRepeatedAttempts(t *testing.T) {
	service, engine := newUpdateTestService(t, time.Hour)
	bundle := filepath.Join(t.TempDir(), "ProfileDeck.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "profiledeck-desktop")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	service.executable = func() (string, error) { return executable, nil }
	service.status.State = StateReady
	service.status.AvailableVersion = "0.1.0-beta.2"
	engine.downloadedPath = "staged"

	started := make(chan struct{})
	release := make(chan struct{})
	engine.restart = func() error {
		close(started)
		<-release
		return nil
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- service.Restart(context.Background()) }()
	<-started
	assertUpdateNotReady(t, service.Restart(context.Background()))
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first restart: %v", err)
	}
	assertUpdateNotReady(t, service.Restart(context.Background()))
	if engine.restarts.Load() != 1 {
		t.Fatalf("updater restart count = %d, want 1", engine.restarts.Load())
	}
}

type fakeUpdateEngine struct {
	checks         atomic.Int64
	restarts       atomic.Int64
	mu             sync.Mutex
	release        *updater.Release
	check          func() (*updater.Release, error)
	download       func() error
	restart        func() error
	downloadedPath string
}

func (engine *fakeUpdateEngine) Check(context.Context) (*updater.Release, error) {
	engine.checks.Add(1)
	engine.mu.Lock()
	check, release := engine.check, engine.release
	engine.mu.Unlock()
	if check != nil {
		return check()
	}
	return release, nil
}

func (engine *fakeUpdateEngine) DownloadAndInstall(context.Context) error {
	engine.mu.Lock()
	download := engine.download
	engine.mu.Unlock()
	if download != nil {
		return download()
	}
	return nil
}

func (engine *fakeUpdateEngine) Restart(context.Context) error {
	engine.restarts.Add(1)
	engine.mu.Lock()
	restart := engine.restart
	engine.mu.Unlock()
	if restart != nil {
		return restart()
	}
	return nil
}

func (engine *fakeUpdateEngine) DownloadedPath() string { return engine.downloadedPath }

func newUpdateTestService(t *testing.T, interval time.Duration) (*Service, *fakeUpdateEngine) {
	t.Helper()
	service := NewService(context.Background(), newUpdateTestApplication(t), BuildConfig{
		CurrentVersion: "0.1.0-beta.1",
		CheckInterval:  interval,
	})
	engine := &fakeUpdateEngine{}
	service.engine = engine
	return service, engine
}

func newUpdateTestApplication(t *testing.T) *coreapp.Application {
	t.Helper()
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	application, err := coreapp.New(coreapp.Config{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Runtime().Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	return application
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func compactStates(states []string) []string {
	compacted := make([]string, 0, len(states))
	for _, state := range states {
		if len(compacted) == 0 || compacted[len(compacted)-1] != state {
			compacted = append(compacted, state)
		}
	}
	return compacted
}

func assertUpdateNotReady(t *testing.T, err error) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.UpdateNotReady {
		t.Fatalf("error = %v, want %s", err, apperror.UpdateNotReady)
	}
}
