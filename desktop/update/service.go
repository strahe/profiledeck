package update

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"

	coreapp "github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
)

const (
	StatusEventName = "profiledeck:update-status"

	StateUnavailable = "unavailable"
	StateIdle        = "idle"
	StateChecking    = "checking"
	StateUpToDate    = "up_to_date"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StatePreparing   = "preparing"
	StateReady       = "ready"
	StateError       = "error"

	defaultCheckInterval = 6 * time.Hour
)

type UpdateStatus struct {
	Revision            uint64 `json:"revision"`
	Configured          bool   `json:"configured"`
	Automatic           bool   `json:"automatic"`
	Channel             string `json:"channel"`
	State               string `json:"state"`
	CurrentVersion      string `json:"current_version"`
	AvailableVersion    string `json:"available_version"`
	DownloadedBytes     int64  `json:"downloaded_bytes"`
	TotalBytes          int64  `json:"total_bytes"`
	LastCheckedAtUnixMS int64  `json:"last_checked_at_unix_ms"`
	ErrorCode           string `json:"error_code"`
}

type BuildConfig struct {
	CurrentVersion string
	CheckInterval  time.Duration
}

type updateEngine interface {
	Check(context.Context) (*updater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
	DownloadedPath() string
}

type Service struct {
	application *coreapp.Application
	provider    *channelGitHubProvider
	interval    time.Duration
	now         func() time.Time
	executable  func() (string, error)

	mu         sync.RWMutex
	status     UpdateStatus
	engine     updateEngine
	emit       func(UpdateStatus)
	dispose    []func()
	restarting bool

	checkMu   sync.Mutex
	restartMu sync.Mutex
	startMu   sync.Mutex
	cancel    context.CancelFunc
	wake      chan struct{}
	done      chan struct{}
	workWG    sync.WaitGroup
}

func NewService(ctx context.Context, application *coreapp.Application, config BuildConfig) *Service {
	interval := config.CheckInterval
	if interval <= 0 {
		interval = defaultCheckInterval
	}
	service := &Service{
		application: application,
		interval:    interval,
		now:         time.Now,
		executable:  os.Executable,
		status: UpdateStatus{
			Automatic: true, State: StateUnavailable, CurrentVersion: strings.TrimSpace(config.CurrentVersion),
		},
	}
	if application == nil {
		return service
	}
	provider, err := newGitHubProvider(config.CurrentVersion, githubProviderOptions{})
	if err != nil {
		return service
	}
	channel := provider.Channel()
	if settings, err := application.Settings().EnsureUpdateChannel(ctx, channel); err == nil {
		channel = settings.UpdateChannel
		_ = provider.SetChannel(channel)
	}
	service.provider = provider
	service.status.Configured = true
	service.status.Channel = channel
	service.status.State = StateIdle
	return service
}

func Attach(service *Service, wailsApp *application.App) error {
	if wailsApp == nil || !service.Status(context.Background()).Configured {
		return nil
	}
	if err := wailsApp.Updater.Init(updater.Config{
		CurrentVersion: service.status.CurrentVersion,
		Providers:      []updater.Provider{service.provider},
		Platform:       UpdatePlatform,
		Window:         updater.WindowNone,
	}); err != nil {
		service.setStatus(func(status *UpdateStatus) {
			status.Configured = false
			status.State = StateUnavailable
			status.ErrorCode = "configuration_invalid"
		})
		return err
	}
	service.mu.Lock()
	service.engine = wailsApp.Updater
	service.emit = func(status UpdateStatus) { wailsApp.Event.Emit(StatusEventName, status) }
	service.mu.Unlock()

	service.dispose = []func(){
		wailsApp.Event.On(updater.EventDownloadProgress, service.handleProgress),
		wailsApp.Event.On(updater.EventVerifying, func(*application.CustomEvent) {
			service.setState(StateVerifying)
		}),
		wailsApp.Event.On(updater.EventInstalling, func(*application.CustomEvent) {
			service.setState(StatePreparing)
		}),
		wailsApp.Event.On(updater.EventUpdateReady, func(event *application.CustomEvent) {
			service.setStatus(func(status *UpdateStatus) {
				status.State = StateReady
				status.ErrorCode = ""
				if release, ok := event.Data.(*updater.Release); ok && release != nil {
					status.AvailableVersion = release.Version
				}
			})
		}),
	}
	service.publish()
	return nil
}

func Start(parent context.Context, service *Service) {
	service.startMu.Lock()
	defer service.startMu.Unlock()
	if service.cancel != nil {
		return
	}
	if service.application == nil || !service.Status(parent).Configured {
		return
	}
	settings, err := service.application.Settings().Get(parent)
	if err != nil {
		service.setStatus(func(status *UpdateStatus) {
			status.State = StateError
			status.ErrorCode = "settings_unavailable"
		})
		return
	}
	if service.provider != nil {
		if err := service.provider.SetChannel(settings.UpdateChannel); err != nil {
			service.setStatus(func(status *UpdateStatus) {
				status.State = StateError
				status.ErrorCode = "settings_unavailable"
			})
			return
		}
	}
	service.setStatus(func(status *UpdateStatus) {
		status.Automatic = settings.AutomaticUpdates
		status.Channel = settings.UpdateChannel
	})
	ctx, cancel := context.WithCancel(parent)
	service.cancel = cancel
	service.wake = make(chan struct{}, 1)
	service.done = make(chan struct{})
	go service.schedule(ctx)
}

func Stop(service *Service) {
	service.startMu.Lock()
	cancel, done := service.cancel, service.done
	service.cancel = nil
	service.done = nil
	service.startMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	service.workWG.Wait()
	for _, dispose := range service.dispose {
		dispose()
	}
	service.dispose = nil

	service.mu.RLock()
	engine, restarting := service.engine, service.restarting
	service.mu.RUnlock()
	if engine != nil && !restarting {
		removeWailsStaging(engine.DownloadedPath())
	}
}

func (service *Service) Status(context.Context) UpdateStatus {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.status
}

func (service *Service) CheckAndDownload(ctx context.Context) UpdateStatus {
	return service.checkAndDownload(ctx)
}

func (service *Service) SetAutomatic(ctx context.Context, enabled bool) UpdateStatus {
	settings, err := service.application.Settings().SetAutomaticUpdates(ctx, enabled)
	if err != nil {
		service.setStatus(func(status *UpdateStatus) { status.ErrorCode = "settings_unavailable" })
		return service.Status(ctx)
	}
	wasEnabled := service.Status(ctx).Automatic
	service.setStatus(func(status *UpdateStatus) {
		status.Automatic = settings.AutomaticUpdates
		status.ErrorCode = ""
	})
	if !wasEnabled && settings.AutomaticUpdates {
		service.signalScheduler()
	}
	return service.Status(ctx)
}

func (service *Service) SetChannel(ctx context.Context, channel string) (UpdateStatus, error) {
	channel, err := normalizeChannel(channel)
	if err != nil {
		return service.Status(ctx), apperror.New(apperror.SettingInvalid, "Unsupported update channel")
	}
	current := service.Status(ctx)
	if channel == current.Channel {
		return current, nil
	}
	if !service.checkMu.TryLock() {
		return current, apperror.New(apperror.UpdateChannelBusy, "Wait for the current update to finish")
	}
	service.mu.RLock()
	state, provider := service.status.State, service.provider
	service.mu.RUnlock()
	if !channelCanChange(state) || provider == nil {
		service.checkMu.Unlock()
		return service.Status(ctx), apperror.New(apperror.UpdateChannelBusy, "Wait for the current update to finish")
	}
	settings, err := service.application.Settings().SetUpdateChannel(ctx, channel)
	if err != nil {
		service.checkMu.Unlock()
		service.setStatus(func(status *UpdateStatus) { status.ErrorCode = "settings_unavailable" })
		return service.Status(ctx), nil
	}
	if err := provider.SetChannel(settings.UpdateChannel); err != nil {
		service.checkMu.Unlock()
		return service.Status(ctx), apperror.New(apperror.SettingInvalid, "Unsupported update channel")
	}
	service.setStatus(func(status *UpdateStatus) {
		status.Channel = settings.UpdateChannel
		status.State = StateIdle
		status.AvailableVersion = ""
		status.DownloadedBytes = 0
		status.TotalBytes = 0
		status.LastCheckedAtUnixMS = 0
		status.ErrorCode = ""
	})
	automatic := service.Status(ctx).Automatic
	service.checkMu.Unlock()
	if automatic {
		service.signalScheduler()
	}
	return service.Status(ctx), nil
}

func (service *Service) Restart(ctx context.Context) error {
	if !service.restartMu.TryLock() {
		return apperror.New(apperror.UpdateNotReady, "An update restart is already in progress")
	}
	defer service.restartMu.Unlock()

	service.mu.RLock()
	status, engine, restarting := service.status, service.engine, service.restarting
	service.mu.RUnlock()
	if restarting || status.State != StateReady || engine == nil || engine.DownloadedPath() == "" {
		return apperror.New(apperror.UpdateNotReady, "No update is ready to restart")
	}

	err := service.application.Switching().RunWithSharedLock(ctx, "desktop-update", func(lockContext context.Context) error {
		bundle, err := service.applicationBundle()
		if err != nil {
			return err
		}
		if err := verifyParentWritable(filepath.Dir(bundle)); err != nil {
			return err
		}
		// A verified encrypted application backup is a restart gate: never swap
		// the app without a recoverable copy of ProfileDeck data.
		if _, err := service.application.Backups().Create(lockContext, appbackup.CreateRequest{
			Kind: appbackup.KindAutomatic, Reason: appbackup.ReasonBeforeUpdate,
		}); err != nil {
			return err
		}
		service.mu.Lock()
		service.restarting = true
		service.mu.Unlock()
		return engine.Restart(lockContext)
	})
	if err != nil {
		service.mu.Lock()
		service.restarting = false
		service.mu.Unlock()
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			log.Printf("profiledeck: update restart failed: %s", appErr.Code)
		} else {
			log.Printf("profiledeck: update restart failed")
		}
		service.setStatus(func(status *UpdateStatus) {
			status.State = StateReady
			status.ErrorCode = "restart_failed"
		})
		return apperror.Wrap(apperror.UpdateRestartFailed, "ProfileDeck could not restart with the update", err)
	}
	return nil
}

