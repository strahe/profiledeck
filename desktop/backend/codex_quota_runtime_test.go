package backend

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/app"
)

func TestCodexQuotaRuntimeMergesSharedCredentialSettings(t *testing.T) {
	now := time.Unix(1780000000, 0)
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.now = func() time.Time { return now }
	runtime.random = rand.New(rand.NewSource(1))
	runtime.applyTargets([]app.CodexAutomationTarget{
		{ProfileID: "slow", CredentialID: "credential-1", CredentialSHA256: "hash", QuotaRefreshIntervalSeconds: 600, QuotaSupported: true},
		{ProfileID: "fast", CredentialID: "credential-1", CredentialSHA256: "hash", QuotaRefreshIntervalSeconds: 300, AuthKeepaliveEnabled: true, QuotaSupported: true, AuthKeepaliveSupported: true},
	})

	runtime.mu.RLock()
	schedule := runtime.schedules["credential-1"]
	runtime.mu.RUnlock()
	if schedule == nil || schedule.interval != 5*time.Minute || !schedule.keepalive {
		t.Fatalf("expected shortest interval and OR keepalive, got %#v", schedule)
	}
	if schedule.nextKind != app.CodexCredentialJobQuota {
		t.Fatalf("expected quota refresh to subsume keepalive, got %q", schedule.nextKind)
	}
	if schedule.nextRunAt.Before(now) || schedule.nextRunAt.After(now.Add(5*time.Minute)) {
		t.Fatalf("expected first run spread across full interval, got %s", schedule.nextRunAt)
	}
	if len(schedule.profileIDs) != 2 || schedule.profileIDs[0] != "fast" || schedule.profileIDs[1] != "slow" {
		t.Fatalf("expected deterministic shared projection, got %#v", schedule.profileIDs)
	}
}

func TestCodexQuotaRuntimeStatusDoesNotExposeCredentialInternals(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{CodexDir: "/private/runtime-path"})
	runtime.applyTargets([]app.CodexAutomationTarget{{
		ProfileID: "work", CredentialID: "credential-secret", CredentialSHA256: "payload-hash-secret",
		QuotaRefreshIntervalSeconds: 300, QuotaSupported: true,
	}})
	raw, err := json.Marshal(runtime.Status())
	if err != nil {
		t.Fatalf("expected runtime status JSON, got %v", err)
	}
	for _, secret := range []string{"credential-secret", "payload-hash-secret", "/private/runtime-path"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("runtime status exposed internal value %q: %s", secret, raw)
		}
	}
}

func TestCodexQuotaRuntimeKeepsPermanentFailurePausedUntilCredentialChanges(t *testing.T) {
	now := time.Unix(1780000000, 0)
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.now = func() time.Time { return now }
	target := app.CodexAutomationTarget{
		ProfileID: "work", CredentialID: "credential-1", CredentialSHA256: "hash-1",
		QuotaRefreshIntervalSeconds: 300, QuotaSupported: true,
	}
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	runtime.mu.Lock()
	runtime.schedules["credential-1"].pausedHash = "hash-1"
	runtime.schedules["credential-1"].nextRunAt = time.Time{}
	runtime.mu.Unlock()
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	runtime.mu.RLock()
	paused := runtime.schedules["credential-1"]
	runtime.mu.RUnlock()
	if paused.pausedHash != "hash-1" || !paused.nextRunAt.IsZero() {
		t.Fatalf("expected unchanged credential to remain paused, got %#v", paused)
	}

	target.CredentialSHA256 = "hash-2"
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	runtime.mu.RLock()
	resumed := runtime.schedules["credential-1"]
	runtime.mu.RUnlock()
	if resumed.pausedHash != "" || resumed.nextRunAt.IsZero() {
		t.Fatalf("expected changed credential to resume scheduling, got %#v", resumed)
	}
}

func TestCodexQuotaRuntimeInvalidatesSnapshotOnUnrelatedCredentialChange(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	target := app.CodexAutomationTarget{
		ProfileID: "work", CredentialID: "credential", CredentialSHA256: "hash-1", QuotaSupported: true,
	}
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	runtime.mu.Lock()
	runtime.profileStatus["work"] = CodexProfileQuotaRuntimeStatus{
		ProfileID: "work", LastCompletedAtUnixMS: 1780000000000, Status: app.CodexProfileQuotaAvailable,
		Snapshot: &app.CodexQuotaSnapshot{FetchedAtUnixMS: 1780000000000},
	}
	runtime.rebuildStatusLocked()
	runtime.mu.Unlock()
	target.CredentialSHA256 = "hash-2"
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	status := runtime.Status().Profiles[0]
	if status.Snapshot != nil || status.LastCompletedAtUnixMS != 0 || status.Status != "" {
		t.Fatalf("expected unrelated credential content to clear quota state, got %#v", status)
	}
}

