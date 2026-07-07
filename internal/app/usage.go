package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/usage"
)

type UsageSyncCodexRequest struct {
	ConfigDir string
	CodexDir  string
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
	ConfigDir  string
	ProviderID string
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

func UsageSyncCodex(ctx context.Context, req UsageSyncCodexRequest) (UsageSyncResult, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return UsageSyncResult{}, err
	}
	defer db.Close()

	files, err := usage.ListCodexSessionFiles(req.CodexDir)
	if err != nil {
		return UsageSyncResult{}, WrapError(ErrorUsageImportFailed, "failed to list Codex session files", err)
	}

	result := UsageSyncResult{
		ProviderID: usage.ProviderCodex,
		Source:     usage.SourceCodexSessionJSONL,
	}
	for _, file := range files {
		result.ScannedFiles++
		cursor, hasCursor, unchanged, err := usageSourceCursor(ctx, db, file)
		if err != nil {
			return UsageSyncResult{}, WrapError(ErrorUsageImportFailed, "failed to inspect usage import cursor", err)
		}
		if unchanged {
			result.SkippedUnchangedFiles++
			continue
		}

		parsed, err := usage.ParseCodexSessionFile(file)
		if err != nil {
			result.Errors = append(result.Errors, UsageImportError{
				SourceKey: file.SourceKey,
				FileName:  filepath.Base(file.Path),
				Message:   sanitizedUsageImportError(err),
			})
			continue
		}

		eventsToStore := usageEventsAfterCursor(parsed.Events, cursor, hasCursor, file)
		insertResult, err := db.InsertUsageEvents(ctx, usageEventsToStoreParams(eventsToStore))
		if err != nil {
			return UsageSyncResult{}, WrapError(ErrorUsageImportFailed, "failed to store usage events", err)
		}
		if err := db.UpsertUsageImportCursor(ctx, store.UpsertUsageImportCursorParams{
			ProviderID:       usage.ProviderCodex,
			Source:           usage.SourceCodexSessionJSONL,
			SourceKey:        file.SourceKey,
			ModifiedUnixMS:   file.ModifiedUnixMS,
			SizeBytes:        file.SizeBytes,
			ImportedEvents:   int64(len(parsed.Events)),
			InvalidLines:     parsed.InvalidLines,
			UnsupportedLines: parsed.UnsupportedLines,
			MetadataJSON:     usageCursorMetadataJSON(parsed.Events),
		}); err != nil {
			return UsageSyncResult{}, WrapError(ErrorUsageImportFailed, "failed to update usage import cursor", err)
		}

		result.ImportedEvents += int64(insertResult.Inserted)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += parsed.InvalidLines
		result.UnsupportedLines += parsed.UnsupportedLines
	}

	return result, nil
}

func UsageSummary(ctx context.Context, req UsageSummaryRequest) (UsageSummaryResult, error) {
	providerID, appErr := normalizeUsageProviderID(req.ProviderID)
	if appErr != nil {
		return UsageSummaryResult{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return UsageSummaryResult{}, err
	}
	defer db.Close()

	summary, err := db.UsageSummary(ctx, providerID)
	if err != nil {
		return UsageSummaryResult{}, WrapError(ErrorStoreStatusFailed, "failed to read usage summary", err)
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
		CostStatus:              usage.CostStatusEstimated,
		UnknownCostEventCount:   summary.UnknownCostEvents,
		EstimatedCostEventCount: summary.EstimatedCostEventCount,
	}
	if summary.UnknownCostEvents > 0 {
		result.CostStatus = usage.CostStatusUnknown
		return result, nil
	}

	cost := usage.USDStringFromMicros(summary.EstimatedCostMicros)
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

func usageSourceCursor(ctx context.Context, db *store.Store, file usage.SourceFile) (store.UsageImportCursor, bool, bool, error) {
	cursor, err := db.GetUsageImportCursor(ctx, usage.ProviderCodex, usage.SourceCodexSessionJSONL, file.SourceKey)
	if errors.Is(err, store.ErrNotFound) {
		return store.UsageImportCursor{}, false, false, nil
	}
	if err != nil {
		return store.UsageImportCursor{}, false, false, err
	}
	unchanged := cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes
	return cursor, true, unchanged, nil
}

func usageEventsAfterCursor(events []usage.Event, cursor store.UsageImportCursor, hasCursor bool, file usage.SourceFile) []usage.Event {
	if !hasCursor || cursor.ImportedEvents <= 0 || cursor.ImportedEvents > int64(len(events)) {
		return events
	}
	if file.SizeBytes <= cursor.SizeBytes || !usageCursorPrefixMatches(events, cursor) {
		return events
	}
	return events[cursor.ImportedEvents:]
}

func usageCursorPrefixMatches(events []usage.Event, cursor store.UsageImportCursor) bool {
	var metadata struct {
		EventDigest string `json:"event_digest"`
	}
	if err := json.Unmarshal([]byte(cursor.MetadataJSON), &metadata); err != nil || metadata.EventDigest == "" {
		return false
	}
	return metadata.EventDigest == usage.EventDigest(events, cursor.ImportedEvents)
}

func usageCursorMetadataJSON(events []usage.Event) string {
	raw, err := json.Marshal(map[string]string{
		"parser_version": usage.CodexSessionParserVersion,
		"event_digest":   usage.EventDigest(events, int64(len(events))),
	})
	if err != nil {
		return `{"parser_version":"` + usage.CodexSessionParserVersion + `"}`
	}
	return string(raw)
}

func usageEventsToStoreParams(events []usage.Event) []store.CreateUsageEventParams {
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

func normalizeUsageProviderID(providerID string) (string, *AppError) {
	if providerID == "" {
		return usage.ProviderCodex, nil
	}
	id, appErr := validateID(providerID, ErrorUsageInvalid)
	if appErr != nil {
		return "", appErr
	}
	if id != usage.ProviderCodex {
		return "", NewError(ErrorUsageInvalid, "unsupported usage provider")
	}
	return id, nil
}
