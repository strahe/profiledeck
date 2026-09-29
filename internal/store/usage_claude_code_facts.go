package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

type ClaudeCodeUsagePricer func(CreateUsageFactParams) (*int64, UsageCostStatus, *int64, *UsagePriceSnapshot)

func (s *Store) reconcileClaudeCodeUsageFacts(ctx context.Context, candidates []CreateUsageFactParams, price ClaudeCodeUsagePricer) (UsageInsertResult, error) {
	var result UsageInsertResult
	for _, candidate := range candidates {
		if err := validateClaudeCodeUsageFact(candidate); err != nil {
			return UsageInsertResult{}, err
		}
		existing, err := s.getClaudeCodeUsageFact(ctx, candidate.EventKey)
		fresh := errors.Is(err, ErrNotFound)
		if err != nil && !fresh {
			return UsageInsertResult{}, err
		}
		merged := candidate
		if !fresh {
			if existing.SourceID != candidate.SourceID {
				return UsageInsertResult{}, ErrUsageFactConflict
			}
			merged = mergeClaudeCodeUsageFact(existing, candidate)
		}
		if merged.TokenStatus >= UsageTokensPartialConflict {
			merged.ModelKey = UsageUnknownModelKey
			merged.InputTokens = 0
			merged.CachedInputTokens = 0
			merged.OutputTokens = 0
			merged.TotalTokens = 0
			merged.CacheWriteInputTokens = nil
			merged.CacheCreationInputTokens = nil
			merged.CacheWrite5mTokens = nil
			merged.CacheWrite1hTokens = nil
			merged.EstimatedCostMicros = nil
			merged.CostStatus = UsageCostStatusUnknown
			if merged.TokenStatus == UsageTokensFinalConflict {
				merged.PriceSnapshot = nil
				merged.PricingCatalogVersion = nil
			}
			merged.PricingEligible = false
		} else if price != nil {
			merged.EstimatedCostMicros, merged.CostStatus, merged.PricingCatalogVersion, merged.PriceSnapshot = price(merged)
		}
		if err := validateClaudeCodeUsageFact(merged); err != nil {
			return UsageInsertResult{}, err
		}
		if !fresh && reflect.DeepEqual(existing, merged) {
			result.Duplicates++
			continue
		}
		if fresh {
			if _, err := s.insertUsageFactsWithSessionPolicy(ctx, []CreateUsageFactParams{merged}, usageFactSessionStrict); err != nil {
				return UsageInsertResult{}, err
			}
			result.Inserted++
		} else {
			result.Updated++
		}
		if err := s.writeClaudeCodeUsageFact(ctx, merged); err != nil {
			return UsageInsertResult{}, err
		}
	}
	return result, nil
}

func mergeClaudeCodeUsageFact(previous, candidate CreateUsageFactParams) CreateUsageFactParams {
	merged := previous
	switch {
	case previous.TokenStatus == UsageTokensFinalConflict:
	case previous.TokenStatus == UsageTokensComplete && candidate.TokenStatus == UsageTokensComplete:
		if !sameClaudeCodeUsageTokens(previous, candidate, true) {
			merged.TokenStatus = UsageTokensFinalConflict
		} else {
			mergeClaudeCodeCacheDetails(&merged, candidate)
			merged.PricingEligible = previous.PricingEligible && candidate.PricingEligible
		}
	case previous.TokenStatus != UsageTokensComplete && candidate.TokenStatus == UsageTokensComplete:
		merged = candidate
		if previous.PriceSnapshot != nil && previous.PriceSnapshot.ModelKey == candidate.ModelKey {
			merged.PriceSnapshot = previous.PriceSnapshot
		}
	case previous.TokenStatus == UsageTokensPartial && candidate.TokenStatus == UsageTokensPartial:
		if !sameClaudeCodeUsageTokens(previous, candidate, false) {
			merged.TokenStatus = UsageTokensPartialConflict
		} else {
			mergeClaudeCodeCacheDetails(&merged, candidate)
			merged.PricingEligible = previous.PricingEligible && candidate.PricingEligible
		}
	}
	if canonicalUsageObservationBefore(candidate.OccurredAtUnixMS, candidate.SessionKey, previous.OccurredAtUnixMS, previous.SessionKey) {
		merged.SessionKey = candidate.SessionKey
		merged.OccurredAtUnixMS = candidate.OccurredAtUnixMS
	} else {
		merged.SessionKey = previous.SessionKey
		merged.OccurredAtUnixMS = previous.OccurredAtUnixMS
	}
	if previous.ModelKey == merged.ModelKey && previous.PriceSnapshot != nil {
		merged.PriceSnapshot = previous.PriceSnapshot
	}
	return merged
}

