package backend

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
)

const (
	CodexQuotaStatusEventName = "profiledeck:codex-quota-status"

	CodexAppServerUnknown      = "unknown"
	CodexAppServerAvailable    = "available"
	CodexAppServerUnavailable  = "unavailable"
	CodexAppServerIncompatible = "incompatible"

	codexQuotaJobTimeout = 35 * time.Second
)

var codexQuotaRetryBackoff = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}

type CodexQuotaRuntimeStatus struct {
	AppServerStatus string                           `json:"app_server_status"`
	Profiles        []CodexProfileQuotaRuntimeStatus `json:"profiles"`
}

type CodexProfileQuotaRuntimeStatus struct {
	ProfileID             string                           `json:"profile_id"`
	ConfigSetID           string                           `json:"config_set_id,omitempty"`
	Source                codex.CodexQuotaSource           `json:"source,omitempty"`
	InsecureTransport     bool                             `json:"insecure_transport,omitempty"`
	Running               bool                             `json:"running"`
	LastTask              string                           `json:"last_task,omitempty"`
	LastStartedAtUnixMS   int64                            `json:"last_started_at_unix_ms"`
	LastCompletedAtUnixMS int64                            `json:"last_completed_at_unix_ms"`
	LastSuccessAtUnixMS   int64                            `json:"last_success_at_unix_ms"`
	NextRunAtUnixMS       int64                            `json:"next_run_at_unix_ms"`
	Status                codex.CodexProfileQuotaStatus    `json:"status"`
	Snapshot              *codex.CodexQuotaSnapshot        `json:"snapshot,omitempty"`
	Sub2APISnapshot       *codex.CodexSub2APIQuotaSnapshot `json:"sub2api_snapshot,omitempty"`
	ErrorCode             string                           `json:"error_code,omitempty"`
}

type codexCredentialSchedule struct {
	key               string
	credentialID      string
	credentialHash    string
	configSetID       string
	configSetHash     string
	quotaSource       codex.CodexQuotaSource
	insecureTransport bool
	profileIDs        []string
	interval          time.Duration
	keepalive         bool
	keepaliveDueAt    time.Time
	nextRunAt         time.Time
	nextKind          codex.CodexCredentialJobKind
	retryIndex        int
	pausedHash        string
	quotaSupported    bool
	keepaliveSupport  bool
}

type codexQuotaWaiter struct {
	profileID string
	result    chan codexQuotaManualResult
}

type codexQuotaManualResult struct {
	quota codex.CodexProfileQuota
	err   error
}

type codexQuotaManualGroup struct {
	key     string
	waiters []codexQuotaWaiter
}

type codexQuotaRuntimeJob struct {
	key            string
	profileID      string
	kind           codex.CodexCredentialJobKind
	manual         bool
	waiters        []codexQuotaWaiter
	startedAt      time.Time
	credentialID   string
	credentialHash string
	configSetHash  string
	quotaSource    codex.CodexQuotaSource
}

type codexQuotaRuntime struct {
	mu                sync.RWMutex
	status            CodexQuotaRuntimeStatus
	emitter           func(CodexQuotaRuntimeStatus)
	schedules         map[string]*codexCredentialSchedule
	profileToKey      map[string]string
	profileStatus     map[string]CodexProfileQuotaRuntimeStatus
	manualByKey       map[string]*codexQuotaManualGroup
	manualOrder       []string
	inflight          *codexQuotaRuntimeJob
	lastCredentialKey string
	nextCredentialAt  time.Time

	lifecycleMu sync.Mutex
	reloadMu    sync.Mutex
	started     bool
	cancel      context.CancelFunc
	pause       chan struct{}
	wg          sync.WaitGroup

	wake        chan struct{}
	now         func() time.Time
	random      *rand.Rand
	timeout     time.Duration
	loadTargets func(context.Context) ([]codex.CodexAutomationTarget, error)
	runJob      func(context.Context, codex.RunCodexCredentialJobRequest) (codex.CodexCredentialJobResult, error)
}

