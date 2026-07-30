package backend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/usage"
)

const (
	UsageAutoSyncEventName = "profiledeck:usage-sync-status"

	UsageAutoSyncOutcomeIdle    = "idle"
	UsageAutoSyncOutcomeSyncing = "syncing"
	UsageAutoSyncOutcomeSuccess = "success"
	UsageAutoSyncOutcomeWarning = "warning"
	UsageAutoSyncOutcomeError   = "error"

	usageAutoSyncTimeout = 30 * time.Second
)

type UsageAutoSyncError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type UsageAutoSyncStatus struct {
	ProviderID            string              `json:"provider_id"`
	Revision              uint64              `json:"revision"`
	IntervalSeconds       int                 `json:"interval_seconds"`
	Syncing               bool                `json:"syncing"`
	Outcome               string              `json:"outcome"`
	LastStartedAtUnixMS   int64               `json:"last_started_at_unix_ms"`
	LastCompletedAtUnixMS int64               `json:"last_completed_at_unix_ms"`
	LastSuccessAtUnixMS   int64               `json:"last_success_at_unix_ms"`
	ImportErrorCount      int64               `json:"import_error_count"`
	Error                 *UsageAutoSyncError `json:"error,omitempty"`
}

type usageAutoSyncTicker interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop()
}

type realUsageAutoSyncTicker struct {
	ticker *time.Ticker
}

func (t realUsageAutoSyncTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realUsageAutoSyncTicker) Reset(interval time.Duration) {
	t.ticker.Reset(interval)
}

func (t realUsageAutoSyncTicker) Stop() {
	t.ticker.Stop()
}

type usageAutoSyncRuntime struct {
	mu               sync.RWMutex
	status           UsageAutoSyncStatus
	emitter          func(UsageAutoSyncStatus)
	intervalRevision uint64

	lifecycleMu sync.Mutex
	started     bool
	runCtx      context.Context
	cancel      context.CancelFunc
	pause       chan struct{}
	loopWG      sync.WaitGroup
	workerWG    sync.WaitGroup
	syncDone    chan struct{}

	intervalUpdates chan struct{}
	now             func() time.Time
	newTicker       func(time.Duration) usageAutoSyncTicker
	timeout         time.Duration
	loadSettings    func(context.Context) (usage.ProviderSyncSettings, error)
	syncProvider    func(context.Context) (usage.UsageSyncResult, error)
}

func newUsageAutoSyncRuntime(
	providerID string,
	loadSettings func(context.Context) (usage.ProviderSyncSettings, error),
	syncProvider func(context.Context) (usage.UsageSyncResult, error),
) *usageAutoSyncRuntime {
	providerID = strings.TrimSpace(providerID)
	return &usageAutoSyncRuntime{
		status:          defaultUsageAutoSyncStatus(providerID),
		intervalUpdates: make(chan struct{}, 1),
		now:             time.Now,
		newTicker: func(interval time.Duration) usageAutoSyncTicker {
			return realUsageAutoSyncTicker{ticker: time.NewTicker(interval)}
		},
		timeout:      usageAutoSyncTimeout,
		loadSettings: loadSettings,
		syncProvider: syncProvider,
	}
}

func defaultUsageAutoSyncStatus(providerID string) UsageAutoSyncStatus {
	return UsageAutoSyncStatus{
		ProviderID:      strings.TrimSpace(providerID),
		IntervalSeconds: usage.UsageSyncIntervalDefault,
		Outcome:         UsageAutoSyncOutcomeIdle,
	}
}

