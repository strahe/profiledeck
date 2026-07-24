package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/agent"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/profile"
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
	locale          atomic.Uint32
}

func (c *trayController) SetLocale(value string) {
	if c == nil {
		return
	}
	locale, ok := parseTrayLocale(value)
	if !ok {
		return
	}
	if previous := c.locale.Swap(uint32(locale)); previous != uint32(locale) {
		// Locale changes invalidate any in-flight menu so stale text cannot replace the rebuilt menu.
		c.Refresh(nil, false)
	}
}

func (c *trayController) messages() trayMessages {
	return messagesForTrayLocale(trayLocale(c.locale.Load()))
}

func newTrayController(ctx context.Context, services backend.Services, ui trayUI, initialLocale trayLocale) *trayController {
	controller := &trayController{
		ctx:      ctx,
		services: services,
		ui:       ui,
		loadDashboard: func(ctx context.Context) (backend.DashboardResult, error) {
			return services.App.Dashboard(ctx)
		},
	}
	controller.locale.Store(uint32(initialLocale))
	return controller
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
			rebuildMenu:    c.rebuildTrayMenu,
			openSwitch:     c.openSwitch,
			quit:           c.quit,
		}, c.messages())
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

// rebuildTrayMenu reloads the dashboard and replaces the tray menu on the
// caller's goroutine. Use this when a click must correct radio state before
// opening UI; Refresh remains the fire-and-forget path for the Refresh item.
func (c *trayController) rebuildTrayMenu() {
	if c == nil {
		return
	}
	menuGeneration := c.menuGeneration.Add(1)
	c.refresh(menuGeneration, 0, nil)
}

func (c *trayController) quit() {
	c.ui.HideTray()
	c.ui.Quit()
}

type trayMenuActions struct {
	openMainWindow func()
	runDoctor      func()
	refresh        func()
	rebuildMenu    func()
	openSwitch     func(providerID, profileID string)
	quit           func()
}

func buildTrayMenu(dashboard backend.DashboardResult, dashboardErr error, actions trayMenuActions, messages trayMessages) *application.Menu {
	menu := application.NewMenu()
	// Status readout is omitted: profile submenus show the active item with a
	// radio check. Keep only load failures and missing-binding warnings here.
	if dashboardErr != nil {
		menu.Add(messages.profileDeckUnavailable).SetEnabled(false)
		menu.Add(trayErrorLabel(dashboardErr, messages.dashboardUnavailable)).SetEnabled(false)
		menu.AddSeparator()
	} else if missing := missingActiveProfileLabels(dashboard, messages); len(missing) > 0 {
		for _, label := range missing {
			menu.Add(label).SetEnabled(false)
		}
		menu.AddSeparator()
	}
	menu.Add(messages.openProfileDeck).OnClick(func(*application.Context) {
		runTrayAction(actions.openMainWindow)
	})
	menu.Add(messages.runDoctor).OnClick(func(*application.Context) {
		runTrayAction(actions.runDoctor)
	})
	var codexProfiles []trayProfile
	if dashboard.CodexProfiles != nil {
		for _, profile := range dashboard.CodexProfiles.Profiles {
			codexProfiles = append(codexProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	if trayAgentEnabled(dashboard, agent.Codex) {
		addTrayProfilesMenu(menu, messages.codexProfiles, codexconfig.ProviderID, codexProfiles, dashboard.CodexProfiles != nil, messages.noCodexProfiles, messages.codexProfilesUnavailable, actions)
	}

	var antigravityProfiles []trayProfile
	if dashboard.AntigravityProfiles != nil {
		for _, profile := range dashboard.AntigravityProfiles.Profiles {
			antigravityProfiles = append(antigravityProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	if trayAgentEnabled(dashboard, agent.Antigravity) {
		addTrayProfilesMenu(menu, messages.antigravityProfiles, agyconfig.ProviderID, antigravityProfiles, dashboard.AntigravityProfiles != nil, messages.noAntigravityProfiles, messages.antigravityUnavailable, actions)
	}

	var claudeCodeProfiles []trayProfile
	if dashboard.ClaudeCodeProfiles != nil {
		for _, profile := range dashboard.ClaudeCodeProfiles.Profiles {
			claudeCodeProfiles = append(claudeCodeProfiles, trayProfile{Profile: profile.Profile, Active: profile.Active})
		}
	}
	if trayAgentEnabled(dashboard, agent.ClaudeCode) {
		addTrayProfilesMenu(menu, messages.claudeCodeProfiles, claudecodeconfig.ProviderID, claudeCodeProfiles, dashboard.ClaudeCodeProfiles != nil, messages.noClaudeCodeProfiles, messages.claudeCodeUnavailable, actions)
	}

	menu.AddSeparator()
	menu.Add(messages.refreshMenu).OnClick(func(*application.Context) {
		runTrayAction(actions.refresh)
	})
	menu.Add(messages.quit).OnClick(func(*application.Context) {
		runTrayAction(actions.quit)
	})
	return menu
}

type trayProfile struct {
	Profile profile.Profile
	Active  bool
}

func trayAgentEnabled(dashboard backend.DashboardResult, id agent.ID) bool {
	if len(dashboard.Agents) == 0 {
		return true
	}
	for _, state := range dashboard.Agents {
		if state.Manifest.ID == id {
			return state.Enabled
		}
	}
	return false
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
		// Radio (not plain text + SetChecked): macOS only paints native check
		// marks for checkbox/radio items when the tray menu is built.
		item := profilesMenu.AddRadio(label, profile.Active)
		item.OnClick(func(*application.Context) {
			// One goroutine, ordered steps: radio clicks auto-check before apply,
			// so rebuild from the dashboard first, then open the switch UI.
			runTrayAction(func() {
				if actions.rebuildMenu != nil {
					actions.rebuildMenu()
				}
				if actions.openSwitch != nil {
					actions.openSwitch(providerID, profile.Profile.ID)
				}
			})
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

func missingActiveProfileLabels(dashboard backend.DashboardResult, messages trayMessages) []string {
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
		labels = append(labels, fmt.Sprintf(messages.missingActiveProfile, providerName, state.ProfileID))
	}
	return labels
}

func showMainWindow(window application.Window) {
	window.Show().Focus()
}