func newCodexQuotaRuntime(
	loadTargets func(context.Context) ([]codex.CodexAutomationTarget, error),
	runJob func(context.Context, codex.RunCodexCredentialJobRequest) (codex.CodexCredentialJobResult, error),
) *codexQuotaRuntime {
	appServerState := CodexAppServerUnknown
	if !codexappserver.NewRunner().Available() {
		appServerState = CodexAppServerUnavailable
	}
	runtime := &codexQuotaRuntime{
		status:        CodexQuotaRuntimeStatus{AppServerStatus: appServerState, Profiles: []CodexProfileQuotaRuntimeStatus{}},
		schedules:     map[string]*codexCredentialSchedule{},
		profileToKey:  map[string]string{},
		profileStatus: map[string]CodexProfileQuotaRuntimeStatus{},
		manualByKey:   map[string]*codexQuotaManualGroup{},
		wake:          make(chan struct{}, 1),
		now:           time.Now,
		random:        rand.New(rand.NewSource(time.Now().UnixNano())),
		timeout:       codexQuotaJobTimeout,
		loadTargets:   loadTargets,
		runJob:        runJob,
	}
	return runtime
}

func (r *codexQuotaRuntime) Start(ctx context.Context, emitter func(CodexQuotaRuntimeStatus)) {
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
	if err := r.Reload(runCtx); err != nil && runCtx.Err() == nil {
		r.recordRuntimeError(err)
	}
	r.wg.Add(1)
	r.started = true
	r.cancel = cancel
	r.pause = pause
	go r.run(runCtx, pause)
	r.lifecycleMu.Unlock()
}

func (r *codexQuotaRuntime) SetEmitter(emitter func(CodexQuotaRuntimeStatus)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.emitter = emitter
	r.mu.Unlock()
}

func (r *codexQuotaRuntime) startAgentRuntime(ctx context.Context) {
	r.mu.RLock()
	emitter := r.emitter
	r.mu.RUnlock()
	r.Start(ctx, emitter)
}

func (r *codexQuotaRuntime) Stop() {
	r.stop(false)
}

func (r *codexQuotaRuntime) stopAgentRuntime() {
	r.stop(true)
}

func (r *codexQuotaRuntime) stop(graceful bool) {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	if !r.started {
		r.lifecycleMu.Unlock()
		return
	}
	if graceful {
		close(r.pause)
	} else {
		r.cancel()
	}
	r.signalWake()
	r.wg.Wait()
	if graceful {
		r.cancel()
	}
	r.mu.Lock()
	for _, group := range r.manualByKey {
		for _, waiter := range group.waiters {
			nonBlockingQuotaResult(waiter.result, codexQuotaManualResult{err: context.Canceled})
		}
	}
	if r.inflight != nil {
		for _, waiter := range r.inflight.waiters {
			nonBlockingQuotaResult(waiter.result, codexQuotaManualResult{err: context.Canceled})
		}
	}
	r.manualByKey = map[string]*codexQuotaManualGroup{}
	r.manualOrder = nil
	r.inflight = nil
	r.started = false
	r.cancel = nil
	r.pause = nil
	r.mu.Unlock()
	r.lifecycleMu.Unlock()
}

