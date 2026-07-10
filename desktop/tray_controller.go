package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/app"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const trayUsageSyncTimeout = 30 * time.Second

type trayUI interface {
	SetMenu(*application.Menu)
	Emit(name string, data ...any)
	ShowMainWindow()
	HideTray()
	Quit()
}

type wailsTrayUI struct {
	app    *application.App
	window application.Window
	tray   *application.SystemTray
}

func (ui wailsTrayUI) SetMenu(menu *application.Menu) {
	ui.tray.SetMenu(menu)
}

func (ui wailsTrayUI) Emit(name string, data ...any) {
	ui.app.Event.Emit(name, data...)
}

func (ui wailsTrayUI) ShowMainWindow() {
	showMainWindow(ui.window)
}

func (ui wailsTrayUI) HideTray() {
	ui.tray.Hide()
}

func (ui wailsTrayUI) Quit() {
	ui.app.Quit()
}

type dashboardLoader func(context.Context) (backend.DashboardResult, error)
type usageSyncer func(context.Context) (app.UsageSyncResult, error)

type trayController struct {
	ctx             context.Context
	services        backend.Services
	ui              trayUI
	loadDashboard   dashboardLoader
	syncUsageCodex  usageSyncer
	menuGeneration  atomic.Uint64
	eventGeneration atomic.Uint64
}

func newTrayController(ctx context.Context, services backend.Services, ui trayUI) *trayController {
	return &trayController{
		ctx:      ctx,
		services: services,
		ui:       ui,
		loadDashboard: func(ctx context.Context) (backend.DashboardResult, error) {
			return services.App.Dashboard(ctx)
		},
		syncUsageCodex: services.Usage.SyncCodex,
	}
}

func (c *trayController) Refresh(event *backend.DesktopChangeEvent, emit bool) {
	if c == nil {
		return
	}
	menuGeneration := c.menuGeneration.Add(1)
	var eventGeneration uint64
	var eventCopy *backend.DesktopChangeEvent
	if emit && event != nil {
		eventGeneration = c.eventGeneration.Add(1)
		copied := *event
		eventCopy = &copied
	}
	go c.refresh(menuGeneration, eventGeneration, eventCopy)
}

func (c *trayController) refresh(menuGeneration uint64, eventGeneration uint64, event *backend.DesktopChangeEvent) {
	dashboard, dashboardErr := c.loadDashboard(c.ctx)
	if c.ctx.Err() != nil {
		return
	}

	// Wails SetMenu, Event.Emit, Show and Focus already marshal to the UI
	// thread as needed. Wrapping them in application.InvokeAsync can deadlock
	// when a callback is already executing on the UI thread.
	if c.menuGeneration.Load() == menuGeneration {
		menu := buildTrayMenu(dashboard, dashboardErr, trayMenuActions{
			openMainWindow: c.openMainWindow,
			runDoctor:      c.runDoctor,
			syncUsage:      c.syncUsage,
			refresh:        func() { c.Refresh(nil, false) },
			openSwitch:     c.openSwitch,
			quit:           c.quit,
		})
		if c.ctx.Err() != nil {
			return
		}
		if c.menuGeneration.Load() == menuGeneration {
			c.ui.SetMenu(menu)
		}
	}
	if event != nil && c.ctx.Err() == nil && c.eventGeneration.Load() == eventGeneration {
		payload := backend.DashboardUpdatePayload{Event: *event, Dashboard: dashboard}
		if dashboardErr != nil {
			payload.Error = backend.FormatDesktopErrorPtr(dashboardErr)
		}
		c.ui.Emit("profiledeck:dashboard-updated", payload)
	}
}

func (c *trayController) openMainWindow() {
	c.ui.ShowMainWindow()
}

func (c *trayController) runDoctor() {
	c.ui.ShowMainWindow()
	c.ui.Emit("profiledeck:open-doctor")
}

func (c *trayController) syncUsage() {
	// Bound tray-triggered syncs so a stalled scan cannot retain the action
	// goroutine indefinitely while preserving desktop shutdown cancellation.
	ctx, cancel := context.WithTimeout(c.ctx, trayUsageSyncTimeout)
	defer cancel()
	result, syncErr := c.syncUsageCodex(ctx)
	if c.ctx.Err() != nil {
		return
	}
	if syncErr != nil {
		c.ui.Emit("profiledeck:operation-error", backend.FormatDesktopError(syncErr))
		c.ui.ShowMainWindow()
		return
	}
	c.ui.Emit("profiledeck:usage-synced", result)
}

func (c *trayController) openSwitch(profileID string) {
	c.ui.ShowMainWindow()
	c.ui.Emit("profiledeck:open-switch", map[string]string{
		"provider_id": codexconfig.ProviderID,
		"profile_id":  profileID,
	})
}

func (c *trayController) quit() {
	c.ui.HideTray()
	c.ui.Quit()
}

type trayMenuActions struct {
	openMainWindow func()
	runDoctor      func()
	syncUsage      func()
	refresh        func()
	openSwitch     func(profileID string)
	quit           func()
}

func buildTrayMenu(dashboard backend.DashboardResult, dashboardErr error, actions trayMenuActions) *application.Menu {
	menu := application.NewMenu()
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
		runTrayAction(actions.openMainWindow)
	})
	menu.Add("Run Doctor").OnClick(func(*application.Context) {
		runTrayAction(actions.runDoctor)
	})
	menu.Add("Sync Usage").OnClick(func(*application.Context) {
		runTrayAction(actions.syncUsage)
	})

	profilesMenu := menu.AddSubmenu("Codex Profiles")
	if dashboard.CodexProfiles == nil {
		profilesMenu.Add(trayCodexProfilesUnavailableLabel).SetEnabled(false)
	} else if len(dashboard.CodexProfiles.Profiles) == 0 {
		profilesMenu.Add("No Codex profiles").SetEnabled(false)
	} else {
		for _, profile := range dashboard.CodexProfiles.Profiles {
			profile := profile
			label := profile.Profile.Name
			if label == "" {
				label = profile.Profile.ID
			}
			item := profilesMenu.Add(label)
			if profile.Active {
				item.SetChecked(true)
			}
			item.OnClick(func(*application.Context) {
				if actions.openSwitch != nil {
					runTrayAction(func() {
						actions.openSwitch(profile.Profile.ID)
					})
				}
			})
		}
	}

	menu.AddSeparator()
	menu.Add("Refresh Menu").OnClick(func(*application.Context) {
		runTrayAction(actions.refresh)
	})
	menu.Add("Quit").OnClick(func(*application.Context) {
		runTrayAction(actions.quit)
	})
	return menu
}

func runTrayAction(action func()) {
	if action != nil {
		go action()
	}
}

func subscribeTrayRefresh(services backend.Services, controller *trayController) func() {
	const debounce = 120 * time.Millisecond
	debouncer := newDesktopChangeDebouncer(debounce, func(event backend.DesktopChangeEvent) {
		controller.Refresh(&event, true)
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

func showMainWindow(window application.Window) {
	window.Show().Focus()
}
