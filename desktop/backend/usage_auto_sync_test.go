package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/usage"
)

func TestUsageAutoSyncStartsImmediatelyAndSkipsOverlappingTicks(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	started := make(chan int32, 3)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		call := calls.Add(1)
		started <- call
		if call == 1 {
			<-releaseFirst
		}
		return usage.UsageSyncResult{}, nil
	})
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

func TestUsageAutoSyncNoopDoesNotPublishOrAdvanceStatus(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	called := make(chan struct{}, 2)
	runtime.syncProvider = func(context.Context, func()) (usage.BackgroundSyncOutcome, error) {
		called <- struct{}{}
		return usage.BackgroundSyncOutcome{
			Result: usage.UsageSyncResult{ProviderID: codexconfig.ProviderID},
		}, nil
	}
	statuses := make(chan UsageAutoSyncStatus, 4)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)
	waitUsageSyncSignal(t, called)

	initial := runtime.Status()
	if initial.Revision != 0 || initial.Syncing || initial.Outcome != UsageAutoSyncOutcomeIdle ||
		initial.LastStartedAtUnixMS != 0 || initial.LastCompletedAtUnixMS != 0 {
		t.Fatalf("no-op startup changed status: %#v", initial)
	}
	select {
	case status := <-statuses:
		t.Fatalf("no-op startup published status: %#v", status)
	case <-time.After(50 * time.Millisecond):
	}

	ticker.tick()
	waitUsageSyncSignal(t, called)
	if status := runtime.Status(); status != initial {
		t.Fatalf("repeated no-op advanced status: before=%#v after=%#v", initial, status)
	}
	select {
	case status := <-statuses:
		t.Fatalf("repeated no-op published status: %#v", status)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUsageAutoSyncNoopClearsRecoveredError(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	called := make(chan struct{}, 2)
	var calls atomic.Int32
	runtime.syncProvider = func(context.Context, func()) (usage.BackgroundSyncOutcome, error) {
		called <- struct{}{}
		if calls.Add(1) == 1 {
			return usage.BackgroundSyncOutcome{}, errors.New("temporary failure")
		}
		return usage.BackgroundSyncOutcome{}, nil
	}
	statuses := make(chan UsageAutoSyncStatus, 4)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)
	waitUsageSyncSignal(t, called)
	waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeError
	})

	ticker.tick()
	waitUsageSyncSignal(t, called)
	recovered := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeIdle
	})
	if recovered.Error != nil || recovered.ImportErrorCount != 0 ||
		recovered.LastSuccessAtUnixMS != 0 {
		t.Fatalf("no-op recovery status = %#v", recovered)
	}
}

func TestUsageAutoSyncResetsIntervalWithoutImmediateSync(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	started := make(chan struct{}, 2)
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		started <- struct{}{}
		return usage.UsageSyncResult{}, nil
	})
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

func TestUsageAutoSyncStartDelayDefersFirstSync(t *testing.T) {
	runtime, _ := newTestUsageAutoSyncRuntime()
	delayRequested := make(chan time.Duration, 1)
	runtime.startDelayFunc = func() time.Duration { return 5 * time.Millisecond }
	runtime.afterFunc = func(d time.Duration) <-chan time.Time {
		delayRequested <- d
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	syncStarted := make(chan struct{}, 1)
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		syncStarted <- struct{}{}
		return usage.UsageSyncResult{ProviderID: "codex"}, nil
	})
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)

	select {
	case d := <-delayRequested:
		if d != 5*time.Millisecond {
			t.Fatalf("start delay = %v, want 5ms", d)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("start delay was not scheduled")
	}
	select {
	case <-syncStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first sync did not run after start delay")
	}
}

