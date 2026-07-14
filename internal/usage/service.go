package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	stores   store.Factory
	codexDir string
	policy   agent.Policy
}

func NewService(stores store.Factory, codexDir string, policy agent.Policy) *Service {
	return &Service{stores: stores, codexDir: codexDir, policy: policy}
}

func (service *Service) requireProvider(ctx context.Context, providerID string) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireProvider(ctx, providerID)
}

func requireEnabledProviderIfPresent(ctx context.Context, db *store.Store, providerID string) error {
	provider, err := db.GetProvider(ctx, providerID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to read usage Provider", err).
			WithDetail("provider_id", providerID)
	}
	if !provider.Enabled {
		return apperror.New(apperror.ProviderDisabled, "Provider is disabled").
			WithDetail("provider_id", providerID)
	}
	return nil
}

type UsageImportError struct {
	SourceKey string `json:"source_key"`
	FileName  string `json:"file_name,omitempty"`
	Message   string `json:"message"`
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

func (service *Service) SyncCodex(ctx context.Context) (UsageSyncResult, error) {
	if err := service.requireProvider(ctx, ProviderCodex); err != nil {
		return UsageSyncResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return UsageSyncResult{}, err
	}
	defer db.Close()
	if err := requireEnabledProviderIfPresent(ctx, db, ProviderCodex); err != nil {
		return UsageSyncResult{}, err
	}

	files, err := ListCodexSessionFilesContext(ctx, service.codexDir)
	if err != nil {
		return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to list Codex session files", err)
	}

	result := UsageSyncResult{
		ProviderID: ProviderCodex,
		Source:     SourceCodexSessionJSONL,
	}
	var freshnessCursor store.UsageImportCursor
	hasFreshnessCursor := false
	cursorCommitted := false
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", err)
		}
		result.ScannedFiles++
		cursor, hasCursor, unchanged, err := usageSourceCursor(ctx, db, file)
		if err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to inspect usage import cursor", err)
		}
		if unchanged {
			if !hasFreshnessCursor {
				freshnessCursor = cursor
				hasFreshnessCursor = true
			}
			result.SkippedUnchangedFiles++
			continue
		}

		parsed, err := ParseCodexSessionFileContext(ctx, file)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", ctxErr)
			}
			result.Errors = append(result.Errors, UsageImportError{
				SourceKey: file.SourceKey,
				FileName:  filepath.Base(file.Path),
				Message:   sanitizedUsageImportError(err),
			})
			continue
		}

		eventsToStore := usageEventsAfterCursor(parsed.Events, cursor, hasCursor, file)
		insertResult, err := db.CommitUsageImport(ctx, store.CommitUsageImportParams{
			Events: usageEventsToStoreParams(eventsToStore),
			Cursor: store.UpsertUsageImportCursorParams{
				ProviderID:       ProviderCodex,
				Source:           SourceCodexSessionJSONL,
				SourceKey:        file.SourceKey,
				ModifiedUnixMS:   file.ModifiedUnixMS,
				SizeBytes:        file.SizeBytes,
				ImportedEvents:   int64(len(parsed.Events)),
				InvalidLines:     parsed.InvalidLines,
				UnsupportedLines: parsed.UnsupportedLines,
				MetadataJSON:     usageCursorMetadataJSON(parsed.Events),
			},
		})
		if err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to commit usage import", err)
		}
		cursorCommitted = true

		result.ImportedEvents += int64(insertResult.Inserted)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += parsed.InvalidLines
		result.UnsupportedLines += parsed.UnsupportedLines
	}
	if !cursorCommitted && hasFreshnessCursor {
		// A completed no-op scan needs one freshness write for report status, not
		// one write per unchanged session file. Touch updates no cursor identity or
		// import progress, so a concurrent importer cannot be moved backwards.
		if err := db.TouchUsageImportCursor(ctx, freshnessCursor.ProviderID, freshnessCursor.Source, freshnessCursor.SourceKey); err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to refresh usage import cursor", err)
		}
	}
	if err := backfillPartialUsageCosts(ctx, db); err != nil {
		return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to update usage pricing", err)
	}

	return result, nil
}