func (service *Service) schedule(ctx context.Context) {
	defer close(service.done)
	if service.Status(ctx).Automatic {
		service.runScheduledCheck(ctx)
	}
	ticker := time.NewTicker(service.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if service.Status(ctx).Automatic {
				service.runScheduledCheck(ctx)
			}
		case <-service.wake:
			if service.Status(ctx).Automatic {
				service.runScheduledCheck(ctx)
			}
		}
	}
}

func (service *Service) runScheduledCheck(ctx context.Context) {
	service.workWG.Add(1)
	go func() {
		defer service.workWG.Done()
		service.checkAndDownload(ctx)
	}()
}

func (service *Service) checkAndDownload(ctx context.Context) UpdateStatus {
	if !service.checkMu.TryLock() {
		return service.Status(ctx)
	}
	defer service.checkMu.Unlock()
	service.mu.RLock()
	engine, configured, ready := service.engine, service.status.Configured, service.status.State == StateReady
	service.mu.RUnlock()
	if !configured || engine == nil || ready {
		return service.Status(ctx)
	}

	service.setStatus(func(status *UpdateStatus) {
		status.State = StateChecking
		status.AvailableVersion = ""
		status.DownloadedBytes = 0
		status.TotalBytes = 0
		status.ErrorCode = ""
	})
	release, err := engine.Check(ctx)
	checkedAtUnixMS := service.now().UnixMilli()
	if err != nil {
		service.failCheck(err, checkedAtUnixMS)
		return service.Status(ctx)
	}
	if release == nil {
		service.setStatus(func(status *UpdateStatus) {
			status.State = StateUpToDate
			status.LastCheckedAtUnixMS = checkedAtUnixMS
		})
		return service.Status(ctx)
	}
	service.setStatus(func(status *UpdateStatus) {
		status.State = StateDownloading
		status.AvailableVersion = release.Version
		status.TotalBytes = release.Artifact.Size
		status.LastCheckedAtUnixMS = checkedAtUnixMS
	})
	if err := engine.DownloadAndInstall(ctx); err != nil {
		service.failCheck(err, 0)
		return service.Status(ctx)
	}
	service.setStatus(func(status *UpdateStatus) {
		status.State = StateReady
		status.DownloadedBytes = status.TotalBytes
		status.ErrorCode = ""
	})
	return service.Status(ctx)
}

