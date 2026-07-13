package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/strahe/profiledeck/desktop/backend"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
)

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

type trayController struct {
	ctx             context.Context
	services        backend.Services
	ui              trayUI
	loadDashboard   dashboardLoader
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

func (c *trayController) refresh(menuGeneration, eventGeneration uint64, event *backend.DesktopChangeEvent) {
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

func (c *trayController) openSwitch(providerID, profileID string) {
	c.ui.ShowMainWindow()
	c.ui.Emit("profiledeck:open-switch", map[string]string{
		"provider_id": providerID,
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
	refresh        func()
	openSwitch     func(providerID, profileID string)
	quit           func()
}

func buildTrayMenu(dashboard backend.DashboardResult, dashboardErr error, actions trayMenuActions) *application.Menu {
	menu := application.NewMenu()
	if dashboardErr != nil {
		menu.Add("ProfileDeck: unavailable").SetEnabled(false)
		menu.Add(trayErrorLabel(dashboardErr, trayDashboardUnavailableLabel)).SetEnabled(false)
	} else {
		menu.Add(currentProfileLabel(dashboard)).SetEnabled(false)
		menu.Add(providerCurrentProfileLabel(dashboard, agyconfig.ProviderID, "Antigravity")).SetEnabled(false)
		menu.Add(providerCurrentProfileLabel(dashboard, claudecodeconfig.ProviderID, "Claude Code")).SetEnabled(false)
		for _, missing := range missingActiveProfileLabels(dashboard) {
			menu.Add(missing).SetEnabled(false)
		}
	}
	menu.AddSeparator()
	menu.Add("Open ProfileDeck").OnClick(func(*application.Context) {
		runTrayAction(actions.openMainWindow)
	})
	menu.Add("Run Doctor").OnClick(func(*application.Context) {
		runTrayAction(actions.runDoctor)
	})
	var codexProfiles []trayProfile
	if dashboard.CodexProfiles != nil {
		for _, profile := range dashboard.CodexProfiles.Profiles {
			codexProfiles = append(codexProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	addTrayProfilesMenu(menu, "Codex Profiles", codexconfig.ProviderID, codexProfiles, dashboard.CodexProfiles != nil, "No Codex profiles", trayCodexProfilesUnavailableLabel, actions)

	var antigravityProfiles []trayProfile
	if dashboard.AntigravityProfiles != nil {
		for _, profile := range dashboard.AntigravityProfiles.Profiles {
			antigravityProfiles = append(antigravityProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	addTrayProfilesMenu(menu, "Antigravity Profiles", agyconfig.ProviderID, antigravityProfiles, dashboard.AntigravityProfiles != nil, "No Antigravity profiles", trayAntigravityProfilesUnavailableLabel, actions)

	var claudeCodeProfiles []trayProfile
	if dashboard.ClaudeCodeProfiles != nil {
		for _, profile := range dashboard.ClaudeCodeProfiles.Profiles {
			claudeCodeProfiles = append(claudeCodeProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	addTrayProfilesMenu(menu, "Claude Code Profiles", claudecodeconfig.ProviderID, claudeCodeProfiles, dashboard.ClaudeCodeProfiles != nil, "No Claude Code profiles", trayClaudeCodeProfilesUnavailableLabel, actions)

	menu.AddSeparator()
	menu.Add("Refresh Menu").OnClick(func(*application.Context) {
		runTrayAction(actions.refresh)
	})
	menu.Add("Quit").OnClick(func(*application.Context) {
		runTrayAction(actions.quit)
	})
	return menu
}

type trayProfile struct {
	Profile app.Profile
	Active  bool
}

func addTrayProfilesMenu(menu *application.Menu, title, providerID string, profiles []trayProfile, loaded bool, emptyLabel, unavailableLabel string, actions trayMenuActions) {
	profilesMenu := menu.AddSubmenu(title)
	if !loaded {
		profilesMenu.Add(unavailableLabel).SetEnabled(false)
		return
	}
	if len(profiles) == 0 {
		profilesMenu.Add(emptyLabel).SetEnabled(false)
		return
	}
	for _, profile := range profiles {
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
					actions.openSwitch(providerID, profile.Profile.ID)
				})
			}
		})
	}
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

func providerCurrentProfileLabel(dashboard backend.DashboardResult, providerID, providerName string) string {
	for _, state := range dashboard.ActiveStates {
		if state.ProviderID != providerID {
			continue
		}
		name := state.ProfileName
		if name == "" {
			name = state.ProfileID
		}
		if name == "" {
			return providerName + ": not active"
		}
		if !state.ProfileAvailable {
			return providerName + ": missing profile " + name
		}
		return providerName + ": " + name
	}
	return providerName + ": not active"
}

func missingActiveProfileLabels(dashboard backend.DashboardResult) []string {
	labels := []string{}
	for _, state := range dashboard.ActiveStates {
		if state.ProfileID == "" || state.ProfileAvailable {
			continue
		}
		providerName := state.ProviderID
		switch state.ProviderID {
		case codexconfig.ProviderID:
			providerName = "Codex"
		case agyconfig.ProviderID:
			providerName = "Antigravity"
		case claudecodeconfig.ProviderID:
			providerName = "Claude Code"
		}
		labels = append(labels, "Missing "+providerName+" profile: "+state.ProfileID)
	}
	return labels
}

func showMainWindow(window application.Window) {
	window.Show().Focus()
}