func TestCodexQuotaRuntimePreservesSnapshotAcrossNativeTokenRotation(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	target := app.CodexAutomationTarget{
		ProfileID: "work", CredentialID: "credential", CredentialSHA256: "hash-1", QuotaSupported: true,
	}
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	snapshot := &app.CodexQuotaSnapshot{FetchedAtUnixMS: 1780000000000}
	runtime.completeJob(
		&codexQuotaRuntimeJob{key: "credential", profileID: "work", kind: app.CodexCredentialJobQuota},
		app.CodexCredentialJobResult{
			Quota:             app.CodexProfileQuota{Status: app.CodexProfileQuotaAvailable, Snapshot: snapshot},
			CredentialUpdated: true, CredentialSHA256: "hash-2", NativeAttempted: true,
		},
		nil,
		time.Unix(1780000000, 0),
	)
	target.CredentialSHA256 = "hash-2"
	runtime.applyTargets([]app.CodexAutomationTarget{target})
	status := runtime.Status().Profiles[0]
	if status.Snapshot == nil || status.Snapshot.FetchedAtUnixMS != snapshot.FetchedAtUnixMS {
		t.Fatalf("expected snapshot from rotating native request to remain current, got %#v", status)
	}
}

func TestCodexQuotaRuntimeUsesJitterAndBoundedBackoff(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.random = rand.New(rand.NewSource(2))
	for i := 0; i < 100; i++ {
		got := runtime.randomJitterLocked(10 * time.Minute)
		if got < 9*time.Minute || got > 11*time.Minute {
			t.Fatalf("expected +/-10%% jitter, got %s", got)
		}
	}
	schedule := &codexCredentialSchedule{nextKind: app.CodexCredentialJobQuota}
	now := time.Unix(1780000000, 0)
	for i, expected := range []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour} {
		runtime.scheduleRetryLocked(schedule, now)
		if got := schedule.nextRunAt.Sub(now); got != expected {
			t.Fatalf("retry %d: expected %s, got %s", i, expected, got)
		}
	}
}

func TestCodexQuotaRuntimePrioritizesManualJobs(t *testing.T) {
	now := time.Unix(1780000000, 0)
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.now = func() time.Time { return now }
	runtime.schedules = map[string]*codexCredentialSchedule{
		"auto":   {key: "auto", profileIDs: []string{"auto-profile"}, nextKind: app.CodexCredentialJobQuota, nextRunAt: now},
		"manual": {key: "manual", profileIDs: []string{"manual-profile"}},
	}
	runtime.profileStatus = map[string]CodexProfileQuotaRuntimeStatus{
		"auto-profile": {ProfileID: "auto-profile"}, "manual-profile": {ProfileID: "manual-profile"},
	}
	runtime.manualByKey["manual"] = &codexQuotaManualGroup{
		key: "manual", waiters: []codexQuotaWaiter{{profileID: "manual-profile", result: make(chan codexQuotaManualResult, 1)}},
	}
	runtime.manualOrder = []string{"manual"}
	job, wait := runtime.nextJob()
	if wait != 0 || job == nil || !job.manual || job.key != "manual" {
		t.Fatalf("expected queued manual job before due automatic work, job=%#v wait=%s", job, wait)
	}
}

func TestCodexQuotaRuntimeRefreshesCrossCredentialGapAfterEveryCompletion(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.random = rand.New(rand.NewSource(3))
	runtime.schedules = map[string]*codexCredentialSchedule{
		"credential-a": {key: "credential-a", profileIDs: []string{"profile-a"}},
	}
	runtime.profileStatus = map[string]CodexProfileQuotaRuntimeStatus{
		"profile-a": {ProfileID: "profile-a"},
	}
	runtime.lastCredentialKey = "credential-a"
	runtime.nextCredentialAt = time.Unix(1780000000, 0)
	completedAt := time.Unix(1780000100, 0)
	runtime.completeJob(
		&codexQuotaRuntimeJob{key: "credential-a", profileID: "profile-a", kind: app.CodexCredentialJobQuota},
		app.CodexCredentialJobResult{Quota: app.CodexProfileQuota{Status: app.CodexProfileQuotaAvailable}},
		nil,
		completedAt,
	)
	runtime.mu.RLock()
	nextCredentialAt := runtime.nextCredentialAt
	waitForOther := runtime.credentialGapLocked("credential-b", completedAt)
	waitForSame := runtime.credentialGapLocked("credential-a", completedAt)
	runtime.mu.RUnlock()
	if !nextCredentialAt.After(completedAt) || waitForOther < 2*time.Second || waitForOther > 5*time.Second {
		t.Fatalf("expected a fresh 2-5 second cross-credential gap, next=%s wait=%s", nextCredentialAt, waitForOther)
	}
	if waitForSame != 0 {
		t.Fatalf("expected same credential to bypass the gap, got %s", waitForSame)
	}
}