func (service *Service) failCheck(err error, checkedAtUnixMS int64) {
	code := ErrorCode(err)
	service.mu.RLock()
	state := service.status.State
	service.mu.RUnlock()
	if code == "update_failed" && (state == StateVerifying || state == StatePreparing) {
		code = ErrorArtifactVerificationFailed
	}
	log.Printf("profiledeck: update check failed (%s): %v", code, err)
	service.setStatus(func(status *UpdateStatus) {
		status.State = StateError
		status.ErrorCode = code
		if checkedAtUnixMS > 0 {
			status.LastCheckedAtUnixMS = checkedAtUnixMS
		}
	})
}

func (service *Service) handleProgress(event *application.CustomEvent) {
	progress, ok := event.Data.(updater.Progress)
	if !ok {
		if pointer, pointerOK := event.Data.(*updater.Progress); pointerOK && pointer != nil {
			progress, ok = *pointer, true
		}
	}
	if !ok {
		return
	}
	service.setStatus(func(status *UpdateStatus) {
		status.State = StateDownloading
		status.DownloadedBytes = progress.Written
		status.TotalBytes = progress.Total
	})
}

func (service *Service) setState(state string) {
	service.setStatus(func(status *UpdateStatus) { status.State = state })
}

func (service *Service) setStatus(change func(*UpdateStatus)) {
	service.mu.Lock()
	change(&service.status)
	// Events and RPC snapshots may arrive out of order; revisions let the UI
	// discard an older snapshot without coupling delivery to the service lock.
	service.status.Revision++
	status, emit := service.status, service.emit
	service.mu.Unlock()
	if emit != nil {
		emit(status)
	}
}