func sameClaudeCodeUsageTokens(a, b CreateUsageFactParams, final bool) bool {
	return a.ModelKey == b.ModelKey && a.InputTokens == b.InputTokens && a.CachedInputTokens == b.CachedInputTokens &&
		(!final || a.OutputTokens == b.OutputTokens) && equalOptionalClaudeToken(a.CacheCreationInputTokens, b.CacheCreationInputTokens) &&
		equalOptionalClaudeToken(a.CacheWrite5mTokens, b.CacheWrite5mTokens) && equalOptionalClaudeToken(a.CacheWrite1hTokens, b.CacheWrite1hTokens)
}

func equalOptionalClaudeToken(a, b *int64) bool { return a == nil || b == nil || *a == *b }

func mergeClaudeCodeCacheDetails(target *CreateUsageFactParams, candidate CreateUsageFactParams) {
	if target.CacheWrite5mTokens == nil {
		target.CacheWrite5mTokens = candidate.CacheWrite5mTokens
		target.CacheWrite1hTokens = candidate.CacheWrite1hTokens
	}
}

func validateClaudeCodeUsageFact(fact CreateUsageFactParams) error {
	if err := validateUsageFact(fact); err != nil {
		return err
	}
	if !validClaudeCodeDerivedSessionKey(fact.SessionKey) || NormalizeUsageModelKey(fact.ModelKey) != fact.ModelKey || fact.TokenStatus < UsageTokensComplete || fact.TokenStatus > UsageTokensFinalConflict {
		return errors.New("claude code usage fact is invalid")
	}
	if fact.OutputTokens > 0 && fact.TokenStatus == UsageTokensPartial || fact.TotalTokens-fact.OutputTokens != fact.InputTokens {
		return errors.New("claude code usage totals are invalid")
	}
	if fact.CacheCreationInputTokens != nil && *fact.CacheCreationInputTokens > fact.InputTokens-fact.CachedInputTokens {
		return errors.New("claude code cache totals are invalid")
	}
	if (fact.CacheWrite5mTokens == nil) != (fact.CacheWrite1hTokens == nil) {
		return errors.New("claude code cache durations are invalid")
	}
	if fact.CacheWrite5mTokens != nil {
		if fact.CacheCreationInputTokens == nil || *fact.CacheWrite5mTokens < 0 || *fact.CacheWrite1hTokens < 0 || *fact.CacheWrite5mTokens > *fact.CacheCreationInputTokens || *fact.CacheWrite1hTokens != *fact.CacheCreationInputTokens-*fact.CacheWrite5mTokens {
			return errors.New("claude code cache durations are invalid")
		}
	}
	if fact.PriceSnapshot != nil {
		rates := fact.PriceSnapshot
		if NormalizeUsageModelKey(rates.ModelKey) == UsageUnknownModelKey || fact.TokenStatus < UsageTokensPartialConflict && rates.ModelKey != fact.ModelKey || rates.Version <= 0 || rates.Input <= 0 || rates.Input > 1_000_000_000 || rates.Output <= 0 || rates.Output > 1_000_000_000 {
			return errors.New("claude code pricing snapshot is invalid")
		}
		for _, rate := range []*int64{rates.CachedInput, rates.CacheWrite5m, rates.CacheWrite1h} {
			if rate != nil && (*rate <= 0 || *rate > 1_000_000_000) {
				return errors.New("claude code pricing snapshot is invalid")
			}
		}
	}
	_, _, err := usageCostStorageValues(fact.CostStatus, fact.EstimatedCostMicros)
	return err
}

