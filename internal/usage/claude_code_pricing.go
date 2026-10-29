package usage

import (
	"context"

	"github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
)

func claudeCodePricer(catalog pricing.Catalog) store.ClaudeCodeUsagePricer {
	return func(fact store.CreateUsageFactParams) (*int64, store.UsageCostStatus, *int64, *store.UsagePriceSnapshot) {
		if !fact.PricingEligible || fact.TokenStatus >= store.UsageTokensPartialConflict {
			return nil, CostStatusUnknown, nil, nil
		}
		snapshot := fact.PriceSnapshot
		if snapshot == nil {
			selected, ok := catalog.Select("claude-code", fact.ModelKey, fact.OccurredAtUnixMS)
			if !ok || selected.Requirements.CompleteTokens && fact.TokenStatus != store.UsageTokensComplete {
				return nil, CostStatusUnknown, nil, nil
			}
			rates := selected.Rates
			if selected.Requirements.CompleteTokens && selected.Long != nil && fact.InputTokens > selected.Above {
				rates = *selected.Long
			}
			snapshot = &store.UsagePriceSnapshot{ModelKey: fact.ModelKey, Version: selected.Version, Input: rates.Input, CachedInput: rates.CachedInput, CacheWrite5m: rates.CacheWrite, CacheWrite1h: rates.CacheWrite1h, Output: rates.Output}
		}
		if fact.CacheCreationInputTokens == nil {
			return nil, CostStatusUnknown, nil, nil
		}
		fresh := fact.InputTokens - fact.CachedInputTokens - *fact.CacheCreationInputTokens
		partial := fact.TokenStatus != store.UsageTokensComplete
		cost := int64(0)
		components := []struct {
			count int64
			rate  *int64
		}{{fresh, &snapshot.Input}, {fact.CachedInputTokens, snapshot.CachedInput}, {fact.OutputTokens, &snapshot.Output}}
		if *fact.CacheCreationInputTokens > 0 {
			if fact.CacheWrite5mTokens == nil {
				partial = true
			} else {
				components = append(components, struct {
					count int64
					rate  *int64
				}{*fact.CacheWrite5mTokens, snapshot.CacheWrite5m}, struct {
					count int64
					rate  *int64
				}{*fact.CacheWrite1hTokens, snapshot.CacheWrite1h})
			}
		}
		for _, component := range components {
			if component.count == 0 {
				continue
			}
			if component.rate == nil {
				partial = true
				continue
			}
			amount, ok := roundedTokenCostMicrosSafe(component.count, *component.rate)
			if !ok {
				return nil, CostStatusUnknown, nil, nil
			}
			cost, ok = addCostMicros(cost, amount)
			if !ok {
				return nil, CostStatusUnknown, nil, nil
			}
		}
		status := CostStatusEstimated
		if partial {
			status = CostStatusPartial
		}
		return &cost, status, &snapshot.Version, snapshot
	}
}

func backfillClaudeCodeUsageCosts(ctx context.Context, db *store.Store, sourceID, generation int64) error {
	catalog := pricingSnapshot(ctx)
	models, err := db.ListUnknownUsageCostModels(ctx, "claude-code")
	if err != nil {
		return err
	}
	var filters []store.UsageCostFilter
	for _, model := range models {
		if model.SourceID == sourceID {
			filters = append(filters, usageCostFilters(catalog, "claude-code", model)...)
		}
	}
	return db.BackfillClaudeCodeUsageCosts(ctx, sourceID, generation, filters, claudeCodePricer(catalog))
}
