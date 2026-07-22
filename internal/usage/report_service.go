package usage

import (
	"context"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

type UsageRangePreset string

const (
	UsageRangeToday  UsageRangePreset = "today"
	UsageRange7Days  UsageRangePreset = "7d"
	UsageRange30Days UsageRangePreset = "30d"
	UsageRangeAll    UsageRangePreset = "all"
)

type UsageReportRequest struct {
	ProviderID string           `json:"provider_id"`
	Range      UsageRangePreset `json:"range"`
}

type UsageResolvedRange struct {
	Preset             UsageRangePreset `json:"preset"`
	StartUnixMS        int64            `json:"start_unix_ms"`
	EndExclusiveUnixMS int64            `json:"end_exclusive_unix_ms"`
	BucketUnit         string           `json:"bucket_unit"`
	TimeZone           string           `json:"time_zone"`
}

type UsageAggregateSummary struct {
	EventCount              int64   `json:"event_count"`
	SessionCount            int64   `json:"session_count"`
	FreshInputTokens        int64   `json:"fresh_input_tokens"`
	InputTokens             int64   `json:"input_tokens"`
	CachedInputTokens       int64   `json:"cached_input_tokens"`
	OutputTokens            int64   `json:"output_tokens"`
	TotalTokens             int64   `json:"total_tokens"`
	CacheHitRate            float64 `json:"cache_hit_rate"`
	KnownEstimatedCostUSD   string  `json:"known_estimated_cost_usd"`
	CostStatus              string  `json:"cost_status"`
	EstimatedCostEventCount int64   `json:"estimated_cost_event_count"`
	PartialCostEventCount   int64   `json:"partial_cost_event_count"`
	UnknownCostEventCount   int64   `json:"unknown_cost_event_count"`
	EstimatedTokenCount     int64   `json:"estimated_token_count"`
	PricingCoverage         float64 `json:"pricing_coverage"`
	UndatedEventCount       int64   `json:"undated_event_count"`
}

type UsageTrendPoint struct {
	StartUnixMS int64                 `json:"start_unix_ms"`
	EndUnixMS   int64                 `json:"end_unix_ms"`
	Summary     UsageAggregateSummary `json:"summary"`
}

type UsageModelSummary struct {
	Model   string                `json:"model"`
	Summary UsageAggregateSummary `json:"summary"`
}

type UsageImportSummary struct {
	TrackedFiles       int64 `json:"tracked_files"`
	LastSyncedAtUnixMS int64 `json:"last_synced_at_unix_ms"`
	InvalidLines       int64 `json:"invalid_lines"`
	UnsupportedLines   int64 `json:"unsupported_lines"`
}

type UsagePricingInfo struct {
	Basis               string `json:"basis"`
	SourceURL           string `json:"source_url"`
	VerifiedAt          string `json:"verified_at"`
	HistoricalRepricing bool   `json:"historical_repricing"`
}

// Codex session logs do not prove which stored credential served a request;
// reports intentionally stop at provider, model, time, and session aggregates.
type UsageReportResult struct {
	ProviderID string                `json:"provider_id"`
	Source     string                `json:"source"`
	Sources    []string              `json:"sources"`
	Range      UsageResolvedRange    `json:"range"`
	Summary    UsageAggregateSummary `json:"summary"`
	Trend      []UsageTrendPoint     `json:"trend"`
	Models     []UsageModelSummary   `json:"models"`
	Import     UsageImportSummary    `json:"import"`
	Pricing    UsagePricingInfo      `json:"pricing"`
}

func (service *Service) Report(ctx context.Context, req UsageReportRequest) (UsageReportResult, error) {
	return service.usageReportAt(ctx, req, time.Now())
}

func (service *Service) usageReportAt(ctx context.Context, req UsageReportRequest, now time.Time) (UsageReportResult, error) {
	providerID, integration, appErr := service.resolveIntegration(req.ProviderID)
	if appErr != nil {
		return UsageReportResult{}, appErr
	}
	rangeValue := req.Range
	if rangeValue == "" {
		rangeValue = UsageRange7Days
	}
	preset, err := ParseRangePreset(string(rangeValue))
	if err != nil {
		return UsageReportResult{}, apperror.New(apperror.UsageInvalid, "unsupported usage range")
	}

	if err := service.requireProvider(ctx, providerID); err != nil {
		return UsageReportResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return UsageReportResult{}, err
	}
	defer db.Close()

	var resolved ResolvedRange
	var snapshot store.UsageReportSnapshot
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		earliest, err := txStore.EarliestDatedUsageUnixMS(ctx, providerID)
		if err != nil {
			return err
		}
		resolved, err = ResolveRange(preset, now, earliest)
		if err != nil {
			return err
		}
		buckets := make([]store.UsageTimeBucket, 0, len(resolved.Buckets))
		for _, bucket := range resolved.Buckets {
			buckets = append(buckets, store.UsageTimeBucket{
				StartUnixMS: bucket.StartUnixMS,
				EndUnixMS:   bucket.EndUnixMS,
			})
		}
		snapshot, err = txStore.UsageReport(ctx, store.UsageReportQuery{
			ProviderID:  providerID,
			StartUnixMS: resolved.StartUnixMS,
			EndUnixMS:   resolved.EndUnixMS,
			Buckets:     buckets,
		})
		return err
	})
	if err != nil {
		return UsageReportResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read usage report", err)
	}

	startUnixMS := resolved.EndUnixMS
	if len(resolved.Buckets) > 0 {
		startUnixMS = resolved.Buckets[0].StartUnixMS
	}
	result := UsageReportResult{
		ProviderID: providerID,
		Source:     summarySource(snapshot.Sources),
		Sources:    snapshot.Sources,
		Range: UsageResolvedRange{
			Preset:             UsageRangePreset(resolved.Preset),
			StartUnixMS:        startUnixMS,
			EndExclusiveUnixMS: resolved.EndUnixMS,
			BucketUnit:         string(resolved.BucketUnit),
			TimeZone:           resolved.TimeZone,
		},
		Summary: usageAggregateSummary(snapshot.Summary),
		Import: UsageImportSummary{
			TrackedFiles:       snapshot.ImportSummary.TrackedFiles,
			LastSyncedAtUnixMS: snapshot.ImportSummary.LastSyncedAtUnixMS,
			InvalidLines:       snapshot.ImportSummary.InvalidLines,
			UnsupportedLines:   snapshot.ImportSummary.UnsupportedLines,
		},
		Pricing: integration.PricingInfo(),
	}
	result.Trend = make([]UsageTrendPoint, 0, len(snapshot.Trend))
	for _, point := range snapshot.Trend {
		bucket := resolved.Buckets[point.BucketIndex]
		result.Trend = append(result.Trend, UsageTrendPoint{
			StartUnixMS: bucket.StartUnixMS,
			EndUnixMS:   bucket.EndUnixMS,
			Summary:     usageAggregateSummary(point.UsageAggregate),
		})
	}
	result.Models = make([]UsageModelSummary, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		result.Models = append(result.Models, UsageModelSummary{
			Model:   model.Model,
			Summary: usageAggregateSummary(model.UsageAggregate),
		})
	}
	return result, nil
}