func (s *Store) getClaudeCodeUsageFact(ctx context.Context, key UsageKey) (CreateUsageFactParams, error) {
	fact := CreateUsageFactParams{EventKey: key}
	var snapshot string
	err := s.executor().QueryRowContext(ctx, `SELECT f.source_id, COALESCE(s.session_key,''), m.model_key, f.occurred_at_unix_ms,
 f.input_tokens, f.cached_input_tokens, f.output_tokens, f.total_tokens, f.estimated_cost_micros, f.cost_status,
 f.pricing_catalog_version, f.cache_write_input_tokens, f.cache_creation_input_tokens, f.token_status,
 f.cache_write_5m_tokens, f.cache_write_1h_tokens, f.pricing_eligible, f.price_snapshot_json
 FROM usage_facts f JOIN usage_models m ON m.id=f.model_id AND m.source_id=f.source_id
 LEFT JOIN usage_sessions s ON s.id=f.session_id AND s.source_id=f.source_id WHERE f.event_key=?`, key).Scan(
		&fact.SourceID, &fact.SessionKey, &fact.ModelKey, &fact.OccurredAtUnixMS, &fact.InputTokens, &fact.CachedInputTokens, &fact.OutputTokens, &fact.TotalTokens,
		&fact.EstimatedCostMicros, &fact.CostStatus, &fact.PricingCatalogVersion, &fact.CacheWriteInputTokens, &fact.CacheCreationInputTokens, &fact.TokenStatus,
		&fact.CacheWrite5mTokens, &fact.CacheWrite1hTokens, &fact.PricingEligible, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return fact, ErrNotFound
	}
	if err != nil {
		return fact, err
	}
	if snapshot != "{}" {
		if err := json.Unmarshal([]byte(snapshot), &fact.PriceSnapshot); err != nil {
			return fact, errors.New("claude code pricing snapshot is invalid")
		}
	}
	return fact, nil
}

func (s *Store) writeClaudeCodeUsageFact(ctx context.Context, fact CreateUsageFactParams) error {
	sessionID, err := s.resolveUsageSessionID(ctx, fact.SourceID, fact.SessionKey, make(map[string]int64))
	if err != nil {
		return err
	}
	modelID, err := s.resolveUsageModelID(ctx, fact.SourceID, fact.ModelKey, make(map[string]int64))
	if err != nil {
		return err
	}
	snapshot := "{}"
	if fact.PriceSnapshot != nil {
		encoded, err := json.Marshal(fact.PriceSnapshot)
		if err != nil {
			return err
		}
		snapshot = string(encoded)
	}
	_, err = s.executor().ExecContext(ctx, `UPDATE usage_facts SET session_id=?, model_id=?, occurred_at_unix_ms=?,
 input_tokens=?, cached_input_tokens=?, output_tokens=?, total_tokens=?, estimated_cost_micros=?, cost_status=?,
 pricing_catalog_version=?, cache_write_input_tokens=?, cache_creation_input_tokens=?, token_status=?,
 cache_write_5m_tokens=?, cache_write_1h_tokens=?, pricing_eligible=?, price_snapshot_json=? WHERE source_id=? AND event_key=?`,
		sessionID, modelID, fact.OccurredAtUnixMS, fact.InputTokens, fact.CachedInputTokens, fact.OutputTokens, fact.TotalTokens, fact.EstimatedCostMicros, fact.CostStatus,
		fact.PricingCatalogVersion, fact.CacheWriteInputTokens, fact.CacheCreationInputTokens, fact.TokenStatus, fact.CacheWrite5mTokens, fact.CacheWrite1hTokens, fact.PricingEligible, snapshot, fact.SourceID, fact.EventKey)
	return err
}

func (s *Store) BackfillClaudeCodeUsageCosts(ctx context.Context, sourceID, generation int64, price ClaudeCodeUsagePricer) error {
	return s.WithTransaction(ctx, func(tx *Store) error {
		source, err := tx.reserveCurrentUsageSyncForWrite(ctx, sourceID, generation)
		if err != nil {
			return err
		}
		if source.ProviderID != "claude-code" {
			return errors.New("claude code usage source is invalid")
		}
		rows, err := tx.executor().QueryContext(ctx, `SELECT event_key FROM usage_facts WHERE source_id=? AND cost_status=0 AND token_status IN (0,1) AND pricing_eligible=1`, sourceID)
		if err != nil {
			return err
		}
		var keys []UsageKey
		for rows.Next() {
			var key UsageKey
			if err := rows.Scan(&key); err != nil {
				_ = rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		for _, key := range keys {
			fact, err := tx.getClaudeCodeUsageFact(ctx, key)
			if err != nil {
				return err
			}
			if _, err := tx.reconcileClaudeCodeUsageFacts(ctx, []CreateUsageFactParams{fact}, price); err != nil {
				return err
			}
		}
		return nil
	})
}