func (service *Service) publish() {
	service.mu.RLock()
	status, emit := service.status, service.emit
	service.mu.RUnlock()
	if emit != nil {
		emit(status)
	}
}

func (service *Service) signalScheduler() {
	service.startMu.Lock()
	wake := service.wake
	service.startMu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func channelCanChange(state string) bool {
	switch state {
	case StateIdle, StateUpToDate, StateError:
		return true
	default:
		return false
	}
}

func (service *Service) applicationBundle() (string, error) {
	if runtime.GOOS != UpdatePlatform {
		return "", errors.New("updates are only supported on macOS")
	}
	executable, err := service.executable()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(executable)
	for current := clean; current != filepath.Dir(current); current = filepath.Dir(current) {
		if strings.HasSuffix(current, ".app") {
			return current, nil
		}
	}
	return "", errors.New("running executable is not inside an application bundle")
}

func verifyParentWritable(parent string) error {
	probe, err := os.CreateTemp(parent, ".profiledeck-update-write-*")
	if err != nil {
		return err
	}
	path := probe.Name()
	return errors.Join(probe.Close(), os.Remove(path))
}

func removeWailsStaging(downloadedPath string) {
	if strings.TrimSpace(downloadedPath) == "" {
		return
	}
	dir := filepath.Clean(filepath.Dir(downloadedPath))
	relative, err := filepath.Rel(os.TempDir(), dir)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return
	}
	if strings.HasPrefix(filepath.Base(dir), "wails-update-") {
		_ = os.RemoveAll(dir)
	}
}
