package usage

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	stores   store.Factory
	registry Registry
	policy   agent.Policy
}

func NewService(stores store.Factory, registry Registry, policy agent.Policy) *Service {
	return &Service{stores: stores, registry: registry, policy: policy}
}

func (service *Service) requireProvider(ctx context.Context, providerID string) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireProvider(ctx, providerID)
}

type UsageImportError struct {
	SourceKey string `json:"source_key"`
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
	ProviderID              string   `json:"provider_id"`
	Source                  string   `json:"source"`
	Sources                 []string `json:"sources"`
	EventCount              int64    `json:"event_count"`
	InputTokens             int64    `json:"input_tokens"`
	CachedInputTokens       int64    `json:"cached_input_tokens"`
	OutputTokens            int64    `json:"output_tokens"`
	TotalTokens             int64    `json:"total_tokens"`
	EstimatedCostUSD        *string  `json:"estimated_cost_usd"`
	CostStatus              string   `json:"cost_status"`
	UnknownCostEventCount   int64    `json:"unknown_cost_event_count"`
	EstimatedCostEventCount int64    `json:"estimated_cost_event_count"`
}

func (service *Service) Sync(ctx context.Context, req UsageSyncRequest) (UsageSyncResult, error) {
	providerID, integration, appErr := service.resolveIntegration(req.ProviderID)
	if appErr != nil {
		return UsageSyncResult{}, appErr
	}
	if err := service.requireProvider(ctx, providerID); err != nil {
		return UsageSyncResult{}, err
	}
	result, err := integration.Sync(ctx, service.stores)
	if err != nil {
		return UsageSyncResult{}, usageSyncError(providerID, err)
	}
	return result, nil
}

func usageSyncError(providerID string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrUsageProviderDisabled):
		return apperror.Wrap(apperror.ProviderDisabled, "Provider is disabled", err).
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

func (service *Service) Summary(ctx context.Context, req UsageSummaryRequest) (UsageSummaryResult, error) {
	providerID, _, appErr := service.resolveIntegration(req.ProviderID)
	if appErr != nil {
		return UsageSummaryResult{}, appErr
	}
	if err := service.requireProvider(ctx, providerID); err != nil {
		return UsageSummaryResult{}, err
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
		ProviderID:              providerID,
		Source:                  summarySource(summary.Sources),
		Sources:                 summary.Sources,
		EventCount:              summary.EventCount,
		InputTokens:             summary.InputTokens,
		CachedInputTokens:       summary.CachedInputTokens,
		OutputTokens:            summary.OutputTokens,
		TotalTokens:             summary.TotalTokens,
		CostStatus:              CostStatusEstimated.String(),
		UnknownCostEventCount:   summary.UnknownCostEvents + summary.PartialCostEvents,
		EstimatedCostEventCount: summary.EstimatedCostEventCount,
	}
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