func TestUsageAutoSyncSyncNowDoesNotWaitForStartDelay(t *testing.T) {
	runtime, _ := newTestUsageAutoSyncRuntime()
	releaseDelay := make(chan struct{})
	runtime.startDelayFunc = func() time.Duration { return time.Hour }
	runtime.afterFunc = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time)
		go func() {
			<-releaseDelay
			ch <- time.Now()
		}()
		return ch
	}
	syncStarted := make(chan struct{}, 1)
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		syncStarted <- struct{}{}
		return usage.UsageSyncResult{ProviderID: "codex"}, nil
	})
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() { close(releaseDelay) })

	status := runtime.SyncNow(context.Background())
	if status.Outcome != UsageAutoSyncOutcomeSuccess {
		t.Fatalf("SyncNow during start delay = %#v", status)
	}
	select {
	case <-syncStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SyncNow did not start a sync during start delay")
	}
}

func TestRandomUsageAutoSyncStartDelayIsWithinRange(t *testing.T) {
	for i := 0; i < 50; i++ {
		d := randomUsageAutoSyncStartDelay()
		if d < usageAutoSyncStartDelayMin || d > usageAutoSyncStartDelayMax {
			t.Fatalf("delay %v outside [%v, %v]", d, usageAutoSyncStartDelayMin, usageAutoSyncStartDelayMax)
		}
	}
}

func TestUsageAutoSyncStatusIsProviderScoped(t *testing.T) {
	codex := newUsageAutoSyncRuntime(
		"codex",
		func(context.Context) (usage.ProviderSyncSettings, error) {
			return usage.ProviderSyncSettings{UsageSyncIntervalSeconds: 5}, nil
		},
		performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{ProviderID: "codex"}, nil
		}),
	)
	grok := newUsageAutoSyncRuntime(
		"grok-build",
		func(context.Context) (usage.ProviderSyncSettings, error) {
			return usage.ProviderSyncSettings{UsageSyncIntervalSeconds: 60}, nil
		},
		performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{ProviderID: "grok-build"}, nil
		}),
	)
	codex.SetInterval(15)
	grok.SetInterval(30)
	codexStatus := codex.Status()
	grokStatus := grok.Status()
	if codexStatus.ProviderID != "codex" ||
		codexStatus.IntervalSeconds != 15 ||
		grokStatus.ProviderID != "grok-build" ||
		grokStatus.IntervalSeconds != 30 {
		t.Fatalf("provider statuses crossed: codex=%#v grok=%#v", codexStatus, grokStatus)
	}
}

func TestUsageAutoSyncStartupLoadDoesNotOverwriteNewerInterval(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	runtime.loadSettings = func(context.Context) (usage.ProviderSyncSettings, error) {
		close(loadStarted)
		<-releaseLoad
		return usage.ProviderSyncSettings{UsageSyncIntervalSeconds: 15}, nil
	}
	createdIntervals := make(chan time.Duration, 1)
	runtime.newTicker = func(interval time.Duration) usageAutoSyncTicker {
		createdIntervals <- interval
		return ticker
	}
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		return usage.UsageSyncResult{}, nil
	})
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

func TestUsageAutoSyncStartupLoadFailureDoesNotSupersedeSyncNow(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	runtime.loadSettings = func(context.Context) (usage.ProviderSyncSettings, error) {
		close(loadStarted)
		<-releaseLoad
		return usage.ProviderSyncSettings{}, fmt.Errorf("settings unavailable")
	}
	tickerCreated := make(chan struct{})
	runtime.newTicker = func(time.Duration) usageAutoSyncTicker {
		close(tickerCreated)
		return ticker
	}
	syncStarted := make(chan int32, 2)
	releaseSync := make(chan struct{})
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		syncStarted <- calls.Add(1)
		<-releaseSync
		return usage.UsageSyncResult{}, nil
	})
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() {
		select {
		case <-releaseLoad:
		default:
			close(releaseLoad)
		}
		select {
		case <-releaseSync:
		default:
			close(releaseSync)
		}
	})
	waitUsageSyncSignal(t, loadStarted)

	completed := make(chan UsageAutoSyncStatus, 1)
	go func() {
		completed <- runtime.SyncNow(context.Background())
	}()
	if call := waitUsageSyncCall(t, syncStarted); call != 1 {
		t.Fatalf("expected requested sync, got %d", call)
	}
	close(releaseLoad)
	waitUsageSyncSignal(t, tickerCreated)
	select {
	case call := <-syncStarted:
		t.Fatalf("startup failure started an overlapping sync: %d", call)
	case <-time.After(50 * time.Millisecond):
	}
	if status := runtime.Status(); !status.Syncing || status.Outcome != UsageAutoSyncOutcomeSyncing {
		t.Fatalf("startup failure superseded active sync: %#v", status)
	}

	close(releaseSync)
	select {
	case status := <-completed:
		if status.Syncing || status.Outcome != UsageAutoSyncOutcomeSuccess {
			t.Fatalf("requested sync result = %#v", status)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SyncNow did not return after the Provider sync completed")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one Provider sync, got %d", got)
	}
}

