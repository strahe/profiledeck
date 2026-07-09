package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/strahe/profiledeck/desktop/backend"
	"github.com/strahe/profiledeck/internal/app"
)

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

func TestCodexProfilesReturnsSharedListError(t *testing.T) {
	services := backend.NewServices(app.DefaultInfo(), backend.Environment{ConfigDir: t.TempDir()}, nil)

	_, err := codexProfiles(context.Background(), services, backend.DashboardResult{})

	if err == nil {
		t.Fatalf("expected Codex profile listing error")
	}
}

func TestTrayErrorLabelDoesNotExposeRawError(t *testing.T) {
	rawPath := "/Users/alice/Library/Application Support/profiledeck/profiledeck.db"
	err := fmt.Errorf("open %s: permission denied", rawPath)

	for _, label := range []string{
		trayErrorLabel(err, trayDashboardUnavailableLabel),
		trayErrorLabel(err, trayCodexProfilesUnavailableLabel),
	} {
		if strings.Contains(label, rawPath) || strings.Contains(label, "permission denied") {
			t.Fatalf("expected tray label to omit raw error details, got %q", label)
		}
		if !strings.Contains(label, "Open ProfileDeck") {
			t.Fatalf("expected tray label to guide user to main window, got %q", label)
		}
	}
}
