package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/app"
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
			application.NewService(services.Settings),
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

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
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
	tray.SetTemplateIcon(trayTemplateIcon())
	tray.SetIconPosition(application.NSImageOnly)
	tray.SetTooltip(app.ProductName)
	tray.AttachWindow(mainWindow).WindowOffset(10)

	controller := newTrayController(ctx, services, wailsTrayUI{
		app:    wailsApp,
		window: mainWindow,
		tray:   tray,
	})
	tray.OnClick(func() {
		runTrayAction(controller.openMainWindow)
	})
	controller.Refresh(nil, false)
	cleanupTrayRefresh := subscribeTrayRefresh(services, controller)
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
