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
			if !ok {
				return nil, CostStatusUnknown, nil, nil
			}
			snapshot = &store.UsagePriceSnapshot{ModelKey: fact.ModelKey, Version: selected.Version, Input: selected.Rates.Input, CachedInput: selected.Rates.CachedInput, CacheWrite5m: selected.Rates.CacheWrite, CacheWrite1h: selected.Rates.CacheWrite1h, Output: selected.Rates.Output}
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
	return db.BackfillClaudeCodeUsageCosts(ctx, sourceID, generation, claudeCodePricer(pricingSnapshot(ctx)))
}
