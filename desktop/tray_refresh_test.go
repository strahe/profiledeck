package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/antigravity"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/claudecode"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/codex"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/profile"
)

func newDesktopTestServices(t *testing.T, env backend.Environment) backend.Services {
	t.Helper()
	if env.ConfigDir == "" {
		env.ConfigDir = t.TempDir()
	}
	core, err := app.New(app.Config{
		ConfigDir: env.ConfigDir, CodexDir: env.CodexDir, AgentAccess: agent.AccessDesktopPreferences,
	})
	if err != nil {
		t.Fatalf("create test Application: %v", err)
	}
	return backend.NewServices(core, app.DefaultInfo(), env, nil)
}

func TestDesktopChangeDebouncerCoalescesLatestEvent(t *testing.T) {
	events := make(chan backend.DesktopChangeEvent, 2)
	debouncer := newDesktopChangeDebouncer(20*time.Millisecond, func(event backend.DesktopChangeEvent) {
		events <- event
	})

	debouncer.Notify(backend.DesktopChangeEvent{Kind: "first"})
	time.Sleep(5 * time.Millisecond)
	debouncer.Notify(backend.DesktopChangeEvent{Kind: "second"})

	select {
	case event := <-events:
		if event.Kind != "second" {
			t.Fatalf("expected latest event to win, got %#v", event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected debounced event")
	}

	select {
	case event := <-events:
		t.Fatalf("expected stale timer callback to be ignored, got %#v", event)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestDesktopChangeDebouncerStopCancelsPendingEvent(t *testing.T) {
	events := make(chan backend.DesktopChangeEvent, 1)
	debouncer := newDesktopChangeDebouncer(20*time.Millisecond, func(event backend.DesktopChangeEvent) {
		events <- event
	})

	debouncer.Notify(backend.DesktopChangeEvent{Kind: "pending"})
	debouncer.Stop()
	debouncer.Notify(backend.DesktopChangeEvent{Kind: "after-stop"})

	select {
	case event := <-events:
		t.Fatalf("expected stopped debouncer not to emit events, got %#v", event)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestBuildTrayMenuUsesDashboardCodexProfiles(t *testing.T) {
	if item := buildTrayMenu(backend.DashboardResult{}, nil, trayMenuActions{}).FindByLabel("Sync Usage"); item != nil {
		t.Fatalf("expected tray menu to omit manual usage sync")
	}

	t.Run("unavailable", func(t *testing.T) {
		menu := buildTrayMenu(backend.DashboardResult{}, nil, trayMenuActions{})

		submenu := requireMenuSubmenu(t, menu, "Codex Profiles")
		if got := submenu.ItemAt(0).Label(); got != trayCodexProfilesUnavailableLabel {
			t.Fatalf("expected unavailable profile label, got %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		menu := buildTrayMenu(dashboardWithCodexProfiles(), nil, trayMenuActions{})

		submenu := requireMenuSubmenu(t, menu, "Codex Profiles")
		if got := submenu.ItemAt(0).Label(); got != "No Codex profiles" {
			t.Fatalf("expected empty profile label, got %q", got)
		}
	})

	t.Run("profiles", func(t *testing.T) {
		menu := buildTrayMenu(
			dashboardWithCodexProfiles(
				codexProfileSummary("work", "Work", true),
				codexProfileSummary("personal", "", false),
			),
			nil,
			trayMenuActions{},
		)

		submenu := requireMenuSubmenu(t, menu, "Codex Profiles")
		work := submenu.ItemAt(0)
		if got := work.Label(); got != "Work" {
			t.Fatalf("expected named profile label, got %q", got)
		}
		if !work.Checked() {
			t.Fatalf("expected active profile to be checked")
		}
		if got := submenu.ItemAt(1).Label(); got != "personal" {
			t.Fatalf("expected profile id fallback label, got %q", got)
		}
	})
}

func TestBuildTrayMenuUsesDashboardAntigravityProfiles(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		menu := buildTrayMenu(backend.DashboardResult{}, nil, trayMenuActions{})
		submenu := requireMenuSubmenu(t, menu, "Antigravity Profiles")
		if got := submenu.ItemAt(0).Label(); got != trayAntigravityProfilesUnavailableLabel {
			t.Fatalf("expected unavailable Antigravity label, got %q", got)
		}
	})

	t.Run("profiles", func(t *testing.T) {
		menu := buildTrayMenu(dashboardWithAntigravityProfiles(
			antigravityProfileSummary("work", "Work", true),
			antigravityProfileSummary("personal", "", false),
		), nil, trayMenuActions{})
		submenu := requireMenuSubmenu(t, menu, "Antigravity Profiles")
		if got := submenu.ItemAt(0).Label(); got != "Work" || !submenu.ItemAt(0).Checked() {
			t.Fatalf("expected active Antigravity Profile, got label=%q checked=%t", got, submenu.ItemAt(0).Checked())
		}
		if got := submenu.ItemAt(1).Label(); got != "personal" {
			t.Fatalf("expected Antigravity Profile id fallback, got %q", got)
		}
	})
}

func TestBuildTrayMenuUsesDashboardClaudeCodeProfiles(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		menu := buildTrayMenu(backend.DashboardResult{}, nil, trayMenuActions{})
		submenu := requireMenuSubmenu(t, menu, "Claude Code Profiles")
		if got := submenu.ItemAt(0).Label(); got != trayClaudeCodeProfilesUnavailableLabel {
			t.Fatalf("expected unavailable Claude Code label, got %q", got)
		}
	})

	t.Run("profiles", func(t *testing.T) {
		menu := buildTrayMenu(dashboardWithClaudeCodeProfiles(
			claudeCodeProfileSummary("work", "Work", true),
			claudeCodeProfileSummary("personal", "", false),
		), nil, trayMenuActions{})
		submenu := requireMenuSubmenu(t, menu, "Claude Code Profiles")
		if got := submenu.ItemAt(0).Label(); got != "Work" || !submenu.ItemAt(0).Checked() {
			t.Fatalf("expected active Claude Code Profile, got label=%q checked=%t", got, submenu.ItemAt(0).Checked())
		}
		if got := submenu.ItemAt(1).Label(); got != "personal" {
			t.Fatalf("expected Claude Code Profile id fallback, got %q", got)
		}
	})
}

func TestBuildTrayMenuKeepsSafetyActionsWhenAllAgentsDisabled(t *testing.T) {
	states := make([]agent.State, 0, 3)
	for _, manifest := range agent.BuiltinRegistry().Manifests() {
		states = append(states, agent.State{Manifest: manifest, Enabled: false})
	}
	menu := buildTrayMenu(backend.DashboardResult{Agents: states}, nil, trayMenuActions{})

	for _, label := range []string{"Codex Profiles", "Antigravity Profiles", "Claude Code Profiles"} {
		if item := menu.FindByLabel(label); item != nil {
			t.Fatalf("disabled Agent remained in tray menu: %q", label)
		}
	}
	for _, label := range []string{"Open ProfileDeck", "Run Doctor", "Refresh Menu", "Quit"} {
		if item := menu.FindByLabel(label); item == nil {
			t.Fatalf("Agent-independent tray action is missing: %q", label)
		}
	}
}

func TestTrayControllerOpensAntigravitySwitch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := newFakeTrayUI()
	controller := newTrayController(ctx, newDesktopTestServices(t, backend.Environment{ConfigDir: t.TempDir()}), ui)
	controller.openSwitch(agyconfig.ProviderID, "work")
	event := waitForEvent(t, ui)
	if event.name != "profiledeck:open-switch" || len(event.data) != 1 {
		t.Fatalf("expected Antigravity switch event, got %#v", event)
	}
	payload, ok := event.data[0].(map[string]string)
	if !ok || payload["provider_id"] != agyconfig.ProviderID || payload["profile_id"] != "work" {
		t.Fatalf("unexpected Antigravity switch payload: %#v", event.data)
	}
}

func TestTrayControllerRefreshDropsStaleDashboard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services := newDesktopTestServices(t, backend.Environment{ConfigDir: t.TempDir()})
	ui := newFakeTrayUI()
	controller := newTrayController(ctx, services, ui)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var loads atomic.Int32
	controller.loadDashboard = func(context.Context) (backend.DashboardResult, error) {
		if loads.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return dashboardWithCodexProfiles(codexProfileSummary("old", "Old", false)), nil
		}
		return dashboardWithCodexProfiles(codexProfileSummary("new", "New", false)), nil
	}

	controller.Refresh(nil, false)
	select {
	case <-firstStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected first refresh to start")
	}

	event := backend.DesktopChangeEvent{Kind: backend.DesktopChangeCodexProfileChanged}
	controller.Refresh(&event, true)
	menu := waitForMenu(t, ui)
	submenu := requireMenuSubmenu(t, menu, "Codex Profiles")
	if got := submenu.ItemAt(0).Label(); got != "New" {
		t.Fatalf("expected latest dashboard menu, got %q", got)
	}
	emitted := waitForEvent(t, ui)
	if emitted.name != "profiledeck:dashboard-updated" {
		t.Fatalf("expected dashboard update event, got %q", emitted.name)
	}

	close(releaseFirst)
	select {
	case stale := <-ui.menus:
		t.Fatalf("expected stale refresh to be dropped, got menu %#v", stale)
	case stale := <-ui.events:
		t.Fatalf("expected stale refresh not to emit, got event %#v", stale)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestTrayControllerMenuRefreshDoesNotDropPendingDashboardEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services := newDesktopTestServices(t, backend.Environment{ConfigDir: t.TempDir()})
	ui := newFakeTrayUI()
	controller := newTrayController(ctx, services, ui)

	eventStarted := make(chan struct{})
	releaseEvent := make(chan struct{})
	var loads atomic.Int32
	controller.loadDashboard = func(context.Context) (backend.DashboardResult, error) {
		switch loads.Add(1) {
		case 1:
			close(eventStarted)
			<-releaseEvent
			return dashboardWithCodexProfiles(codexProfileSummary("event", "Event", false)), nil
		case 2:
			return dashboardWithCodexProfiles(codexProfileSummary("manual", "Manual", false)), nil
		default:
			return backend.DashboardResult{}, fmt.Errorf("unexpected dashboard load")
		}
	}

	event := backend.DesktopChangeEvent{Kind: backend.DesktopChangeCodexProfileChanged}
	controller.Refresh(&event, true)
	select {
	case <-eventStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected event refresh to start")
	}

	controller.Refresh(nil, false)
	menu := waitForMenu(t, ui)
	submenu := requireMenuSubmenu(t, menu, "Codex Profiles")
	if got := submenu.ItemAt(0).Label(); got != "Manual" {
		t.Fatalf("expected menu-only refresh to update tray menu, got %q", got)
	}

	close(releaseEvent)
	emitted := waitForEvent(t, ui)
	if emitted.name != "profiledeck:dashboard-updated" {
		t.Fatalf("expected pending dashboard event to survive menu refresh, got %q", emitted.name)
	}
	if len(emitted.data) != 1 {
		t.Fatalf("expected one dashboard update payload, got %#v", emitted.data)
	}
	payload, ok := emitted.data[0].(backend.DashboardUpdatePayload)
	if !ok {
		t.Fatalf("expected dashboard update payload, got %#v", emitted.data)
	}
	if payload.Event.Kind != backend.DesktopChangeCodexProfileChanged {
		t.Fatalf("expected profile event payload, got %q", payload.Event.Kind)
	}
	if got := payload.Dashboard.CodexProfiles.Profiles[0].Profile.Name; got != "Event" {
		t.Fatalf("expected event dashboard payload, got %q", got)
	}
	select {
	case stale := <-ui.menus:
		t.Fatalf("expected event refresh not to overwrite newer menu, got %#v", stale)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestTrayControllerRefreshSetsMenuBeforeDashboardEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services := newDesktopTestServices(t, backend.Environment{ConfigDir: t.TempDir()})
	ui := newFakeTrayUI()
	controller := newTrayController(ctx, services, ui)
	controller.loadDashboard = func(context.Context) (backend.DashboardResult, error) {
		return dashboardWithCodexProfiles(codexProfileSummary("work", "Work", true)), nil
	}

	event := backend.DesktopChangeEvent{Kind: backend.DesktopChangeCodexProfileChanged}
	controller.Refresh(&event, true)

	if got := waitForTrayUICall(t, ui); got != "set-menu" {
		t.Fatalf("expected SetMenu before event emit, got %q", got)
	}
	if got := waitForTrayUICall(t, ui); got != "emit:profiledeck:dashboard-updated" {
		t.Fatalf("expected dashboard update emit after SetMenu, got %q", got)
	}
}

func TestTrayErrorLabelDoesNotExposeRawError(t *testing.T) {
	rawPath := "/Users/alice/Library/Application Support/profiledeck/profiledeck.db"
	err := fmt.Errorf("open %s: permission denied", rawPath)

	for _, label := range []string{
		trayErrorLabel(err, trayDashboardUnavailableLabel),
		trayErrorLabel(err, trayCodexProfilesUnavailableLabel),
		trayErrorLabel(err, trayClaudeCodeProfilesUnavailableLabel),
	} {
		if strings.Contains(label, rawPath) || strings.Contains(label, "permission denied") {
			t.Fatalf("expected tray label to omit raw error details, got %q", label)
		}
		if !strings.Contains(label, "Open ProfileDeck") {
			t.Fatalf("expected tray label to guide user to main window, got %q", label)
		}
	}
}

type fakeTrayEvent struct {
	name string
	data []any
}

type fakeTrayUI struct {
	menus  chan *application.Menu
	events chan fakeTrayEvent
	calls  chan string
}

func newFakeTrayUI() *fakeTrayUI {
	return &fakeTrayUI{
		menus:  make(chan *application.Menu, 4),
		events: make(chan fakeTrayEvent, 4),
		calls:  make(chan string, 8),
	}
}

func (ui *fakeTrayUI) SetMenu(menu *application.Menu) {
	ui.calls <- "set-menu"
	ui.menus <- menu
}

func (ui *fakeTrayUI) Emit(name string, data ...any) {
	ui.calls <- "emit:" + name
	ui.events <- fakeTrayEvent{name: name, data: data}
}

func (ui *fakeTrayUI) ShowMainWindow() {}

func (ui *fakeTrayUI) HideTray() {}

func (ui *fakeTrayUI) Quit() {}

func waitForMenu(t *testing.T, ui *fakeTrayUI) *application.Menu {
	t.Helper()
	select {
	case menu := <-ui.menus:
		return menu
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected menu update")
		return nil
	}
}

func waitForEvent(t *testing.T, ui *fakeTrayUI) fakeTrayEvent {
	t.Helper()
	select {
	case event := <-ui.events:
		return event
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected emitted event")
		return fakeTrayEvent{}
	}
}

func waitForTrayUICall(t *testing.T, ui *fakeTrayUI) string {
	t.Helper()
	select {
	case call := <-ui.calls:
		return call
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected tray UI call")
		return ""
	}
}

func requireMenuSubmenu(t *testing.T, menu *application.Menu, label string) *application.Menu {
	t.Helper()
	item := menu.FindByLabel(label)
	if item == nil {
		t.Fatalf("expected submenu %q", label)
	}
	submenu := item.GetSubmenu()
	if submenu == nil {
		t.Fatalf("expected %q to be a submenu", label)
	}
	return submenu
}

func dashboardWithCodexProfiles(profiles ...codex.CodexProfileSummary) backend.DashboardResult {
	return backend.DashboardResult{
		CodexProfiles: &codex.CodexProfileListResult{Profiles: profiles},
	}
}

func dashboardWithAntigravityProfiles(profiles ...antigravity.AntigravityProfileSummary) backend.DashboardResult {
	return backend.DashboardResult{AntigravityProfiles: &antigravity.AntigravityProfileListResult{Profiles: profiles}}
}

func dashboardWithClaudeCodeProfiles(profiles ...claudecode.ClaudeCodeProfileSummary) backend.DashboardResult {
	return backend.DashboardResult{ClaudeCodeProfiles: &claudecode.ClaudeCodeProfileListResult{Profiles: profiles}}
}

func codexProfileSummary(profileID, name string, active bool) codex.CodexProfileSummary {
	return codex.CodexProfileSummary{
		Profile: profile.Profile{
			ID:   profileID,
			Name: name,
		},
		ProviderID: codexconfig.ProviderID,
		Active:     active,
	}
}

func antigravityProfileSummary(profileID, name string, active bool) antigravity.AntigravityProfileSummary {
	return antigravity.AntigravityProfileSummary{
		Profile: profile.Profile{ID: profileID, Name: name}, ProviderID: agyconfig.ProviderID, Active: active,
	}
}

func claudeCodeProfileSummary(profileID, name string, active bool) claudecode.ClaudeCodeProfileSummary {
	return claudecode.ClaudeCodeProfileSummary{
		Profile: profile.Profile{ID: profileID, Name: name}, ProviderID: claudecodeconfig.ProviderID, Active: active,
	}
}
