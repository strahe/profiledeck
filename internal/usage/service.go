package usage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	stores   store.Factory
	registry Registry
	pricing  *pricing.Service
	syncMu   sync.Mutex
}

func NewService(stores store.Factory, registry Registry, priceServices ...*pricing.Service) *Service {
	service := &Service{stores: stores, registry: registry}
	if len(priceServices) > 0 {
		service.pricing = priceServices[0]
	}
	return service
}

type UsageImportError struct {
	SourceKey string `json:"source_key,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	Message   string `json:"message"`
}

type UsageSyncRequest struct {
	ProviderID string `json:"provider_id"`
}

type UsageSyncResult struct {
	ProviderID             string             `json:"provider_id"`
	Source                 string             `json:"source"`
	ScannedFiles           int64              `json:"scanned_files"`
	SkippedUnchangedFiles  int64              `json:"skipped_unchanged_files"`
	ImportedEvents         int64              `json:"imported_events"`
	SkippedDuplicateEvents int64              `json:"skipped_duplicate_events"`
	UnsupportedLines       int64              `json:"unsupported_lines"`
	InvalidLines           int64              `json:"invalid_lines"`
	Errors                 []UsageImportError `json:"errors"`
}

type UsageSummaryRequest struct {
	ProviderID string `json:"provider_id"`
}

type UsageSummaryResult struct {
	ProviderID                    string   `json:"provider_id"`
	Source                        string   `json:"source"`
	Sources                       []string `json:"sources"`
	EventCount                    int64    `json:"event_count"`
	InputTokens                   int64    `json:"input_tokens"`
	CachedInputTokens             int64    `json:"cached_input_tokens"`
	OutputTokens                  int64    `json:"output_tokens"`
	TotalTokens                   int64    `json:"total_tokens"`
	EstimatedCostUSD              *string  `json:"estimated_cost_usd"`
	CostStatus                    string   `json:"cost_status"`
	UnknownCostEventCount         int64    `json:"unknown_cost_event_count"`
	EstimatedCostEventCount       int64    `json:"estimated_cost_event_count"`
	ReportedCostUSD               *string  `json:"reported_cost_usd"`
	ReportedCostStatus            string   `json:"reported_cost_status"`
	UnknownReportedCostEventCount int64    `json:"unknown_reported_cost_event_count"`
	ReportedCostEventCount        int64    `json:"reported_cost_event_count"`
	PartialReportedCostEventCount int64    `json:"partial_reported_cost_event_count"`
}

func (service *Service) Sync(ctx context.Context, req UsageSyncRequest) (UsageSyncResult, error) {
	outcome, err := service.sync(ctx, req, SyncOptions{
		ProvisionMode:      SyncProvisionProvider,
		ForceObservedRetry: true,
	})
	return outcome.Result, err
}

func (service *Service) sync(
	ctx context.Context,
	req UsageSyncRequest,
	options SyncOptions,
) (SyncOutcome, error) {
	providerID, integration, appErr := service.resolveIntegration(req.ProviderID)
	if appErr != nil {
		return SyncOutcome{}, appErr
	}

	workCtx, releaseWork, err := service.acquireSyncForWork(ctx)
	if err != nil {
		return SyncOutcome{}, usageSyncError(providerID, err)
	}
	defer releaseWork()
	if service.pricing != nil {
		catalog, err := service.pricing.Snapshot(workCtx)
		if err != nil {
			return SyncOutcome{}, usageSyncError(providerID, err)
		}
		workCtx = withPricingSnapshot(workCtx, catalog)
	}

	outcome, err := integration.Sync(workCtx, service.stores, options)
	if options.ProvisionMode == SyncExistingProvider && errors.Is(err, store.ErrUsageProviderMissing) {
		return SyncOutcome{Result: UsageSyncResult{
			ProviderID: providerID,
			Source:     summarySource(integration.SourceIDs()),
			Errors:     []UsageImportError{},
		}}, nil
	}
	if err != nil {
		return SyncOutcome{}, usageSyncError(providerID, err)
	}
	return outcome, nil
}

type phaseTimeoutKey struct{}

func WithPhaseTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, phaseTimeoutKey{}, timeout)
}

func (service *Service) acquireSyncForWork(ctx context.Context) (context.Context, func(), error) {
	budget, hasBudget := syncPhaseBudget(ctx)
	waitCtx, stopWait := syncWaitContext(ctx, budget, hasBudget)
	defer stopWait()

	if err := service.acquireSync(waitCtx); err != nil {
		return nil, func() {}, err
	}

	workCtx := ctx
	release := func() { service.syncMu.Unlock() }
	if hasBudget {
		var cancel context.CancelFunc
		workCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), budget)
		release = func() {
			cancel()
			service.syncMu.Unlock()
		}
		stop := context.AfterFunc(ctx, cancel)
		prev := release
		release = func() {
			stop()
			prev()
		}
	}
	return workCtx, release, nil
}

func syncPhaseBudget(ctx context.Context) (time.Duration, bool) {
	if timeout, ok := ctx.Value(phaseTimeoutKey{}).(time.Duration); ok && timeout > 0 {
		return timeout, true
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	budget := time.Until(deadline)
	if budget < 0 {
		return 0, true
	}
	return budget, true
}

func syncWaitContext(ctx context.Context, budget time.Duration, hasBudget bool) (context.Context, context.CancelFunc) {
	base, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, cancelBase)
	cleanup := func() {
		stop()
		cancelBase()
	}
	if !hasBudget {
		return base, cleanup
	}
	waitCtx, cancelWait := context.WithTimeout(base, budget)
	return waitCtx, func() {
		cancelWait()
		cleanup()
	}
}

func (service *Service) acquireSync(ctx context.Context) error {
	const poll = 20 * time.Millisecond
	timer := time.NewTimer(poll)
	defer timer.Stop()
	for {
		if service.syncMu.TryLock() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		timer.Reset(poll)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func usageSyncError(providerID string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrUsageProviderMissing):
		return apperror.Wrap(apperror.ProviderNotFound, "Provider is unavailable", err).
			WithDetail("provider_id", providerID)
	case errors.Is(err, store.ErrUsageIdentityRevision):
		return apperror.Wrap(
			apperror.UsageMigrationRequired,
			"Stored usage cannot be updated safely with this ProfileDeck version",
			err,
		)
	case errors.Is(err, store.ErrUsageCursorConflict), errors.Is(err, store.ErrUsageSyncSuperseded):
		return apperror.Wrap(
			apperror.UsageSyncConflict,
			"Usage changed during sync; run the sync again",
			err,
		)
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) && apperror.KnownCode(appErr.Code) {
		return err
	}
	return apperror.Wrap(apperror.UsageImportFailed, "Usage could not be synchronized; try again", err)
}

func (service *Service) SyncCodex(ctx context.Context) (UsageSyncResult, error) {
	return service.Sync(ctx, UsageSyncRequest{ProviderID: ProviderCodex})
}

// SyncCodexBackground never provisions a deleted Provider. A later explicit
// sync remains the only action that may recreate it.
func (service *Service) SyncCodexBackground(
	ctx context.Context,
	onWorkDetected func(),
) (BackgroundSyncOutcome, error) {
	return service.sync(ctx, UsageSyncRequest{ProviderID: ProviderCodex}, SyncOptions{
		ProvisionMode:  SyncExistingProvider,
		OnWorkDetected: onWorkDetected,
	})
}

func (service *Service) SyncGrokBuild(ctx context.Context) (UsageSyncResult, error) {
	return service.Sync(ctx, UsageSyncRequest{ProviderID: grokconfig.ProviderID})
}

// SyncProviderBackground never provisions a deleted Provider.
func (service *Service) SyncProviderBackground(
	ctx context.Context,
	providerID string,
	onWorkDetected func(),
) (BackgroundSyncOutcome, error) {
	return service.sync(ctx, UsageSyncRequest{ProviderID: providerID}, SyncOptions{
		ProvisionMode:  SyncExistingProvider,
		OnWorkDetected: onWorkDetected,
	})
}

func (service *Service) Summary(ctx context.Context, req UsageSummaryRequest) (UsageSummaryResult, error) {
	providerID, _, appErr := service.resolveIntegration(req.ProviderID)
	if appErr != nil {
		return UsageSummaryResult{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return UsageSummaryResult{}, err
	}
	defer db.Close()

	summary, err := db.UsageSummary(ctx, providerID)
	if err != nil {
		return UsageSummaryResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read usage summary", err)
	}
	result := UsageSummaryResult{
		ProviderID:                    providerID,
		Source:                        summarySource(summary.Sources),
		Sources:                       summary.Sources,
		EventCount:                    summary.EventCount,
		InputTokens:                   summary.InputTokens,
		CachedInputTokens:             summary.CachedInputTokens,
		OutputTokens:                  summary.OutputTokens,
		TotalTokens:                   summary.TotalTokens,
		CostStatus:                    CostStatusEstimated.String(),
		UnknownCostEventCount:         summary.UnknownCostEvents + summary.PartialCostEvents,
		EstimatedCostEventCount:       summary.EstimatedCostEventCount,
		ReportedCostStatus:            ReportedCostStatusUnknown.String(),
		UnknownReportedCostEventCount: summary.UnknownReportedCostEvents,
		ReportedCostEventCount:        summary.ReportedCostEventCount,
		PartialReportedCostEventCount: summary.PartialReportedCostEvents,
	}
	if summary.ReportedCostEventCount+summary.PartialReportedCostEvents > 0 {
		reportedCost := USDStringFromTicks(summary.ReportedCostUSDTicks)
		result.ReportedCostUSD = &reportedCost
	}
	result.ReportedCostStatus = aggregateReportedCostStatus(
		summary.EventCount,
		summary.ReportedCostEventCount,
		summary.PartialReportedCostEvents,
		summary.UnknownReportedCostEvents,
	)
	// The legacy summary contract has no partial-cost state. Keep treating any
	// incomplete subtotal as unknown instead of overstating precision.
	if result.UnknownCostEventCount > 0 {
		result.CostStatus = CostStatusUnknown.String()
		return result, nil
	}
	cost := USDStringFromMicros(summary.EstimatedCostMicros)
	result.EstimatedCostUSD = &cost
	return result, nil
}

func (service *Service) resolveIntegration(providerID string) (string, Integration, *apperror.Error) {
	if providerID == "" {
		providerID = ProviderCodex
	}
	id, appErr := validate.ID(providerID, apperror.UsageInvalid)
	if appErr != nil {
		return "", nil, appErr
	}
	integration, ok := service.registry.Integration(id)
	if !ok {
		return "", nil, apperror.New(apperror.UsageInvalid, "unsupported usage provider")
	}
	return id, integration, nil
}

func summarySource(sources []string) string {
	switch len(sources) {
	case 0:
		return ""
	case 1:
		return sources[0]
	default:
		return "multiple"
	}
}