func usageAggregateSummary(aggregate store.UsageAggregate) UsageAggregateSummary {
	cacheHitRate := ratio(aggregate.CachedInputTokens, aggregate.InputTokens)
	pricingCoverage := ratio(aggregate.EstimatedTokenCount, aggregate.TotalTokens)
	costStatus := CostStatusUnknown.String()
	if aggregate.EventCount > 0 && aggregate.UnknownCostEvents == 0 && aggregate.PartialCostEventCount > 0 {
		costStatus = CostStatusPartial.String()
	} else if aggregate.EventCount > 0 && aggregate.UnknownCostEvents == 0 {
		costStatus = CostStatusEstimated.String()
	}
	return UsageAggregateSummary{
		EventCount:              aggregate.EventCount,
		SessionCount:            aggregate.SessionCount,
		FreshInputTokens:        aggregate.FreshInputTokens,
		InputTokens:             aggregate.InputTokens,
		CachedInputTokens:       aggregate.CachedInputTokens,
		OutputTokens:            aggregate.OutputTokens,
		TotalTokens:             aggregate.TotalTokens,
		CacheHitRate:            cacheHitRate,
		KnownEstimatedCostUSD:   USDStringFromMicros(aggregate.EstimatedCostMicros),
		CostStatus:              costStatus,
		EstimatedCostEventCount: aggregate.EstimatedCostEventCount,
		PartialCostEventCount:   aggregate.PartialCostEventCount,
		UnknownCostEventCount:   aggregate.UnknownCostEvents,
		EstimatedTokenCount:     aggregate.EstimatedTokenCount,
		PricingCoverage:         pricingCoverage,
		UndatedEventCount:       aggregate.UndatedEventCount,
	}
}

func ratio(numerator, denominator int64) float64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator)
	if value > 1 {
		return 1
	}
	return value
}