func TestUsageAutoSyncStartupLoadFailureDoesNotOverwriteCompletedSyncNow(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	runtime.loadSettings = func(context.Context) (usage.ProviderSyncSettings, error) {
		close(loadStarted)
		<-releaseLoad
		return usage.ProviderSyncSettings{}, fmt.Errorf("settings unavailable")
	}
	tickerCreated := make(chan struct{})
	runtime.newTicker = func(time.Duration) usageAutoSyncTicker {
		close(tickerCreated)
		return ticker
	}
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		calls.Add(1)
		return usage.UsageSyncResult{}, nil
	})
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() {
		select {
		case <-releaseLoad:
		default:
			close(releaseLoad)
		}
	})
	waitUsageSyncSignal(t, loadStarted)

	completed := runtime.SyncNow(context.Background())
	if completed.Syncing || completed.Outcome != UsageAutoSyncOutcomeSuccess {
		t.Fatalf("requested sync result = %#v", completed)
	}
	close(releaseLoad)
	waitUsageSyncSignal(t, tickerCreated)
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("startup failure repeated a completed Provider sync, calls=%d", got)
	}
	if status := runtime.Status(); status.Outcome != UsageAutoSyncOutcomeSuccess || status.Error != nil {
		t.Fatalf("startup failure overwrote completed sync: %#v", status)
	}
}

func TestUsageAutoSyncRetriesAfterTimeout(t *testing.T) {
	runtime, ticker := newTestUsageAutoSyncRuntime()
	runtime.timeout = 20 * time.Millisecond
	releaseRetry := make(chan struct{})
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(ctx context.Context) (usage.UsageSyncResult, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return usage.UsageSyncResult{}, ctx.Err()
		}
		<-releaseRetry
		return usage.UsageSyncResult{}, nil
	})
	statuses := make(chan UsageAutoSyncStatus, 16)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() {
		select {
		case <-releaseRetry:
		default:
			close(releaseRetry)
		}
	})

	timedOut := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeError
	})
	if timedOut.Error == nil || timedOut.Error.Code != "TIMEOUT" {
		t.Fatalf("expected timeout status, got %#v", timedOut)
	}
	ticker.tick()
	retrying := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeSyncing
	})
	if retrying.Error != nil || retrying.Revision <= timedOut.Revision {
		t.Fatalf("retry kept a stale error status: timedOut=%#v retrying=%#v", timedOut, retrying)
	}
	close(releaseRetry)
	retried := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeSuccess
	})
	if calls.Load() != 2 || retried.Error != nil || retried.LastSuccessAtUnixMS == 0 {
		t.Fatalf("expected successful retry, calls=%d status=%#v", calls.Load(), retried)
	}
}