func TestCodexQuotaRuntimeChangesAppServerStatusOnlyAfterNativeAttempt(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.status.AppServerStatus = CodexAppServerUnavailable
	runtime.schedules = map[string]*codexCredentialSchedule{
		"credential": {key: "credential", profileIDs: []string{"profile"}},
	}
	runtime.profileStatus = map[string]CodexProfileQuotaRuntimeStatus{
		"profile": {ProfileID: "profile"},
	}
	job := &codexQuotaRuntimeJob{key: "credential", profileID: "profile", kind: app.CodexCredentialJobQuota}
	runtime.completeJob(job, app.CodexCredentialJobResult{
		Quota: app.CodexProfileQuota{Status: app.CodexProfileQuotaUnsupported},
	}, nil, time.Unix(1780000000, 0))
	if got := runtime.Status().AppServerStatus; got != CodexAppServerUnavailable {
		t.Fatalf("expected unsupported auth not to claim app-server availability, got %q", got)
	}

	runtime.completeJob(job, app.CodexCredentialJobResult{
		Quota: app.CodexProfileQuota{Status: app.CodexProfileQuotaAvailable}, NativeAttempted: true,
	}, nil, time.Unix(1780000010, 0))
	if got := runtime.Status().AppServerStatus; got != CodexAppServerAvailable {
		t.Fatalf("expected successful native attempt to mark app-server available, got %q", got)
	}
}

func TestCodexQuotaRuntimeManualFailureDoesNotEnableAutomaticRetry(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.schedules = map[string]*codexCredentialSchedule{
		"credential": {key: "credential", profileIDs: []string{"profile"}},
	}
	runtime.profileStatus = map[string]CodexProfileQuotaRuntimeStatus{
		"profile": {ProfileID: "profile"},
	}
	runtime.completeJob(
		&codexQuotaRuntimeJob{key: "credential", profileID: "profile", kind: app.CodexCredentialJobQuota, manual: true},
		app.CodexCredentialJobResult{},
		errors.New("manual request failed"),
		time.Unix(1780000000, 0),
	)
	runtime.mu.RLock()
	schedule := runtime.schedules["credential"]
	runtime.mu.RUnlock()
	if schedule.nextKind != "" || !schedule.nextRunAt.IsZero() {
		t.Fatalf("expected manual failure to leave automation disabled, got %#v", schedule)
	}
}

func TestCodexQuotaRuntimeDoesNotRestoreRemovedProfileAfterJobCompletion(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.schedules = map[string]*codexCredentialSchedule{}
	runtime.profileToKey = map[string]string{}
	runtime.profileStatus = map[string]CodexProfileQuotaRuntimeStatus{}
	runtime.completeJob(
		&codexQuotaRuntimeJob{key: "removed", profileID: "removed", kind: app.CodexCredentialJobQuota},
		app.CodexCredentialJobResult{
			Quota: app.CodexProfileQuota{
				Status:   app.CodexProfileQuotaAvailable,
				Snapshot: &app.CodexQuotaSnapshot{FetchedAtUnixMS: 1780000000000},
			},
		},
		nil,
		time.Unix(1780000000, 0),
	)
	if profiles := runtime.Status().Profiles; len(profiles) != 0 {
		t.Fatalf("expected removed Profile to stay absent, got %#v", profiles)
	}
}

