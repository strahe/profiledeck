package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"time"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/app"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var (
	version   = app.DefaultVersion
	commit    = app.UnknownBuildValue
	buildDate = app.UnknownBuildValue
)

const (
	trayDashboardUnavailableLabel     = "Dashboard unavailable. Open ProfileDeck for details."
	trayCodexProfilesUnavailableLabel = "Unable to load Codex profiles. Open ProfileDeck for details."
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	info := app.NewInfo(version, commit, buildDate)
	env := backend.NewEnvironmentFromEnv()
	desktopCtx, cancelDesktop := context.WithCancel(context.Background())
	defer cancelDesktop()

	startupErr := backend.Bootstrap(desktopCtx, env)
	services := backend.NewServices(info, env, startupErr)

	wailsApp := application.New(application.Options{
		Name:        app.ProductName,
		Description: "Provider/profile switcher and local usage tracker for AI coding tools",
		Services: []application.Service{
			application.NewService(services.App),
			application.NewService(services.Codex),
			application.NewService(services.Profile),
			application.NewService(services.Switch),
			application.NewService(services.Doctor),
			application.NewService(services.Backup),
			application.NewService(services.Usage),
		},
		Assets: application.AssetOptions{
			Handler:        application.AssetFileServerFS(assets),
			DisableLogging: true,
		},
		MarshalError: marshalWailsError,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	mainWindow := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            app.ProductName,
		Width:            1120,
		Height:           760,
		MinWidth:         920,
		MinHeight:        620,
		URL:              "/",
		BackgroundColour: application.NewRGB(248, 250, 252),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
			Backdrop: application.MacBackdropNormal,
		},
	})
	hideMainWindowOnUserClose(mainWindow)
	setupTray(desktopCtx, wailsApp, mainWindow, services)

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
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
	tray.SetTemplateIcon(trayTemplateIcon())
	tray.SetIconPosition(application.NSImageOnly)
	tray.SetTooltip(app.ProductName)
	tray.AttachWindow(mainWindow).WindowOffset(10)
	tray.OnClick(func() {
		showMainWindow(mainWindow)
	})

	refresh := func() {
		refreshTrayDashboard(ctx, wailsApp, mainWindow, tray, services, nil, false)
	}
	dashboard, dashboardErr := services.App.Dashboard(ctx)
	tray.SetMenu(buildTrayMenu(ctx, wailsApp, mainWindow, tray, services, dashboard, dashboardErr, refresh))
	cleanupTrayRefresh := subscribeTrayRefresh(ctx, wailsApp, mainWindow, tray, services)
	wailsApp.OnShutdown(cleanupTrayRefresh)
}

func hideMainWindowOnUserClose(window *application.WebviewWindow) {
	// Menu-bar apps keep the process alive when the main window is closed;
	// Quit is the only path that exits the application.
	window.RegisterHook(events.Mac.WindowShouldClose, func(event *application.WindowEvent) {
		event.Cancel()
		window.Hide()
	})
}

func refreshTrayDashboard(ctx context.Context, wailsApp *application.App, mainWindow *application.WebviewWindow, tray *application.SystemTray, services backend.Services, event *backend.DesktopChangeEvent, emit bool) {
	dashboard, dashboardErr := services.App.Dashboard(ctx)
	application.InvokeAsync(func() {
		refresh := func() {
			refreshTrayDashboard(ctx, wailsApp, mainWindow, tray, services, nil, false)
		}
		tray.SetMenu(buildTrayMenu(ctx, wailsApp, mainWindow, tray, services, dashboard, dashboardErr, refresh))
		if emit && event != nil {
			payload := backend.DashboardUpdatePayload{Event: *event, Dashboard: dashboard}
			if dashboardErr != nil {
				payload.Error = backend.FormatDesktopErrorPtr(dashboardErr)
			}
			wailsApp.Event.Emit("profiledeck:dashboard-updated", payload)
		}
	})
}

