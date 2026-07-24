package main

import (
	"context"
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/strahe/profiledeck/desktop/backend"
	desktopupdate "github.com/strahe/profiledeck/desktop/update"
	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/settings"
)

var (
	version               = app.DefaultVersion
	commit                = app.UnknownBuildValue
	buildDate             = app.UnknownBuildValue
	updatePublicKeyBase64 string
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/appicon.png
var appIcon []byte

//go:embed assets/menubar-template.png
var menuBarTemplateIcon []byte

func main() {
	// The detached update helper must swap the application before runtime or
	// database initialisation can fail or contend with the exiting process.
	updater.HandleHelperMode()
	if os.Getenv("PROFILEDECK_RESTART_DELAYED") == "1" {
		time.Sleep(750 * time.Millisecond)
	}

	info := app.NewInfo(version, commit, buildDate)
	env := backend.NewEnvironmentFromEnv()
	desktopCtx, cancelDesktop := context.WithCancel(context.Background())
	defer cancelDesktop()

	core, err := app.New(app.Config{
		ConfigDir:   env.ConfigDir,
		CodexDir:    env.CodexDir,
		AgentAccess: agent.AccessDesktopPreferences,
	})
	if err != nil {
		log.Fatal(apperror.Public(err))
	}
	defer core.Close()
	startupErr := backend.Bootstrap(desktopCtx, core)
	services := backend.NewServices(core, info, env, startupErr)
	updates := desktopupdate.NewService(desktopCtx, core, desktopupdate.BuildConfig{
		CurrentVersion: version, PublicKeyBase64: updatePublicKeyBase64,
	})
	if updates.Status(desktopCtx).ErrorCode == desktopupdate.ErrorConfigurationInvalid {
		log.Print("profiledeck: automatic updates are unavailable (verification configuration is invalid)")
	}

	wailsApp := application.New(application.Options{
		Name:        app.ProductName,
		Description: "Provider/profile switcher and local usage tracker for AI coding tools",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(services.App),
			application.NewService(services.Agent),
			application.NewService(services.Antigravity),
			application.NewService(services.ClaudeCode),
			application.NewService(services.Codex),
			application.NewService(services.Profile),
			application.NewService(services.Switch),
			application.NewService(services.Doctor),
			application.NewService(services.Backup),
			application.NewService(services.Usage),
			application.NewService(services.Settings),
			application.NewService(updates),
		},
		Assets: application.AssetOptions{
			Handler:        application.AssetFileServerFS(assets),
			Middleware:     noStoreAssetMiddleware,
			DisableLogging: true,
		},
		MarshalError: marshalWailsError,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	backend.ConfigureBackupRestarter(services.Backup, desktopRestarter(wailsApp))
	if err := desktopupdate.Attach(updates, wailsApp); err != nil {
		log.Print("profiledeck: automatic updates are unavailable")
	}

	mainWindow := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            app.ProductName,
		Width:            940,
		Height:           600,
		MinWidth:         900,
		MinHeight:        580,
		URL:              "/",
		BackgroundColour: application.NewRGB(248, 250, 252),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
			Backdrop: application.MacBackdropNormal,
		},
	})
	hideMainWindowOnUserClose(mainWindow)
	setupTray(desktopCtx, wailsApp, mainWindow, services)
	setupUsageAutoSync(desktopCtx, wailsApp, services)
	setupCodexQuotaRuntime(desktopCtx, wailsApp, services)
	setupUpdateRuntime(desktopCtx, wailsApp, updates)
	setupApplicationBackupRuntime(desktopCtx, wailsApp, services)

	if err := wailsApp.Run(); err != nil {
		log.Fatal(apperror.Public(err))
	}
}

func setupApplicationBackupRuntime(ctx context.Context, wailsApp *application.App, services backend.Services) {
	removeStartedHandler := wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		services.StartApplicationBackups(ctx)
	})
	wailsApp.OnShutdown(func() {
		removeStartedHandler()
		services.StopApplicationBackups()
	})
}

func desktopRestarter(wailsApp *application.App) func() error {
	return func() error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		process, err := os.StartProcess(executable, os.Args, &os.ProcAttr{
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
			Env:   restartEnvironment(os.Environ()),
		})
		if err != nil {
			return err
		}
		_ = process.Release()
		go func() {
			time.Sleep(250 * time.Millisecond)
			wailsApp.Quit()
		}()
		return nil
	}
}