func (r *usageAutoSyncRuntime) Start(ctx context.Context, emitter func(UsageAutoSyncStatus)) {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	if r.started {
		r.lifecycleMu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	pause := make(chan struct{})
	r.mu.Lock()
	r.emitter = emitter
	r.mu.Unlock()
	// Publish the started state only after the wait group is armed. Otherwise a
	// concurrent shutdown could call Wait while Start is still about to call Add.
	r.loopWG.Add(1)
	r.started = true
	r.runCtx = runCtx
	r.cancel = cancel
	r.pause = pause
	go r.run(runCtx, pause)
	r.lifecycleMu.Unlock()
}

func (r *usageAutoSyncRuntime) SetEmitter(emitter func(UsageAutoSyncStatus)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.emitter = emitter
	r.mu.Unlock()
}

func (r *usageAutoSyncRuntime) startAgentRuntime(ctx context.Context) {
	r.mu.RLock()
	emitter := r.emitter
	r.mu.RUnlock()
	r.Start(ctx, emitter)
}

func (r *usageAutoSyncRuntime) Stop() {
	r.stop(false)
}

func (r *usageAutoSyncRuntime) stopAgentRuntime() {
	r.stop(true)
}

func (r *usageAutoSyncRuntime) stop(graceful bool) {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	if !r.started {
		r.lifecycleMu.Unlock()
		return
	}
	// Keep lifecycle transitions serialized until the old loop and worker have
	// exited. This prevents a concurrent Start or second Stop from reusing and
	// then clobbering the wait groups for a newer scheduler generation.
	if graceful {
		close(r.pause)
	} else {
		r.cancel()
	}
	r.loopWG.Wait()
	r.workerWG.Wait()
	if graceful {
		r.cancel()
	}
	r.started = false
	r.runCtx = nil
	r.cancel = nil
	r.pause = nil
	r.lifecycleMu.Unlock()
}

func (r *usageAutoSyncRuntime) Status() UsageAutoSyncStatus {
	if r == nil {
		return defaultUsageAutoSyncStatus("")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneUsageAutoSyncStatus(r.status)
}

func (r *usageAutoSyncRuntime) SetInterval(interval int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.status.IntervalSeconds = interval
	r.status.Revision++
	r.intervalRevision++
	r.mu.Unlock()

	// The channel is a wake-up signal; the loop reads the latest value under
	// the status lock. Concurrent updates therefore coalesce without allowing
	// an older queued interval to win.
	select {
	case r.intervalUpdates <- struct{}{}:
	default:
	}
}

// SyncNow joins the current Provider sync or starts one under the scheduler's
// lifecycle context. It never resumes a runtime paused by Agent preferences.
func (r *usageAutoSyncRuntime) SyncNow(ctx context.Context) UsageAutoSyncStatus {
	if r == nil {
		return defaultUsageAutoSyncStatus("")
	}
	r.lifecycleMu.Lock()
	if !r.started || r.runCtx == nil {
		r.lifecycleMu.Unlock()
		return r.Status()
	}
	r.startSync(r.runCtx)
	r.mu.RLock()
	done := r.syncDone
	status := cloneUsageAutoSyncStatus(r.status)
	r.mu.RUnlock()
	r.lifecycleMu.Unlock()

	if done == nil || !status.Syncing {
		return status
	}
	select {
	case <-ctx.Done():
	case <-done:
	}
	return r.Status()
}

func (r *usageAutoSyncRuntime) run(ctx context.Context, pause <-chan struct{}) {
	defer r.loopWG.Done()

	interval := usage.UsageSyncIntervalDefault
	r.mu.RLock()
	loadRevision := r.intervalRevision
	r.mu.RUnlock()
	settings, err := r.loadSettings(ctx)
	if err == nil {
		r.mu.Lock()
		// A settings update can complete while the startup read is in flight. Do
		// not let that stale read overwrite the newer persisted/runtime interval.
		if r.intervalRevision == loadRevision {
			r.status.IntervalSeconds = settings.UsageSyncIntervalSeconds
		}
		interval = r.status.IntervalSeconds
		r.mu.Unlock()
	} else if ctx.Err() == nil {
		r.completeWithError(err)
	}
	if ctx.Err() != nil {
		return
	}

	ticker := r.newTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	r.startSync(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pause:
			return
		case <-r.intervalUpdates:
			interval = r.Status().IntervalSeconds
			ticker.Reset(time.Duration(interval) * time.Second)
			r.emitStatus()
		case <-ticker.C():
			// startSync is a non-blocking compare-and-start operation. A tick that
			// arrives during a long scan is intentionally skipped rather than queued.
			r.startSync(ctx)
		}
	}
}

func (r *usageAutoSyncRuntime) startSync(parent context.Context) bool {
	r.mu.Lock()
	if r.status.Syncing || parent.Err() != nil {
		r.mu.Unlock()
		return false
	}
	r.status.Syncing = true
	r.status.Outcome = UsageAutoSyncOutcomeSyncing
	r.status.Error = nil
	r.status.LastStartedAtUnixMS = r.now().UnixMilli()
	r.status.Revision++
	r.syncDone = make(chan struct{})
	r.mu.Unlock()
	r.emitStatus()

	r.workerWG.Add(1)
	go func() {
		defer r.workerWG.Done()
		ctx, cancel := context.WithTimeout(parent, r.timeout)
		defer cancel()
		result, err := r.syncProvider(ctx)
		if parent.Err() != nil {
			r.mu.Lock()
			r.status.Syncing = false
			r.status.Outcome = UsageAutoSyncOutcomeIdle
			r.status.Error = nil
			r.status.Revision++
			r.finishSyncLocked()
			r.mu.Unlock()
			return
		}
		if err != nil {
			r.completeWithError(err)
			return
		}
		r.completeWithResult(result)
	}()
	return true
}

func (r *usageAutoSyncRuntime) completeWithResult(result usage.UsageSyncResult) {
	completedAt := r.now().UnixMilli()
	outcome := UsageAutoSyncOutcomeSuccess
	if len(result.Errors) > 0 {
		outcome = UsageAutoSyncOutcomeWarning
	}
	r.mu.Lock()
	r.status.Syncing = false
	r.status.Outcome = outcome
	r.status.LastCompletedAtUnixMS = completedAt
	r.status.LastSuccessAtUnixMS = completedAt
	r.status.ImportErrorCount = int64(len(result.Errors))
	r.status.Error = nil
	r.status.Revision++
	r.finishSyncLocked()
	r.mu.Unlock()
	r.emitStatus()
}

func (r *usageAutoSyncRuntime) completeWithError(err error) {
	completedAt := r.now().UnixMilli()
	r.mu.Lock()
	r.status.Syncing = false
	r.status.Outcome = UsageAutoSyncOutcomeError
	r.status.LastCompletedAtUnixMS = completedAt
	r.status.ImportErrorCount = 0
	r.status.Error = formatUsageAutoSyncError(err)
	r.status.Revision++
	r.finishSyncLocked()
	r.mu.Unlock()
	r.emitStatus()
}

func (r *usageAutoSyncRuntime) finishSyncLocked() {
	if r.syncDone == nil {
		return
	}
	close(r.syncDone)
	r.syncDone = nil
}

func (r *usageAutoSyncRuntime) emitStatus() {
	r.mu.RLock()
	emitter := r.emitter
	status := cloneUsageAutoSyncStatus(r.status)
	r.mu.RUnlock()
	if emitter != nil {
		emitter(status)
	}
}

func cloneUsageAutoSyncStatus(status UsageAutoSyncStatus) UsageAutoSyncStatus {
	if status.Error != nil {
		copied := *status.Error
		status.Error = &copied
	}
	return status
}

func formatUsageAutoSyncError(err error) *UsageAutoSyncError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &UsageAutoSyncError{Code: "TIMEOUT", Message: "usage sync timed out"}
	}
	formatted := FormatDesktopError(err)
	return &UsageAutoSyncError{Code: formatted.Code, Message: formatted.Message}
}