func buildTrayMenu(ctx context.Context, wailsApp *application.App, mainWindow *application.WebviewWindow, tray *application.SystemTray, services backend.Services, dashboard backend.DashboardResult, dashboardErr error, refresh func()) *application.Menu {
	menu := wailsApp.NewMenu()
	if dashboardErr != nil {
		menu.Add("ProfileDeck: unavailable").SetEnabled(false)
		menu.Add(trayErrorLabel(dashboardErr, trayDashboardUnavailableLabel)).SetEnabled(false)
	} else {
		menu.Add(currentProfileLabel(dashboard)).SetEnabled(false)
		if missingID := missingActiveCodexProfileID(dashboard); missingID != "" {
			menu.Add("Missing active profile: " + missingID).SetEnabled(false)
		}
	}
	menu.AddSeparator()
	menu.Add("Open ProfileDeck").OnClick(func(*application.Context) {
		showMainWindow(mainWindow)
	})
	menu.Add("Run Doctor").OnClick(func(*application.Context) {
		showMainWindow(mainWindow)
		wailsApp.Event.Emit("profiledeck:open-doctor")
	})
	menu.Add("Sync Usage").OnClick(func(*application.Context) {
		go func() {
			result, syncErr := services.Usage.SyncCodex(ctx)
			application.InvokeAsync(func() {
				if syncErr != nil {
					wailsApp.Event.Emit("profiledeck:operation-error", backend.FormatDesktopError(syncErr))
					showMainWindow(mainWindow)
					return
				}
				wailsApp.Event.Emit("profiledeck:usage-synced", result)
			})
		}()
	})

	profilesMenu := menu.AddSubmenu("Codex Profiles")
	profiles, profilesErr := codexProfiles(ctx, services, dashboard)
	if profilesErr != nil {
		profilesMenu.Add(trayErrorLabel(profilesErr, trayCodexProfilesUnavailableLabel)).SetEnabled(false)
	} else if len(profiles) == 0 {
		profilesMenu.Add("No Codex profiles").SetEnabled(false)
	} else {
		activeProfileID := activeCodexProfileID(dashboard)
		for _, profile := range profiles {
			profile := profile
			label := profile.Name
			if label == "" {
				label = profile.ID
			}
			item := profilesMenu.Add(label)
			if profile.ID == activeProfileID {
				item.SetChecked(true)
			}
			item.OnClick(func(*application.Context) {
				showMainWindow(mainWindow)
				wailsApp.Event.Emit("profiledeck:open-switch", map[string]string{
					"provider_id": codexconfig.ProviderID,
					"profile_id":  profile.ID,
				})
			})
		}
	}

	menu.AddSeparator()
	menu.Add("Refresh Menu").OnClick(func(*application.Context) {
		refresh()
	})
	menu.Add("Quit").OnClick(func(*application.Context) {
		tray.Hide()
		wailsApp.Quit()
	})
	return menu
}

func subscribeTrayRefresh(ctx context.Context, wailsApp *application.App, mainWindow *application.WebviewWindow, tray *application.SystemTray, services backend.Services) func() {
	const debounce = 120 * time.Millisecond
	debouncer := newDesktopChangeDebouncer(debounce, func(event backend.DesktopChangeEvent) {
		refreshTrayDashboard(ctx, wailsApp, mainWindow, tray, services, &event, true)
	})

	unsubscribe := services.SubscribeChanges(func(event backend.DesktopChangeEvent) {
		debouncer.Notify(event)
	})
	return func() {
		unsubscribe()
		debouncer.Stop()
	}
}

func trayErrorLabel(err error, fallback string) string {
	if err == nil {
		return ""
	}
	return fallback
}

func codexProfiles(ctx context.Context, services backend.Services, dashboard backend.DashboardResult) ([]app.Profile, error) {
	profiles := dashboard.Profiles
	result := make([]app.Profile, 0, len(profiles))
	for _, profile := range profiles {
		targets, err := services.Profile.ListTargets(ctx, profile.ID, codexconfig.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("list Codex targets for profile %q: %w", profile.ID, err)
		}
		if len(targets) > 0 {
			result = append(result, profile)
		}
	}
	return result, nil
}

func currentProfileLabel(dashboard backend.DashboardResult) string {
	for _, state := range dashboard.ActiveStates {
		if state.ProviderID != codexconfig.ProviderID {
			continue
		}
		name := state.ProfileName
		if name == "" {
			name = state.ProfileID
		}
		if name == "" {
			return "Current: Codex not active"
		}
		if !state.ProfileAvailable {
			return "Current: missing profile " + name
		}
		return "Current: " + name
	}
	return "Current: Codex not active"
}

func missingActiveCodexProfileID(dashboard backend.DashboardResult) string {
	for _, state := range dashboard.ActiveStates {
		if state.ProviderID == codexconfig.ProviderID && state.ProfileID != "" && !state.ProfileAvailable {
			return state.ProfileID
		}
	}
	return ""
}

func activeCodexProfileID(dashboard backend.DashboardResult) string {
	for _, state := range dashboard.ActiveStates {
		if state.ProviderID == codexconfig.ProviderID {
			return state.ProfileID
		}
	}
	return ""
}

func showMainWindow(window application.Window) {
	window.Show().Focus()
}

func trayTemplateIcon() []byte {
	const size = 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	black := color.RGBA{A: 255}
	for y := 4; y < 18; y++ {
		for x := 4; x < 8; x++ {
			img.SetRGBA(x, y, black)
		}
	}
	for y := 4; y < 9; y++ {
		for x := 8; x < 17; x++ {
			img.SetRGBA(x, y, black)
		}
	}
	for y := 10; y < 14; y++ {
		for x := 8; x < 15; x++ {
			img.SetRGBA(x, y, black)
		}
	}
	for y := 15; y < 18; y++ {
		for x := 8; x < 13; x++ {
			img.SetRGBA(x, y, black)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil
	}
	return out.Bytes()
}
