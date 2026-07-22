package usage

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

const codexHistoryChangedMessage = "This Codex session file changed before the saved import point, so it was skipped to protect existing usage history."

type codexIntegration struct {
	codexDir string
}

func NewCodexIntegration(codexDir string) Integration {
	return codexIntegration{codexDir: codexDir}
}

func (codexIntegration) ProviderID() string {
	return ProviderCodex
}

func (codexIntegration) SourceIDs() []string {
	return []string{SourceCodexSessionJSONL}
}

func (codexIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{
		Basis:               PricingBasis,
		SourceURL:           PricingSourceURL,
		VerifiedAt:          PricingVerifiedAt,
		HistoricalRepricing: false,
	}
}

func (integration codexIntegration) Sync(ctx context.Context, stores store.Factory) (UsageSyncResult, error) {
	db, err := stores.OpenHealthy(ctx, false)
	if err != nil {
		return UsageSyncResult{}, err
	}
	defer db.Close()

	source, err := beginCodexUsageSync(ctx, db)
	if err != nil {
		return UsageSyncResult{}, err
	}
	files, err := ListCodexSessionFilesContext(ctx, integration.codexDir)
	if err != nil {
		return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to list Codex session files", err)
	}
	result := UsageSyncResult{ProviderID: ProviderCodex, Source: SourceCodexSessionJSONL}
	discoveredFileKeys := make([]store.UsageKey, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", err)
		}
		discoveredFileKeys = append(discoveredFileKeys, file.SourceKey)
		result.ScannedFiles++

		cursor, hasCursor, err := codexUsageCursor(ctx, db, source.ID, file.SourceKey)
		if err != nil {
			return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to inspect usage import progress", err)
		}
		if hasCursor && cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes &&
			cursor.ParserRevision == CodexUsageParserRevision && cursor.IdentityRevision == CodexUsageIdentityRevision {
			result.SkippedUnchangedFiles++
			continue
		}

		parsed, err := ParseCodexSessionFileContext(ctx, file)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", ctxErr)
			}
			result.Errors = append(result.Errors, UsageImportError{
				SourceKey: file.SourceKey.String(),
				FileName:  filepath.Base(file.Path),
				Message:   sanitizedUsageImportError(err),
			})
			continue
		}

		eventsToStore, safe := codexEventsAfterCursor(parsed.Events, cursor, hasCursor, file)
		if !safe {
			result.Errors = append(result.Errors, UsageImportError{
				SourceKey: file.SourceKey.String(),
				FileName:  filepath.Base(file.Path),
				Message:   codexHistoryChangedMessage,
			})
			continue
		}
		desired := store.CodexUsageImportFile{
			SourceID:         source.ID,
			FileKey:          file.SourceKey,
			ModifiedUnixMS:   file.ModifiedUnixMS,
			SizeBytes:        file.SizeBytes,
			ImportedFacts:    int64(len(parsed.Events)),
			InvalidLines:     parsed.InvalidLines,
			UnsupportedLines: parsed.UnsupportedLines,
			ParserRevision:   CodexUsageParserRevision,
			IdentityRevision: CodexUsageIdentityRevision,
			EventDigest:      EventDigest(parsed.Events, int64(len(parsed.Events))),
		}
		var expected *store.CodexUsageImportFile
		if hasCursor {
			expected = &cursor
		}
		insertResult, err := db.CommitCodexUsageImport(ctx, store.CommitCodexUsageImportParams{
			Generation: source.SyncGeneration,
			Facts:      usageEventsToFactParams(source.ID, eventsToStore),
			File:       desired,
			Expected:   expected,
		})
		if errors.Is(err, store.ErrUsageCursorConflict) {
			current, readErr := db.GetCodexUsageImportFile(ctx, source.ID, file.SourceKey)
			if readErr == nil && sameCodexUsageImportProgress(current, desired) {
				result.SkippedDuplicateEvents += int64(len(eventsToStore))
				result.InvalidLines += parsed.InvalidLines
				result.UnsupportedLines += parsed.UnsupportedLines
				continue
			}
			if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
				return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to inspect concurrent usage sync", readErr)
			}
			return UsageSyncResult{}, err
		}
		if err != nil {
			return UsageSyncResult{}, err
		}
		result.ImportedEvents += int64(insertResult.Inserted)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += parsed.InvalidLines
		result.UnsupportedLines += parsed.UnsupportedLines
	}

	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID:          source.ID,
		Generation:        source.SyncGeneration,
		CompletedAtUnixMS: time.Now().UnixMilli(),
		Finalization: &store.CodexUsageSyncFinalization{
			DiscoveredFileKeys: discoveredFileKeys,
		},
	}); err != nil {
		return UsageSyncResult{}, err
	}
	if err := backfillPartialUsageCosts(ctx, db); err != nil {
		return UsageSyncResult{}, apperror.Wrap(apperror.UsageImportFailed, "failed to update usage pricing", err)
	}
	return result, nil
}

