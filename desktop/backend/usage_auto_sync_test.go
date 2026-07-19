package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
	"github.com/strahe/profiledeck/internal/usage"
)

func TestUsageAutoSyncStartsImmediatelyAndSkipsOverlappingTicks(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	started := make(chan int32, 3)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	runtime.syncCodex = func(context.Context) (usage.UsageSyncResult, error) {
		call := calls.Add(1)
		started <- call
		if call == 1 {
			<-releaseFirst
		}
		return usage.UsageSyncResult{}, nil
	}
	statuses := make(chan UsageAutoSyncStatus, 16)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)

	if call := waitUsageSyncCall(t, started); call != 1 {
		t.Fatalf("expected immediate first sync, got call %d", call)
	}
	ticker.tick()
	select {
	case call := <-started:
		t.Fatalf("expected overlapping tick to be skipped, got call %d", call)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeSuccess
	})
	ticker.tick()
	if call := waitUsageSyncCall(t, started); call != 2 {
		t.Fatalf("expected next fixed tick to start a sync, got call %d", call)
	}
}

func TestUsageAutoSyncResetsIntervalWithoutImmediateSync(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	started := make(chan struct{}, 2)
	runtime.syncCodex = func(context.Context) (usage.UsageSyncResult, error) {
		started <- struct{}{}
		return usage.UsageSyncResult{}, nil
	}
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)

	waitUsageSyncSignal(t, started)
	runtime.SetInterval(30)
	select {
	case interval := <-ticker.resets:
		if interval != 30*time.Second {
			t.Fatalf("expected 30 second reset, got %s", interval)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected ticker reset")
	}
	select {
	case <-started:
		t.Fatalf("expected interval update not to start an extra sync")
	case <-time.After(50 * time.Millisecond):
	}
	if status := runtime.Status(); status.IntervalSeconds != 30 {
		t.Fatalf("expected updated interval status, got %#v", status)
	}
}

func TestUsageAutoSyncStartupLoadDoesNotOverwriteNewerInterval(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	runtime.loadSettings = func(context.Context) (codex.CodexSettings, error) {
		close(loadStarted)
		<-releaseLoad
		return codex.CodexSettings{UsageSyncIntervalSeconds: 15}, nil
	}
	createdIntervals := make(chan time.Duration, 1)
	runtime.newTicker = func(interval time.Duration) usageAutoSyncTicker {
		createdIntervals <- interval
		return ticker
	}
	runtime.syncCodex = func(context.Context) (usage.UsageSyncResult, error) {
		return usage.UsageSyncResult{}, nil
	}
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	select {
	case <-loadStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected startup settings load")
	}

	runtime.SetInterval(60)
	close(releaseLoad)
	select {
	case interval := <-createdIntervals:
		if interval != 60*time.Second {
			t.Fatalf("expected startup ticker to use newer interval, got %s", interval)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected startup ticker creation")
	}
	if status := runtime.Status(); status.IntervalSeconds != 60 {
		t.Fatalf("expected newer interval to remain active, got %#v", status)
	}
}

func TestUsageAutoSyncRetriesAfterTimeout(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	runtime.timeout = 20 * time.Millisecond
	var calls atomic.Int32
	runtime.syncCodex = func(ctx context.Context) (usage.UsageSyncResult, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return usage.UsageSyncResult{}, ctx.Err()
		}
		return usage.UsageSyncResult{}, nil
	}
	statuses := make(chan UsageAutoSyncStatus, 16)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)

	timedOut := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeError
	})
	if timedOut.Error == nil || timedOut.Error.Code != "TIMEOUT" {
		t.Fatalf("expected timeout status, got %#v", timedOut)
	}
	ticker.tick()
	retried := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeSuccess
	})
	if calls.Load() != 2 || retried.Error != nil || retried.LastSuccessAtUnixMS == 0 {
		t.Fatalf("expected successful retry, calls=%d status=%#v", calls.Load(), retried)
	}
}