func (r *codexQuotaRuntime) Status() CodexQuotaRuntimeStatus {
	if r == nil {
		return CodexQuotaRuntimeStatus{AppServerStatus: CodexAppServerUnavailable, Profiles: []CodexProfileQuotaRuntimeStatus{}}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneCodexQuotaRuntimeStatus(r.status)
}

func (r *codexQuotaRuntime) Reload(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	targets, err := r.loadTargets(ctx)
	if err != nil {
		return err
	}
	r.applyTargets(targets)
	r.signalWake()
	r.emitStatus()
	return nil
}

func (r *codexQuotaRuntime) ReadProfileQuota(ctx context.Context, profileID string) (codex.CodexProfileQuota, error) {
	if r == nil {
		return codex.CodexProfileQuota{}, errors.New("Codex quota runtime is unavailable")
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return codex.CodexProfileQuota{}, apperror.New(apperror.ProfileInvalid, "Codex profile id is required")
	}
	if err := r.Reload(ctx); err != nil {
		return codex.CodexProfileQuota{}, err
	}
	waiter := codexQuotaWaiter{profileID: profileID, result: make(chan codexQuotaManualResult, 1)}
	r.mu.Lock()
	key, ok := r.profileToKey[profileID]
	if !ok {
		r.mu.Unlock()
		return codex.CodexProfileQuota{}, apperror.New(apperror.ProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
	}
	schedule := r.schedules[key]
	if r.inflight != nil && r.inflight.key == key && r.inflight.kind == codex.CodexCredentialJobQuota &&
		codexQuotaJobMatchesSchedule(r.inflight, schedule) {
		r.inflight.waiters = append(r.inflight.waiters, waiter)
	} else if group, exists := r.manualByKey[key]; exists {
		group.waiters = append(group.waiters, waiter)
	} else {
		r.manualByKey[key] = &codexQuotaManualGroup{key: key, waiters: []codexQuotaWaiter{waiter}}
		r.manualOrder = append(r.manualOrder, key)
	}
	r.mu.Unlock()
	r.signalWake()
	select {
	case <-ctx.Done():
		return codex.CodexProfileQuota{}, ctx.Err()
	case result := <-waiter.result:
		return result.quota, result.err
	}
}

func (r *codexQuotaRuntime) applyTargets(targets []codex.CodexAutomationTarget) {
	now := r.now()
	grouped := map[string]*codexCredentialSchedule{}
	profileToKey := make(map[string]string, len(targets))
	for _, target := range targets {
		key := codexQuotaScheduleKey(target)
		if key == "" {
			key = "profile:" + target.ProfileID
		}
		profileToKey[target.ProfileID] = key
		schedule, ok := grouped[key]
		if !ok {
			schedule = &codexCredentialSchedule{
				key: key, credentialID: target.CredentialID, credentialHash: target.CredentialSHA256,
				configSetID: target.ConfigSetID, configSetHash: target.ConfigSetSHA256, quotaSource: target.QuotaSource,
				insecureTransport: target.InsecureTransport,
				keepaliveDueAt:    timeFromUnixMilli(target.AuthRefreshDueAtUnixMS),
				quotaSupported:    target.QuotaSupported,
				keepaliveSupport:  target.AuthKeepaliveSupported,
			}
			grouped[key] = schedule
		}
		schedule.profileIDs = append(schedule.profileIDs, target.ProfileID)
		if target.QuotaSupported && target.QuotaRefreshIntervalSeconds > 0 {
			interval := time.Duration(target.QuotaRefreshIntervalSeconds) * time.Second
			if schedule.interval == 0 || interval < schedule.interval {
				schedule.interval = interval
			}
		}
		schedule.keepalive = schedule.keepalive || target.AuthKeepaliveEnabled
		if dueAt := timeFromUnixMilli(target.AuthRefreshDueAtUnixMS); !dueAt.IsZero() && (schedule.keepaliveDueAt.IsZero() || dueAt.Before(schedule.keepaliveDueAt)) {
			schedule.keepaliveDueAt = dueAt
		}
		schedule.quotaSupported = schedule.quotaSupported || target.QuotaSupported
		schedule.keepaliveSupport = schedule.keepaliveSupport || target.AuthKeepaliveSupported
	}

	r.mu.Lock()
	for key, schedule := range grouped {
		sort.Strings(schedule.profileIDs)
		old := r.schedules[key]
		sameBinding := codexQuotaSchedulesMatch(old, schedule)
		if sameBinding {
			schedule.retryIndex = old.retryIndex
			schedule.pausedHash = old.pausedHash
			schedule.nextRunAt = old.nextRunAt
			schedule.nextKind = old.nextKind
		}
		if schedule.pausedHash != "" && schedule.pausedHash != schedule.credentialHash {
			schedule.pausedHash = ""
			schedule.retryIndex = 0
			schedule.nextRunAt = time.Time{}
		}
		if schedule.pausedHash == "" {
			r.ensureScheduleLocked(schedule, old, now)
		}
	}
	for profileID := range r.profileStatus {
		if _, exists := profileToKey[profileID]; !exists {
			delete(r.profileStatus, profileID)
		}
	}
	for _, target := range targets {
		newKey := profileToKey[target.ProfileID]
		oldKey := r.profileToKey[target.ProfileID]
		oldSchedule := r.schedules[oldKey]
		newSchedule := grouped[newKey]
		bindingChanged := oldSchedule != nil && newSchedule != nil && !codexQuotaSchedulesMatch(oldSchedule, newSchedule)
		status, exists := r.profileStatus[target.ProfileID]
		if (oldKey != "" && oldKey != newKey) || bindingChanged {
			status = CodexProfileQuotaRuntimeStatus{}
			exists = false
		}
		if !exists {
			status = CodexProfileQuotaRuntimeStatus{ProfileID: target.ProfileID}
		}
		status.NextRunAtUnixMS = unixMilliOrZero(grouped[newKey].nextRunAt)
		r.profileStatus[target.ProfileID] = status
	}
	r.schedules = grouped
	r.profileToKey = profileToKey
	r.rebuildStatusLocked()
	r.mu.Unlock()
}

func (r *codexQuotaRuntime) ensureScheduleLocked(schedule, old *codexCredentialSchedule, now time.Time) {
	if schedule.interval > 0 && schedule.quotaSupported {
		schedule.nextKind = codex.CodexCredentialJobQuota
		if schedule.nextRunAt.IsZero() || old == nil || old.interval != schedule.interval || old.nextKind != codex.CodexCredentialJobQuota {
			schedule.nextRunAt = now.Add(r.randomInitialDelayLocked(schedule.interval))
		}
		return
	}
	if schedule.keepalive && schedule.keepaliveSupport {
		schedule.nextKind = codex.CodexCredentialJobKeepalive
		if schedule.keepaliveDueAt.IsZero() || schedule.keepaliveDueAt.Before(now) {
			schedule.nextRunAt = now
		} else {
			schedule.nextRunAt = schedule.keepaliveDueAt
		}
		return
	}
	schedule.nextKind = ""
	schedule.nextRunAt = time.Time{}
}

func (r *codexQuotaRuntime) run(ctx context.Context, pause <-chan struct{}) {
	defer r.wg.Done()
	for {
		// A canceled automatic job can still have an overdue nextRunAt. Check
		// lifecycle cancellation before selecting work so shutdown cannot spin by
		// repeatedly starting that same job with an already-canceled context.
		select {
		case <-ctx.Done():
			return
		case <-pause:
			return
		default:
		}
		job, wait := r.nextJob()
		if job != nil {
			r.executeJob(ctx, job)
			continue
		}
		if wait <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-pause:
				return
			case <-r.wake:
			}
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-pause:
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-r.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (r *codexQuotaRuntime) nextJob() (*codexQuotaRuntimeJob, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight != nil {
		return nil, 0
	}
	now := r.now()
	if len(r.manualOrder) > 0 {
		key := r.manualOrder[0]
		if wait := r.credentialGapLocked(key, now); wait > 0 {
			return nil, wait
		}
		r.manualOrder = r.manualOrder[1:]
		group := r.manualByKey[key]
		delete(r.manualByKey, key)
		schedule := r.schedules[key]
		if group == nil || schedule == nil || len(schedule.profileIDs) == 0 {
			if group != nil {
				err := apperror.New(apperror.CodexInvalid, "Codex profile changed while quota refresh was queued")
				for _, waiter := range group.waiters {
					nonBlockingQuotaResult(waiter.result, codexQuotaManualResult{err: err})
				}
			}
			return nil, 0
		}
		job := &codexQuotaRuntimeJob{
			key: key, profileID: group.waiters[0].profileID, kind: codex.CodexCredentialJobQuota,
			manual: true, waiters: group.waiters, startedAt: now,
		}
		r.startJobLocked(job, schedule)
		return job, 0
	}

	var selected *codexCredentialSchedule
	for _, schedule := range r.schedules {
		if schedule.nextRunAt.IsZero() || (schedule.pausedHash != "" && schedule.pausedHash == schedule.credentialHash) {
			continue
		}
		if selected == nil || schedule.nextRunAt.Before(selected.nextRunAt) || (schedule.nextRunAt.Equal(selected.nextRunAt) && schedule.key < selected.key) {
			selected = schedule
		}
	}
	if selected == nil {
		return nil, 0
	}
	if selected.nextRunAt.After(now) {
		return nil, selected.nextRunAt.Sub(now)
	}
	if wait := r.credentialGapLocked(selected.key, now); wait > 0 {
		return nil, wait
	}
	job := &codexQuotaRuntimeJob{
		key: selected.key, profileID: selected.profileIDs[0], kind: selected.nextKind, startedAt: now,
	}
	r.startJobLocked(job, selected)
	return job, 0
}

func (r *codexQuotaRuntime) credentialGapLocked(key string, now time.Time) time.Duration {
	spacingKey := key
	if schedule := r.schedules[key]; schedule != nil && schedule.credentialID != "" {
		spacingKey = schedule.credentialID
	}
	if r.lastCredentialKey == "" || r.lastCredentialKey == spacingKey || !r.nextCredentialAt.After(now) {
		return 0
	}
	return r.nextCredentialAt.Sub(now)
}

func (r *codexQuotaRuntime) startJobLocked(job *codexQuotaRuntimeJob, schedule *codexCredentialSchedule) {
	job.credentialID = schedule.credentialID
	job.credentialHash = schedule.credentialHash
	job.configSetHash = schedule.configSetHash
	job.quotaSource = schedule.quotaSource
	r.inflight = job
	for _, profileID := range schedule.profileIDs {
		status := r.profileStatus[profileID]
		status.ProfileID = profileID
		status.Running = true
		status.LastTask = string(job.kind)
		status.LastStartedAtUnixMS = job.startedAt.UnixMilli()
		status.ErrorCode = ""
		r.profileStatus[profileID] = status
	}
	r.rebuildStatusLocked()
	go r.emitStatus()
}

func (r *codexQuotaRuntime) executeJob(parent context.Context, job *codexQuotaRuntimeJob) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	result, err := r.runJob(ctx, codex.RunCodexCredentialJobRequest{
		ProfileID: job.profileID,
		Kind:      job.kind, AllowDirectFallback: job.manual,
	})
	cancel()
	if err == nil && result.CredentialConflict {
		// The native response belongs to the stale temporary credential copy.
		// The concurrent database value wins, so its quota must be re-read rather
		// than projected onto Profiles bound to the replacement credential.
		result.Quota.Status = codex.CodexProfileQuotaUnavailable
		result.Quota.Snapshot = nil
		err = apperror.New(apperror.CodexInvalid, "Codex credential changed during quota refresh")
	}
	completedAt := r.now()
	credentialUpdated := result.CredentialUpdated || result.CredentialConflict
	r.completeJob(job, result, err, completedAt)
	if credentialUpdated && parent.Err() == nil {
		_ = r.Reload(parent)
	}
}

func (r *codexQuotaRuntime) completeJob(job *codexQuotaRuntimeJob, result codex.CodexCredentialJobResult, jobErr error, completedAt time.Time) {
	r.mu.Lock()
	schedule := r.schedules[job.key]
	scheduleExists := schedule != nil
	bindingStale := job.credentialHash != "" && (!scheduleExists || !codexQuotaJobMatchesSchedule(job, schedule))
	if schedule == nil {
		schedule = &codexCredentialSchedule{key: job.key, profileIDs: []string{job.profileID}}
	}
	if bindingStale {
		waiters := append([]codexQuotaWaiter(nil), job.waiters...)
		r.lastCredentialKey = job.credentialID
		if r.lastCredentialKey == "" {
			r.lastCredentialKey = job.key
		}
		r.nextCredentialAt = completedAt.Add(r.randomCredentialGapLocked())
		r.inflight = nil
		r.mu.Unlock()
		staleErr := apperror.New(apperror.CodexInvalid, "Codex Profile changed during quota refresh")
		for _, waiter := range waiters {
			nonBlockingQuotaResult(waiter.result, codexQuotaManualResult{
				quota: codex.CodexProfileQuota{ProfileID: waiter.profileID, Status: codex.CodexProfileQuotaUnavailable},
				err:   staleErr,
			})
		}
		r.signalWake()
		return
	}
	if result.Quota.ConfigSetID == "" {
		result.Quota.ConfigSetID = schedule.configSetID
	}
	if result.Quota.Source == "" {
		result.Quota.Source = schedule.quotaSource
	}
	if schedule.insecureTransport {
		result.Quota.InsecureTransport = true
	}
	if result.CredentialUpdated && result.CredentialSHA256 != "" {
		// The snapshot was read in the same native job as this token rotation.
		// Advance the runtime hash before Reload so the fresh snapshot survives;
		// unrelated credential replacements still invalidate prior results.
		schedule.credentialHash = result.CredentialSHA256
	}
	if result.NativeAttempted {
		if jobErr == nil && result.NativeErrorKind == "" {
			r.status.AppServerStatus = CodexAppServerAvailable
		} else if result.NativeErrorKind != "" {
			switch result.NativeErrorKind {
			case codexappserver.ErrorUnavailable:
				r.status.AppServerStatus = CodexAppServerUnavailable
			case codexappserver.ErrorIncompatible:
				r.status.AppServerStatus = CodexAppServerIncompatible
			default:
				r.status.AppServerStatus = CodexAppServerAvailable
			}
		}
	}
	statusCode := ""
	if jobErr != nil {
		if errors.Is(jobErr, context.DeadlineExceeded) {
			statusCode = "TIMEOUT"
		} else if errors.Is(jobErr, context.Canceled) {
			statusCode = "CANCELED"
		} else {
			statusCode = FormatDesktopError(jobErr).Code
		}
	}
	if result.NativeErrorKind == codexappserver.ErrorAuthPermanent {
		statusCode = "AUTH_PERMANENT"
		schedule.pausedHash = schedule.credentialHash
		schedule.nextRunAt = time.Time{}
	}
	if jobErr == nil && result.NativeErrorKind != codexappserver.ErrorAuthPermanent {
		if result.Quota.Status == codex.CodexProfileQuotaAvailable {
			schedule.retryIndex = 0
			r.scheduleNextSuccessLocked(schedule, job.kind, completedAt)
		} else if !job.manual && (result.Quota.Status == codex.CodexProfileQuotaUnavailable || result.Quota.Status == codex.CodexProfileQuotaAuthRequired) {
			r.scheduleRetryLocked(schedule, completedAt)
		} else if job.manual && schedule.quotaSupported && schedule.interval > 0 {
			schedule.nextKind = codex.CodexCredentialJobQuota
			schedule.nextRunAt = completedAt.Add(r.randomJitterLocked(schedule.interval))
		}
	} else if jobErr != nil && !job.manual && schedule.quotaSupported && !errors.Is(jobErr, context.Canceled) {
		r.scheduleRetryLocked(schedule, completedAt)
	}
	if scheduleExists {
		for _, profileID := range schedule.profileIDs {
			status := r.profileStatus[profileID]
			status.ProfileID = profileID
			status.Running = false
			status.LastTask = string(job.kind)
			status.LastCompletedAtUnixMS = completedAt.UnixMilli()
			status.NextRunAtUnixMS = unixMilliOrZero(schedule.nextRunAt)
			status.ErrorCode = statusCode
			status.ConfigSetID = result.Quota.ConfigSetID
			status.Source = result.Quota.Source
			status.InsecureTransport = result.Quota.InsecureTransport
			if jobErr == nil {
				status.Status = result.Quota.Status
				if job.kind == codex.CodexCredentialJobQuota {
					status.Snapshot = cloneCodexQuotaSnapshot(result.Quota.Snapshot)
					status.Sub2APISnapshot = cloneCodexSub2APIQuotaSnapshot(result.Quota.Sub2APISnapshot)
				}
				if result.Quota.Status == codex.CodexProfileQuotaAvailable {
					status.LastSuccessAtUnixMS = completedAt.UnixMilli()
				}
			} else {
				status.Status = codex.CodexProfileQuotaUnavailable
				if job.kind == codex.CodexCredentialJobQuota {
					status.Snapshot = nil
					status.Sub2APISnapshot = nil
				}
			}
			r.profileStatus[profileID] = status
		}
	}
	// Same-credential work may continue immediately, but every completion must
	// establish a fresh gap before a different credential can run.
	r.lastCredentialKey = schedule.credentialID
	if r.lastCredentialKey == "" {
		r.lastCredentialKey = job.key
	}
	r.nextCredentialAt = completedAt.Add(r.randomCredentialGapLocked())
	waiters := append([]codexQuotaWaiter(nil), job.waiters...)
	r.inflight = nil
	r.rebuildStatusLocked()
	r.mu.Unlock()
	r.emitStatus()
	for _, waiter := range waiters {
		quota := result.Quota
		quota.ProfileID = waiter.profileID
		nonBlockingQuotaResult(waiter.result, codexQuotaManualResult{quota: quota, err: jobErr})
	}
	r.signalWake()
}

func (r *codexQuotaRuntime) scheduleNextSuccessLocked(schedule *codexCredentialSchedule, kind codex.CodexCredentialJobKind, now time.Time) {
	if schedule.interval > 0 && schedule.quotaSupported {
		schedule.nextKind = codex.CodexCredentialJobQuota
		schedule.nextRunAt = now.Add(r.randomJitterLocked(schedule.interval))
		return
	}
	if schedule.keepalive && schedule.keepaliveSupport {
		schedule.nextKind = codex.CodexCredentialJobKeepalive
		if kind == codex.CodexCredentialJobKeepalive {
			schedule.nextRunAt = now.Add(8 * 24 * time.Hour)
		} else if schedule.keepaliveDueAt.After(now) {
			schedule.nextRunAt = schedule.keepaliveDueAt
		} else {
			schedule.nextRunAt = now
		}
		return
	}
	schedule.nextKind = ""
	schedule.nextRunAt = time.Time{}
}

func (r *codexQuotaRuntime) scheduleRetryLocked(schedule *codexCredentialSchedule, now time.Time) {
	if !schedule.quotaSupported {
		schedule.nextKind = ""
		schedule.nextRunAt = time.Time{}
		return
	}
	index := schedule.retryIndex
	if index >= len(codexQuotaRetryBackoff) {
		index = len(codexQuotaRetryBackoff) - 1
	}
	schedule.nextRunAt = now.Add(codexQuotaRetryBackoff[index])
	if schedule.retryIndex < len(codexQuotaRetryBackoff)-1 {
		schedule.retryIndex++
	}
	if schedule.nextKind == "" {
		schedule.nextKind = codex.CodexCredentialJobQuota
	}
}

func (r *codexQuotaRuntime) randomInitialDelayLocked(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return time.Duration(r.random.Int63n(int64(interval) + 1))
}

func (r *codexQuotaRuntime) randomJitterLocked(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	delta := int64(float64(interval) * 0.1)
	if delta <= 0 {
		return interval
	}
	return interval + time.Duration(r.random.Int63n(delta*2+1)-delta)
}

func (r *codexQuotaRuntime) randomCredentialGapLocked() time.Duration {
	return time.Duration(2+r.random.Intn(4)) * time.Second
}

func (r *codexQuotaRuntime) recordRuntimeError(err error) {
	r.mu.Lock()
	for profileID, status := range r.profileStatus {
		status.ErrorCode = FormatDesktopError(err).Code
		r.profileStatus[profileID] = status
	}
	r.rebuildStatusLocked()
	r.mu.Unlock()
	r.emitStatus()
}

func (r *codexQuotaRuntime) rebuildStatusLocked() {
	profiles := make([]CodexProfileQuotaRuntimeStatus, 0, len(r.profileStatus))
	for _, status := range r.profileStatus {
		status.Snapshot = cloneCodexQuotaSnapshot(status.Snapshot)
		status.Sub2APISnapshot = cloneCodexSub2APIQuotaSnapshot(status.Sub2APISnapshot)
		profiles = append(profiles, status)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ProfileID < profiles[j].ProfileID })
	r.status.Profiles = profiles
}

func (r *codexQuotaRuntime) emitStatus() {
	r.mu.RLock()
	emitter := r.emitter
	status := cloneCodexQuotaRuntimeStatus(r.status)
	r.mu.RUnlock()
	if emitter != nil {
		emitter(status)
	}
}

func (r *codexQuotaRuntime) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func cloneCodexQuotaRuntimeStatus(status CodexQuotaRuntimeStatus) CodexQuotaRuntimeStatus {
	status.Profiles = append([]CodexProfileQuotaRuntimeStatus(nil), status.Profiles...)
	for i := range status.Profiles {
		status.Profiles[i].Snapshot = cloneCodexQuotaSnapshot(status.Profiles[i].Snapshot)
		status.Profiles[i].Sub2APISnapshot = cloneCodexSub2APIQuotaSnapshot(status.Profiles[i].Sub2APISnapshot)
	}
	if status.Profiles == nil {
		status.Profiles = []CodexProfileQuotaRuntimeStatus{}
	}
	return status
}

func cloneCodexQuotaSnapshot(snapshot *codex.CodexQuotaSnapshot) *codex.CodexQuotaSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.AdditionalRateLimits = append([]codex.CodexQuotaRateLimit(nil), snapshot.AdditionalRateLimits...)
	return &cloned
}