func restartEnvironment(current []string) []string {
	result := make([]string, 0, len(current)+1)
	for _, value := range current {
		if strings.HasPrefix(value, "PROFILEDECK_RESTART_DELAYED=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "PROFILEDECK_RESTART_DELAYED=1")
}

func setupUpdateRuntime(ctx context.Context, wailsApp *application.App, updates *desktopupdate.Service) {
	removeStartedHandler := wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Update checks are process-scoped so the six-hour schedule continues
		// while the main window is hidden and the tray process remains active.
		desktopupdate.Start(ctx, updates)
	})
	wailsApp.OnShutdown(func() {
		removeStartedHandler()
		desktopupdate.Stop(updates)
	})
}

func setupCodexQuotaRuntime(ctx context.Context, wailsApp *application.App, services backend.Services) {
	removeStartedHandler := wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Profile quota and auth keepalive automation is process-scoped. It stays
		// active while the window is hidden, and stops when the tray app exits.
		services.StartCodexQuotaRuntime(ctx, func(status backend.CodexQuotaRuntimeStatus) {
			wailsApp.Event.Emit(backend.CodexQuotaStatusEventName, status)
		})
	})
	wailsApp.OnShutdown(func() {
		removeStartedHandler()
		services.StopCodexQuotaRuntime()
	})
}

func setupUsageAutoSync(ctx context.Context, wailsApp *application.App, services backend.Services) {
	removeStartedHandler := wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// The scheduler starts only after Wails can safely deliver backend events;
		// it remains active while the main window is hidden in the tray.
		services.StartUsageAutoSync(ctx, func(status backend.UsageAutoSyncStatus) {
			wailsApp.Event.Emit(backend.UsageAutoSyncEventName, status)
		})
	})
	wailsApp.OnShutdown(func() {
		removeStartedHandler()
		services.StopUsageAutoSync()
	})
}

func noStoreAssetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embedded assets change with each build; WebViews can otherwise reuse
		// stale fixed-path JS/CSS across desktop restarts.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

func marshalWailsError(err error) []byte {
	payload := backend.FormatDesktopError(err)
	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return nil
	}
	return raw
}

func setupTray(ctx context.Context, wailsApp *application.App, mainWindow *application.WebviewWindow, services backend.Services) {
	tray := wailsApp.SystemTray.New()
	// Template icons follow the menu bar appearance; use the designed
	// monochrome asset rather than a runtime-drawn placeholder.
	tray.SetTemplateIcon(menuBarTemplateIcon)
	tray.SetIconPosition(application.NSImageOnly)
	tray.SetTooltip(app.ProductName)
	// Do not AttachWindow the main window: that API is for tray popovers and on
	// macOS elevates the window to NSPopUpMenuWindowLevel, so it stays above
	// other apps. Open/focus via OnClick and the tray menu instead.
	initialLocale := loadInitialTrayLocale(ctx, services.Settings, systemPreferredTrayLanguage())
	controller := newTrayController(ctx, services, wailsTrayUI{
		app:    wailsApp,
		window: mainWindow,
		tray:   tray,
	}, initialLocale)
	tray.OnClick(func() {
		runTrayAction(controller.openMainWindow)
	})
	removeLocaleHandler := wailsApp.Event.On(trayLocaleChangedEventName, func(event *application.CustomEvent) {
		if locale, ok := event.Data.(string); ok {
			controller.SetLocale(locale)
		}
	})
	controller.Refresh(nil, false)
	cleanupTrayRefresh := subscribeTrayRefresh(services, controller)
	wailsApp.OnShutdown(func() {
		removeLocaleHandler()
		cleanupTrayRefresh()
	})
}

func loadInitialTrayLocale(
	ctx context.Context,
	settingsService *backend.SettingsService,
	systemLanguage string,
) trayLocale {
	preference := settings.DesktopLanguageAuto
	if settingsService != nil {
		desktopSettings, err := settingsService.Get(ctx)
		if err != nil {
			log.Print("profiledeck: tray language setting is unavailable")
		} else {
			preference = desktopSettings.Language
		}
	}
	return resolveTrayLocale(preference, systemLanguage)
}

func hideMainWindowOnUserClose(window *application.WebviewWindow) {
	// Menu-bar apps keep the process alive when the main window is closed;
	// Quit is the only path that exits the application.
	window.RegisterHook(events.Mac.WindowShouldClose, func(event *application.WindowEvent) {
		event.Cancel()
		window.Hide()
	})
}