func beginCodexUsageSync(ctx context.Context, db *store.Store) (store.UsageSource, error) {
	var source store.UsageSource
	err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		var err error
		source, err = txStore.BeginUsageSync(ctx, ProviderCodex, SourceCodexSessionJSONL, CodexUsageIdentityRevision)
		if err != nil {
			return err
		}
		// A stale identity checkpoint must roll back the generation advance so
		// runtime sync cannot reinterpret already persisted event identities.
		return txStore.ValidateCodexUsageImportIdentity(ctx, source.ID, CodexUsageIdentityRevision)
	})
	return source, err
}

func codexUsageCursor(ctx context.Context, db *store.Store, sourceID int64, fileKey store.UsageKey) (store.CodexUsageImportFile, bool, error) {
	cursor, err := db.GetCodexUsageImportFile(ctx, sourceID, fileKey)
	if errors.Is(err, store.ErrNotFound) {
		return store.CodexUsageImportFile{}, false, nil
	}
	return cursor, err == nil, err
}

func codexEventsAfterCursor(events []Event, cursor store.CodexUsageImportFile, hasCursor bool, file SourceFile) ([]Event, bool) {
	if !hasCursor {
		return events, true
	}
	// A shorter or rewritten append-only file is ambiguous, so retain its
	// checkpoint and facts unchanged instead of reinterpreting prior history.
	if cursor.IdentityRevision != CodexUsageIdentityRevision || cursor.ImportedFacts < 0 ||
		cursor.ImportedFacts > int64(len(events)) || file.SizeBytes < cursor.SizeBytes {
		return nil, false
	}
	if EventDigest(events, cursor.ImportedFacts) != cursor.EventDigest {
		return nil, false
	}
	return events[cursor.ImportedFacts:], true
}

func sameCodexUsageImportProgress(left, right store.CodexUsageImportFile) bool {
	return left.SourceID == right.SourceID && left.FileKey == right.FileKey &&
		left.ModifiedUnixMS == right.ModifiedUnixMS && left.SizeBytes == right.SizeBytes &&
		left.ImportedFacts == right.ImportedFacts && left.InvalidLines == right.InvalidLines &&
		left.UnsupportedLines == right.UnsupportedLines && left.ParserRevision == right.ParserRevision &&
		left.IdentityRevision == right.IdentityRevision && left.EventDigest == right.EventDigest
}

func usageEventsToFactParams(sourceID int64, events []Event) []store.CreateUsageFactParams {
	facts := make([]store.CreateUsageFactParams, 0, len(events))
	for _, event := range events {
		facts = append(facts, store.CreateUsageFactParams{
			EventKey:            event.EventKey,
			SourceID:            sourceID,
			SessionKey:          event.SessionID,
			ModelKey:            event.Model,
			OccurredAtUnixMS:    event.OccurredAtUnixMS,
			InputTokens:         event.InputTokens,
			CachedInputTokens:   event.CachedInputTokens,
			OutputTokens:        event.OutputTokens,
			TotalTokens:         event.TotalTokens,
			EstimatedCostMicros: event.EstimatedCostMicros,
			CostStatus:          event.CostStatus,
		})
	}
	return facts
}

func backfillPartialUsageCosts(ctx context.Context, db *store.Store) error {
	const batchSize = 256
	models, err := db.ListUnknownUsageCostModels(ctx, ProviderCodex)
	if err != nil {
		return err
	}
	for _, model := range models {
		if _, supported := staticPrices[pricingModelID(model.Model)]; !supported {
			continue
		}
		var afterID int64
		for {
			candidates, err := db.ListUnknownUsageFactCostCandidates(
				ctx,
				ProviderCodex,
				model.SourceID,
				model.ModelID,
				afterID,
				batchSize,
			)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				break
			}
			updates := make([]store.UpdateUsageFactCostParams, 0, len(candidates))
			for _, candidate := range candidates {
				cost, status := EstimateCostMicros(model.Model, TokenCounts{
					InputTokens:       candidate.InputTokens,
					CachedInputTokens: candidate.CachedInputTokens,
					OutputTokens:      candidate.OutputTokens,
					TotalTokens:       candidate.TotalTokens,
				})
				if cost == nil || status == CostStatusUnknown {
					continue
				}
				updates = append(updates, store.UpdateUsageFactCostParams{
					ID:                  candidate.ID,
					EstimatedCostMicros: *cost,
					CostStatus:          status,
				})
			}
			// Only unknown facts are eligible, so concurrent import/backfill runs are
			// idempotent and never overwrite an already classified historical fact.
			if _, err := db.UpdateUnknownUsageFactCosts(ctx, ProviderCodex, updates); err != nil {
				return err
			}
			afterID = candidates[len(candidates)-1].ID
			if len(candidates) < batchSize {
				break
			}
		}
	}
	return nil
}

func sanitizedUsageImportError(err error) string {
	if err == nil {
		return ""
	}
	return "Codex session file could not be read"
}