func cloneCodexSub2APIQuotaSnapshot(snapshot *codex.CodexSub2APIQuotaSnapshot) *codex.CodexSub2APIQuotaSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.Windows = append([]codex.CodexSub2APIQuotaWindow(nil), snapshot.Windows...)
	return &cloned
}

func codexQuotaScheduleKey(target codex.CodexAutomationTarget) string {
	if target.CredentialID == "" {
		return ""
	}
	if target.QuotaSource == codex.CodexQuotaSourceSub2API {
		return target.CredentialID + "\x00" + target.ConfigSetID
	}
	return target.CredentialID
}

func codexQuotaJobMatchesSchedule(job *codexQuotaRuntimeJob, schedule *codexCredentialSchedule) bool {
	if job == nil || schedule == nil || job.credentialHash != schedule.credentialHash || job.quotaSource != schedule.quotaSource {
		return false
	}
	return job.quotaSource != codex.CodexQuotaSourceSub2API || job.configSetHash == schedule.configSetHash
}

func codexQuotaSchedulesMatch(left, right *codexCredentialSchedule) bool {
	if left == nil || right == nil || left.credentialHash != right.credentialHash || left.quotaSource != right.quotaSource {
		return false
	}
	return left.quotaSource != codex.CodexQuotaSourceSub2API || left.configSetHash == right.configSetHash
}

func nonBlockingQuotaResult(target chan codexQuotaManualResult, result codexQuotaManualResult) {
	select {
	case target <- result:
	default:
	}
}

func timeFromUnixMilli(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func unixMilliOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}