func TestUsageAutoSyncReportsWarningsAndRedactsFatalErrors(t *testing.T) {
	t.Run("warning", func(t *testing.T) {
		runtime, _ := newTestUsageAutoSyncRuntime()
		runtime.syncCodex = func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{Errors: []usage.UsageImportError{{SourceKey: "/private/session.jsonl", Message: "raw session error"}}}, nil
		}
		statuses := make(chan UsageAutoSyncStatus, 8)
		runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
		t.Cleanup(runtime.Stop)

		status := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
			return status.Outcome == UsageAutoSyncOutcomeWarning
		})
		if status.ImportErrorCount != 1 || status.Error != nil {
			t.Fatalf("expected warning count without raw error payload, got %#v", status)
		}
	})

	t.Run("fatal", func(t *testing.T) {
		runtime, _ := newTestUsageAutoSyncRuntime()
		raw := "/Users/alice/.codex/sessions/private.jsonl"
		runtime.syncCodex = func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{}, fmt.Errorf("failed to read %s", raw)
		}
		statuses := make(chan UsageAutoSyncStatus, 8)
		runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
		t.Cleanup(runtime.Stop)

		status := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
			return status.Outcome == UsageAutoSyncOutcomeError
		})
		if status.Error == nil || status.Error.Code != string(apperror.CommandFailed) {
			t.Fatalf("expected structured fatal error, got %#v", status)
		}
		if strings.Contains(status.Error.Message, raw) {
			t.Fatalf("expected fatal status to redact local path, got %#v", status.Error)
		}
	})
}

func TestUsageAutoSyncStopCancelsRunningSync(t *testing.T) {
	runtime, _ := newTestUsageAutoSyncRuntime()
	started := make(chan struct{})
	stopped := make(chan struct{})
	runtime.syncCodex = func(ctx context.Context) (usage.UsageSyncResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return usage.UsageSyncResult{}, ctx.Err()
	}
	runtime.Start(context.Background(), nil)
	waitUsageSyncSignal(t, started)
	runtime.Stop()

	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected shutdown to cancel running sync")
	}
}

type fakeUsageAutoSyncTicker struct {
	ch       chan time.Time
	resets   chan time.Duration
	stopOnce sync.Once
}

func (t *fakeUsageAutoSyncTicker) C() <-chan time.Time {
	return t.ch
}

func (t *fakeUsageAutoSyncTicker) Reset(interval time.Duration) {
	t.resets <- interval
}

func (t *fakeUsageAutoSyncTicker) Stop() {
	t.stopOnce.Do(func() {})
}

func (t *fakeUsageAutoSyncTicker) tick() {
	t.ch <- time.Now()
}

func newTestUsageAutoSyncRuntime() (*usageAutoSyncRuntime, *fakeUsageAutoSyncTicker) {
	runtime := newUsageAutoSyncRuntime(nil, nil)
	ticker := &fakeUsageAutoSyncTicker{
		ch:     make(chan time.Time),
		resets: make(chan time.Duration, 4),
	}
	runtime.loadSettings = func(context.Context) (codex.CodexSettings, error) {
		return codex.CodexSettings{UsageSyncIntervalSeconds: codex.CodexUsageSyncIntervalDefault}, nil
	}
	runtime.newTicker = func(time.Duration) usageAutoSyncTicker { return ticker }
	return runtime, ticker
}

func waitUsageSyncCall(t *testing.T, calls <-chan int32) int32 {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected usage sync call")
		return 0
	}
}

func waitUsageSyncSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected usage sync signal")
	}
}

func waitUsageAutoSyncStatus(t *testing.T, statuses <-chan UsageAutoSyncStatus, match func(UsageAutoSyncStatus) bool) UsageAutoSyncStatus {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case status := <-statuses:
			if match(status) {
				return status
			}
		case <-timer.C:
			t.Fatalf("expected matching usage auto-sync status")
			return UsageAutoSyncStatus{}
		}
	}
}