func backfillPartialUsageCosts(ctx context.Context, db *store.Store) error {
	const batchSize = 256
	models := PartialCostModelIDs()
	afterID := ""
	for {
		candidates, err := db.ListUnknownUsageCostCandidates(ctx, ProviderCodex, models, afterID, batchSize)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		updates := make([]store.UpdateUsageEventCostParams, 0, len(candidates))
		for _, candidate := range candidates {
			cost, status := EstimateCostMicros(candidate.Model, TokenCounts{
				InputTokens:       candidate.InputTokens,
				CachedInputTokens: candidate.CachedInputTokens,
				OutputTokens:      candidate.OutputTokens,
				TotalTokens:       candidate.TotalTokens,
			})
			if cost == nil || status == CostStatusUnknown {
				continue
			}
			updates = append(updates, store.UpdateUsageEventCostParams{
				ID:                  candidate.ID,
				EstimatedCostMicros: *cost,
				CostStatus:          status,
			})
		}
		// Only unknown rows are eligible, so concurrent import/backfill runs are
		// idempotent and never overwrite an already classified historical event.
		if _, err := db.UpdateUnknownUsageEventCosts(ctx, updates); err != nil {
			return err
		}
		afterID = candidates[len(candidates)-1].ID
		if len(candidates) < batchSize {
			return nil
		}
	}
}

func (service *Service) Summary(ctx context.Context, req UsageSummaryRequest) (UsageSummaryResult, error) {
	providerID, appErr := normalizeUsageProviderID(req.ProviderID)
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
	if err := requireEnabledProviderIfPresent(ctx, db, providerID); err != nil {
		return UsageSummaryResult{}, err
	}

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
		CostStatus:              CostStatusEstimated,
		UnknownCostEventCount:   summary.UnknownCostEvents + summary.PartialCostEvents,
		EstimatedCostEventCount: summary.EstimatedCostEventCount,
	}
	// The legacy summary contract has no partial-cost state. Keep treating any
	// incomplete subtotal as unknown instead of silently overstating precision.
	if result.UnknownCostEventCount > 0 {
		result.CostStatus = CostStatusUnknown
		return result, nil
	}

	cost := USDStringFromMicros(summary.EstimatedCostMicros)
	result.EstimatedCostUSD = &cost
	return result, nil
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

func sanitizedUsageImportError(err error) string {
	if err == nil {
		return ""
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func usageSourceCursor(ctx context.Context, db *store.Store, file SourceFile) (store.UsageImportCursor, bool, bool, error) {
	cursor, err := db.GetUsageImportCursor(ctx, ProviderCodex, SourceCodexSessionJSONL, file.SourceKey)
	if errors.Is(err, store.ErrNotFound) {
		return store.UsageImportCursor{}, false, false, nil
	}
	if err != nil {
		return store.UsageImportCursor{}, false, false, err
	}
	unchanged := cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes
	return cursor, true, unchanged, nil
}

func usageEventsAfterCursor(events []Event, cursor store.UsageImportCursor, hasCursor bool, file SourceFile) []Event {
	if !hasCursor || cursor.ImportedEvents <= 0 || cursor.ImportedEvents > int64(len(events)) {
		return events
	}
	if file.SizeBytes <= cursor.SizeBytes || !usageCursorPrefixMatches(events, cursor) {
		return events
	}
	return events[cursor.ImportedEvents:]
}

func usageCursorPrefixMatches(events []Event, cursor store.UsageImportCursor) bool {
	var metadata struct {
		EventDigest string `json:"event_digest"`
	}
	if err := json.Unmarshal([]byte(cursor.MetadataJSON), &metadata); err != nil || metadata.EventDigest == "" {
		return false
	}
	return metadata.EventDigest == EventDigest(events, cursor.ImportedEvents)
}

func usageCursorMetadataJSON(events []Event) string {
	raw, err := json.Marshal(map[string]string{
		"parser_version": CodexSessionParserVersion,
		"event_digest":   EventDigest(events, int64(len(events))),
	})
	if err != nil {
		return `{"parser_version":"` + CodexSessionParserVersion + `"}`
	}
	return string(raw)
}

func usageEventsToStoreParams(events []Event) []store.CreateUsageEventParams {
	params := make([]store.CreateUsageEventParams, 0, len(events))
	for _, event := range events {
		params = append(params, store.CreateUsageEventParams{
			ID:                  event.ID,
			ProviderID:          event.ProviderID,
			Source:              event.Source,
			SourceKey:           event.SourceKey,
			SessionID:           event.SessionID,
			Model:               event.Model,
			OccurredAtUnixMS:    event.OccurredAtUnixMS,
			InputTokens:         event.InputTokens,
			CachedInputTokens:   event.CachedInputTokens,
			OutputTokens:        event.OutputTokens,
			TotalTokens:         event.TotalTokens,
			EstimatedCostMicros: event.EstimatedCostMicros,
			CostStatus:          event.CostStatus,
			MetadataJSON:        event.MetadataJSON,
		})
	}
	return params
}

func normalizeUsageProviderID(providerID string) (string, *apperror.Error) {
	if providerID == "" {
		return ProviderCodex, nil
	}
	id, appErr := validate.ID(providerID, apperror.UsageInvalid)
	if appErr != nil {
		return "", appErr
	}
	if id != ProviderCodex {
		return "", apperror.New(apperror.UsageInvalid, "unsupported usage provider")
	}
	return id, nil
}