func TestUsageAutoSyncSyncNowStartsAndWaitsForProviderResult(t *testing.T) {
	runtime, _ := newTestUsageAutoSyncRuntime()
	started := make(chan int32, 2)
	releaseRequested := make(chan struct{})
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		call := calls.Add(1)
		started <- call
		if call == 2 {
			<-releaseRequested
		}
		return usage.UsageSyncResult{}, nil
	})
	statuses := make(chan UsageAutoSyncStatus, 16)
	runtime.Start(context.Background(), func(status UsageAutoSyncStatus) { statuses <- status })
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() {
		select {
		case <-releaseRequested:
		default:
			close(releaseRequested)
		}
	})

	if call := waitUsageSyncCall(t, started); call != 1 {
		t.Fatalf("expected startup sync, got %d", call)
	}
	initial := waitUsageAutoSyncStatus(t, statuses, func(status UsageAutoSyncStatus) bool {
		return status.Outcome == UsageAutoSyncOutcomeSuccess
	})

	completed := make(chan UsageAutoSyncStatus, 1)
	go func() {
		completed <- runtime.SyncNow(context.Background())
	}()
	if call := waitUsageSyncCall(t, started); call != 2 {
		t.Fatalf("expected requested sync, got %d", call)
	}
	select {
	case status := <-completed:
		t.Fatalf("SyncNow returned before the Provider sync completed: %#v", status)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequested)

	select {
	case status := <-completed:
		if status.Outcome != UsageAutoSyncOutcomeSuccess ||
			status.Syncing ||
			status.Error != nil ||
			status.Revision <= initial.Revision {
			t.Fatalf("SyncNow result = %#v, initial = %#v", status, initial)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SyncNow did not return after the Provider sync completed")
	}
}

func TestUsageAutoSyncSyncNowJoinsActiveProviderSync(t *testing.T) {
	runtime, _ := newTestUsageAutoSyncRuntime()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return usage.UsageSyncResult{}, nil
	})
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	waitUsageSyncSignal(t, started)

	completed := make(chan UsageAutoSyncStatus, 1)
	go func() {
		completed <- runtime.SyncNow(context.Background())
	}()
	select {
	case status := <-completed:
		t.Fatalf("SyncNow returned before the active sync completed: %#v", status)
	case <-time.After(50 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("SyncNow started an overlapping sync, calls=%d", got)
	}

	close(release)
	select {
	case status := <-completed:
		if status.Outcome != UsageAutoSyncOutcomeSuccess || status.Syncing {
			t.Fatalf("SyncNow joined result = %#v", status)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SyncNow did not return after the active sync completed")
	}
}

func TestUsageAutoSyncReportsWarningsAndRedactsFatalErrors(t *testing.T) {
	t.Run("warning", func(t *testing.T) {
		runtime, _ := newTestUsageAutoSyncRuntime()
		runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{Errors: []usage.UsageImportError{{SourceKey: "/private/session.jsonl", Message: "raw session error"}}}, nil
		})
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
		runtime.syncProvider = performedUsageSync(func(context.Context) (usage.UsageSyncResult, error) {
			return usage.UsageSyncResult{}, fmt.Errorf("failed to read %s", raw)
		})
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
	runtime.syncProvider = performedUsageSync(func(ctx context.Context) (usage.UsageSyncResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return usage.UsageSyncResult{}, ctx.Err()
	})
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
	runtime := newUsageAutoSyncRuntime(codexconfig.ProviderID, nil, nil)
	ticker := &fakeUsageAutoSyncTicker{
		ch:     make(chan time.Time),
		resets: make(chan time.Duration, 4),
	}
	runtime.loadSettings = func(context.Context) (usage.ProviderSyncSettings, error) {
		return usage.ProviderSyncSettings{UsageSyncIntervalSeconds: usage.UsageSyncIntervalDefault}, nil
	}
	runtime.newTicker = func(time.Duration) usageAutoSyncTicker { return ticker }
	runtime.startDelayFunc = func() time.Duration { return 0 }
	return runtime, ticker
}

func performedUsageSync(
	sync func(context.Context) (usage.UsageSyncResult, error),
) backgroundUsageSync {
	return func(ctx context.Context, onWorkDetected func()) (usage.BackgroundSyncOutcome, error) {
		if onWorkDetected != nil {
			onWorkDetected()
		}
		result, err := sync(ctx)
		return usage.BackgroundSyncOutcome{Result: result, Performed: true}, err
	}
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