func TestCodexQuotaRuntimeMergesManualRequestsForSharedCredentialAndRunsSerially(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	targets := []app.CodexAutomationTarget{
		{ProfileID: "one", CredentialID: "shared", CredentialSHA256: "hash", QuotaSupported: true},
		{ProfileID: "two", CredentialID: "shared", CredentialSHA256: "hash", QuotaSupported: true},
	}
	runtime.loadTargets = func(context.Context) ([]app.CodexAutomationTarget, error) { return targets, nil }
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	runtime.runJob = func(ctx context.Context, req app.RunCodexCredentialJobRequest) (app.CodexCredentialJobResult, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-ctx.Done():
			active.Add(-1)
			return app.CodexCredentialJobResult{}, ctx.Err()
		case <-release:
		}
		active.Add(-1)
		return app.CodexCredentialJobResult{Quota: app.CodexProfileQuota{
			ProfileID: req.ProfileID, CredentialID: "shared", Status: app.CodexProfileQuotaAvailable,
			Snapshot: &app.CodexQuotaSnapshot{FetchedAtUnixMS: time.Now().UnixMilli(), AdditionalRateLimits: []app.CodexQuotaRateLimit{}},
		}}, nil
	}
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)

	type response struct {
		quota app.CodexProfileQuota
		err   error
	}
	responses := make(chan response, 2)
	go func() {
		quota, err := runtime.ReadProfileQuota(context.Background(), "one")
		responses <- response{quota: quota, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected first manual quota request")
	}
	go func() {
		quota, err := runtime.ReadProfileQuota(context.Background(), "two")
		responses <- response{quota: quota, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 1 || maxActive.Load() != 1 {
		t.Fatalf("expected shared requests to merge into one serial job, calls=%d max=%d", calls.Load(), maxActive.Load())
	}
	close(release)
	seen := map[string]bool{}
	for range 2 {
		select {
		case result := <-responses:
			if result.err != nil {
				t.Fatalf("unexpected manual request error: %v", result.err)
			}
			seen[result.quota.ProfileID] = true
		case <-time.After(time.Second):
			t.Fatal("expected merged manual result")
		}
	}
	if !seen["one"] || !seen["two"] {
		t.Fatalf("expected result projection to both Profiles, got %#v", seen)
	}
}

func TestCodexQuotaRuntimeDiscardsQuotaFromStaleCredentialCopy(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.loadTargets = func(context.Context) ([]app.CodexAutomationTarget, error) {
		return []app.CodexAutomationTarget{{
			ProfileID: "work", CredentialID: "credential", CredentialSHA256: "hash", QuotaSupported: true,
		}}, nil
	}
	runtime.runJob = func(context.Context, app.RunCodexCredentialJobRequest) (app.CodexCredentialJobResult, error) {
		return app.CodexCredentialJobResult{
			Quota: app.CodexProfileQuota{
				ProfileID: "work", CredentialID: "credential", Status: app.CodexProfileQuotaAvailable,
				Snapshot: &app.CodexQuotaSnapshot{FetchedAtUnixMS: 1780000000000},
			},
			CredentialConflict: true,
		}, nil
	}
	runtime.Start(context.Background(), nil)
	t.Cleanup(runtime.Stop)
	quota, err := runtime.ReadProfileQuota(context.Background(), "work")
	var appErr *app.AppError
	if !errors.As(err, &appErr) || appErr.Code != app.ErrorCodexInvalid {
		t.Fatalf("expected stale credential error, got %v", err)
	}
	if quota.Status != app.CodexProfileQuotaUnavailable || quota.Snapshot != nil {
		t.Fatalf("expected stale quota snapshot to be discarded, got %#v", quota)
	}
}

func TestCodexQuotaRuntimeStopCancelsRunningJob(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.loadTargets = func(context.Context) ([]app.CodexAutomationTarget, error) {
		return []app.CodexAutomationTarget{{ProfileID: "work", CredentialID: "credential", CredentialSHA256: "hash", QuotaSupported: true}}, nil
	}
	started := make(chan struct{})
	var once sync.Once
	runtime.runJob = func(ctx context.Context, _ app.RunCodexCredentialJobRequest) (app.CodexCredentialJobResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return app.CodexCredentialJobResult{}, ctx.Err()
	}
	runtime.Start(context.Background(), nil)
	result := make(chan error, 1)
	go func() {
		_, err := runtime.ReadProfileQuota(context.Background(), "work")
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected running quota job")
	}
	runtime.Stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled manual request, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected canceled waiter")
	}
}

func TestCodexQuotaRuntimeStopDoesNotRestartCanceledOverdueAutomaticJob(t *testing.T) {
	runtime := newCodexQuotaRuntime(Environment{})
	runtime.loadTargets = func(context.Context) ([]app.CodexAutomationTarget, error) {
		return []app.CodexAutomationTarget{{
			ProfileID: "work", CredentialID: "credential", CredentialSHA256: "hash",
			QuotaRefreshIntervalSeconds: 300, QuotaSupported: true,
		}}, nil
	}
	started := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	runtime.runJob = func(ctx context.Context, _ app.RunCodexCredentialJobRequest) (app.CodexCredentialJobResult, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-ctx.Done()
		return app.CodexCredentialJobResult{}, ctx.Err()
	}
	runtime.Start(context.Background(), nil)
	runtime.mu.Lock()
	runtime.schedules["credential"].nextRunAt = time.Now().Add(-time.Second)
	runtime.mu.Unlock()
	runtime.signalWake()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected overdue automatic quota job")
	}
	stopped := make(chan struct{})
	go func() {
		runtime.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("expected runtime shutdown to finish")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected canceled automatic work not to restart, got %d calls", calls.Load())
	}
}
