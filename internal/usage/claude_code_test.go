package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
)

func claudeUsageLine(session, request string, final bool, output int64) []byte {
	var stop any
	if final {
		stop = "end_turn"
	}
	usage := map[string]any{"input_tokens": 70, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 10, "output_tokens": output, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 10}}
	data, _ := json.Marshal(map[string]any{"type": "assistant", "sessionId": session, "requestId": request, "timestamp": "2026-09-24T12:00:00Z", "message": map[string]any{"id": "message-" + request, "model": "claude-opus-5-5", "stop_reason": stop, "usage": usage}})
	return append(data, '\n')
}

func claudeZeroedHistoryLine(t *testing.T, retainedDetails bool) []byte {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(claudeUsageLine("fork", "request", true, 30), &record); err != nil {
		t.Fatal(err)
	}
	usage := record["message"].(map[string]any)["usage"].(map[string]any)
	if retainedDetails {
		iteration := maps.Clone(usage)
		iteration["type"] = "message"
		usage["iterations"] = []any{iteration}
	} else {
		delete(usage, "cache_creation")
	}
	for _, field := range []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "output_tokens"} {
		usage[field] = 0
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return append(line, '\n')
}

func importClaudeCodeTestFile(t *testing.T, ctx context.Context, db *store.Store, source store.UsageSource, file SourceFile, revision int64, price store.ClaudeCodeUsagePricer) store.ClaudeCodeUsageImportFile {
	t.Helper()
	parsed, err := parseClaudeCodeCheckpointFile(ctx, file, 0, store.UsageKey{}, store.UsageKey{}, nil, nil, revision)
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(parsed.ClaudeInvalidReasons)
	if err != nil {
		t.Fatal(err)
	}
	cursor := store.ClaudeCodeUsageImportFile{
		SourceID: source.ID, FileKey: file.SourceKey,
		ModifiedUnixMS: file.ModifiedUnixMS, SizeBytes: file.SizeBytes,
		ImportedFacts: int64(len(parsed.Events)), InvalidLines: parsed.InvalidLines, UnsupportedLines: parsed.UnsupportedLines,
		ParserRevision: revision, IdentityRevision: ClaudeCodeUsageIdentityRevision,
		EventDigest: parsed.CheckpointEventDigest, CheckpointRevision: usageCheckpointRevision,
		ProcessedBytes: parsed.ProcessedBytes, MetadataDigest: file.MetadataDigest,
		FileIdentityDigest: file.FileIdentityDigest, BoundaryDigest: parsed.BoundaryDigest,
		CheckpointEventDigest: parsed.CheckpointEventDigest, ParserStateJSON: string(state),
	}
	if _, err := db.CommitClaudeCodeUsageImport(ctx, store.CommitClaudeCodeUsageImportParams{
		ProviderID: "claude-code", Generation: source.SyncGeneration,
		Facts: usageEventsToFactParams(source.ID, parsed.Events), File: cursor, Price: price,
	}); err != nil {
		t.Fatal(err)
	}
	cursor, err = db.GetClaudeCodeUsageImportFile(ctx, source.ID, file.SourceKey)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func TestClaudeCodeParserValidatesCandidatesWithoutUsingEarlyOutput(t *testing.T) {
	line := claudeUsageLine("parent", "request", false, 1)
	event, invalid, unsupported, err := parseClaudeCodeSessionLine(line)
	if err != nil || invalid || unsupported || event == nil || event.OutputTokens != 0 || event.TotalTokens != 100 || event.TokenStatus != store.UsageTokensPartial {
		t.Fatalf("partial: %+v, %t, %t, %v", event, invalid, unsupported, err)
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatal(err)
	}
	message := record["message"].(map[string]any)
	u := message["usage"].(map[string]any)
	message["stop_reason"] = "end_turn"
	u["cache_creation_input_tokens"] = 0
	bad, _ := json.Marshal(record)
	if event, reason, _, err := parseClaudeCodeSessionObservation(bad); err != nil || reason != store.ClaudeCodeCacheMismatch || event != nil {
		t.Fatalf("inconsistent cache accepted or misclassified: %+v, %s, %v", event, reason, err)
	}
	u["cache_creation_input_tokens"] = 10
	u["iterations"] = []any{map[string]any{"type": "message", "input_tokens": 70, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 10, "output_tokens": 99}}
	bad, _ = json.Marshal(record)
	if event, reason, _, err := parseClaudeCodeSessionObservation(bad); err != nil || reason != store.ClaudeCodeIterationMismatch || event != nil {
		t.Fatalf("inconsistent iteration accepted or misclassified: %+v, %s, %v", event, reason, err)
	}
	delete(u, "iterations")
	delete(record, "requestId")
	bad, _ = json.Marshal(record)
	if event, _, unsupported, err := parseClaudeCodeSessionLine(bad); err != nil || !unsupported || event != nil {
		t.Fatalf("missing identity accepted: %+v, %t, %v", event, unsupported, err)
	}
	cached := bytes.Replace(claudeUsageLine("session", "cached", true, 30), []byte(`"input_tokens":70`), []byte(`"input_tokens":0`), 1)
	cached = bytes.Replace(cached, []byte(`"end_turn"`), []byte(`"model_context_window_exceeded"`), 1)
	event, reason, unsupported, err := parseClaudeCodeSessionObservation(cached)
	if err != nil || reason != "" || unsupported || event == nil {
		t.Fatalf("cached context-limit response rejected: %+v, %s, %t, %v", event, reason, unsupported, err)
	}
	if event.TokenStatus != store.UsageTokensComplete || event.InputTokens != 30 || event.OutputTokens != 30 {
		t.Fatalf("cached context-limit response lost usage: %+v", event)
	}
	for _, retainedDetails := range []bool{false, true} {
		event, reason, unsupported, err := parseClaudeCodeSessionObservation(claudeZeroedHistoryLine(t, retainedDetails))
		if err != nil || reason != "" || unsupported || event != nil {
			t.Fatalf("zeroed history became a usage candidate: %+v, %s, %t, %v", event, reason, unsupported, err)
		}
	}
}

type claudeTestProvisioner struct{}

func (claudeTestProvisioner) Ensure(ctx context.Context, db *store.Store, mode SyncProvisionMode) error {
	if mode == SyncExistingProvider {
		_, err := db.GetProvider(ctx, "claude-code")
		return err
	}
	_, _, err := db.CreateProviderIfMissing(ctx, store.CreateProviderParams{ID: "claude-code", Name: "Claude Code", AdapterID: "claude-code-official-oauth", MetadataJSON: "{}"})
	return err
}

func TestClaudeCodeSyncCompletesAcrossRestartWithFrozenPricingAndRetractsConflict(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "parent.jsonl")
	if err := os.WriteFile(filepath.Join(project, "a-history.jsonl"), claudeZeroedHistoryLine(t, false), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, claudeUsageLine("parent", "request", false, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	first, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncProvisionProvider})
	if err != nil || first.Result.ImportedEvents != 1 {
		t.Fatalf("initial: %+v, %v", first, err)
	}
	registry, err := NewRegistry(integration)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(factory, registry)
	summary, err := service.Summary(ctx, UsageSummaryRequest{ProviderID: "claude-code"})
	if err != nil || summary.KnownEstimatedCostUSD != "0.000364" {
		t.Fatalf("partial cost subtotal: %+v, %v", summary, err)
	}
	report, err := service.Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Summary.TotalTokens != 100 || report.Summary.OutputTokensStatus != "unknown" || report.Summary.IncompleteEventCount != 1 || report.Summary.MissingOutputCostEventCount != 1 || report.Summary.MissingCacheTTLEventCount != 0 || report.Summary.MissingCacheRateEventCount != 0 || report.Summary.KnownEstimatedCostUSD != "0.000364" {
		t.Fatalf("partial report: %+v, %v", report.Summary, err)
	}
	catalog := pricing.Embedded()
	catalog.CatalogVersion++
	for i := range catalog.Entries {
		if catalog.Entries[i].Model == "claude-opus-5-5" {
			catalog.Entries[i].ShortContext.Output = "99"
		}
	}
	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendFile.Write(claudeUsageLine("parent", "request", true, 30)); err != nil {
		t.Fatal(err)
	}
	if err := appendFile.Close(); err != nil {
		t.Fatal(err)
	}
	integration = NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	completed, err := integration.Sync(withPricingSnapshot(ctx, catalog), factory, SyncOptions{ProvisionMode: SyncExistingProvider})
	if err != nil || completed.Result.UpdatedEvents != 1 {
		t.Fatalf("complete: %+v, %v", completed, err)
	}
	report, err = service.Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Summary.TotalTokens != 130 || report.Summary.TokenTotalStatus != "complete" || report.Summary.MissingOutputCostEventCount != 0 || report.Summary.KnownEstimatedCostUSD != "0.000964" {
		t.Fatalf("frozen estimate: %+v, %v", report.Summary, err)
	}
	childDir := filepath.Join(project, "fork", "subagents")
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "agent-copy.jsonl"), claudeUsageLine("fork", "request", true, 30), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "agent-zeroed.jsonl"), claudeZeroedHistoryLine(t, true), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	report, err = service.Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Summary.EventCount != 1 || report.Summary.TotalTokens != 130 {
		t.Fatalf("fork counted twice: %+v, %v", report.Summary, err)
	}
	if report.Import.InvalidLines != 0 || report.Import.UnsupportedLines != 0 || report.Summary.ConflictingEventCount != 0 {
		t.Fatalf("history copies caused a quality warning: %+v, %+v", report.Import, report.Summary)
	}
	if err := os.WriteFile(filepath.Join(project, "conflict.jsonl"), claudeUsageLine("other", "request", true, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	report, err = service.Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Summary.TotalTokens != 0 || report.Summary.ConflictingEventCount != 1 || report.Summary.TokenTotalStatus != "unknown" || report.Summary.KnownEstimatedCostUSD != "0.000000" {
		t.Fatalf("conflict not retracted: %+v, %v", report.Summary, err)
	}
}

func TestClaudeCodePricingDistinguishesCacheDurationAndUnknownTTL(t *testing.T) {
	catalog := pricing.Embedded()
	for _, test := range []struct {
		ttl    string
		want   int64
		status store.UsageCostStatus
	}{{"1h", 364, CostStatusEstimated}, {"5m", 334, CostStatusEstimated}, {"missing", 284, CostStatusPartial}} {
		event, _, _, err := parseClaudeCodeSessionLine(claudeUsageLine("session", "request", true, 0))
		if err != nil {
			t.Fatal(err)
		}
		fact := usageEventsToFactParams(1, []Event{*event})[0]
		if test.ttl == "5m" {
			fact.CacheWrite5mTokens = fact.CacheCreationInputTokens
			zero := int64(0)
			fact.CacheWrite1hTokens = &zero
		}
		if test.ttl == "missing" {
			fact.CacheWrite5mTokens = nil
			fact.CacheWrite1hTokens = nil
		}
		cost, status, _, _ := claudeCodePricer(catalog)(fact)
		if cost == nil || *cost != test.want || status != test.status {
			t.Fatalf("%s: %v, %v", test.ttl, cost, status)
		}
	}
}

func TestClaudeCodeCostBackfillCannotBorrowANewerSyncGeneration(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := beginClaudeCodeUsageSync(ctx, db, claudeTestProvisioner{}, SyncProvisionProvider)
	if err != nil {
		t.Fatal(err)
	}
	event, _, _, err := parseClaudeCodeSessionLine(claudeUsageLine("session", "request", true, 30))
	if err != nil || event == nil {
		t.Fatalf("parse fixture: %+v, %v", event, err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "session.jsonl"), claudeUsageLine("session", "request", true, 30), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := ListClaudeCodeSessionFilesContext(ctx, dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("discover fixture: %d files, %v", len(files), err)
	}
	importClaudeCodeTestFile(t, ctx, db, source, files[0], ClaudeCodeUsageParserRevision, nil)
	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID: source.ID, Generation: source.SyncGeneration, CompletedAtUnixMS: event.OccurredAtUnixMS,
		Finalization: &store.ClaudeCodeUsageSyncFinalization{ProviderID: "claude-code", DiscoveredFileKeys: []store.UsageKey{files[0].SourceKey}},
	}); err != nil {
		t.Fatal(err)
	}
	newSource, err := db.BeginUsageSync(ctx, "claude-code", SourceClaudeCodeSessionJSONL, ClaudeCodeUsageIdentityRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err := backfillClaudeCodeUsageCosts(ctx, db, source.ID, source.SyncGeneration); !errors.Is(err, store.ErrUsageSyncSuperseded) {
		t.Fatalf("superseded backfill wrote with a newer generation: %v", err)
	}
	summary, err := db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.UnknownCostEvents != 1 || summary.EstimatedCostMicros != 0 {
		t.Fatalf("superseded backfill changed costs: %+v, %v", summary, err)
	}
	catalog := pricing.Embedded()
	catalog.CatalogVersion++
	for i := range catalog.Entries {
		if catalog.Entries[i].Model == "claude-opus-5-5" {
			catalog.Entries[i].ShortContext.Output = "99"
		}
	}
	if err := backfillClaudeCodeUsageCosts(withPricingSnapshot(ctx, catalog), db, newSource.ID, newSource.SyncGeneration); err != nil {
		t.Fatal(err)
	}
	summary, err = db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.EstimatedCostMicros != 3334 || summary.UnknownCostEvents != 0 {
		t.Fatalf("current backfill did not retain its own prices: %+v, %v", summary, err)
	}
}

func TestClaudeCodeSyncDoesNotBackfillIneligibleUsageInAPriceablePeriod(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	content := bytes.Replace(claudeUsageLine("session", "before-prices", true, 30), []byte("2026-09-24"), []byte("2026-09-21"), 1)
	for _, field := range []string{`"speed":"fast"`, `"inference_geo":"us"`, `"server_tool_use":{"web_search_requests":1}`} {
		line := claudeUsageLine("session", field, true, 30)
		line = bytes.Replace(line, []byte(`"input_tokens":70`), []byte(field+`,"input_tokens":70`), 1)
		content = append(content, line...)
	}
	if err := os.WriteFile(filepath.Join(project, "session.jsonl"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncProvisionProvider}); err != nil {
		t.Fatal(err)
	}
	source, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
	if err != nil {
		t.Fatal(err)
	}
	work := false
	outcome, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider, OnWorkDetected: func() { work = true }})
	if err != nil || outcome.Performed || work {
		t.Fatalf("ineligible usage triggered an empty backfill: %+v, work=%t, %v", outcome, work, err)
	}
	current, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
	if err != nil || current.SyncGeneration != source.SyncGeneration {
		t.Fatalf("empty backfill advanced the source generation: %+v, %v", current, err)
	}
	summary, err := db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.EventCount != 4 || summary.UnknownCostEvents != 4 {
		t.Fatalf("unknown usage was lost or estimated: %+v, %v", summary, err)
	}
}

func TestClaudeCodeParserUpgradeVerifiesOldHistoryAndCompletesSupportedResponses(t *testing.T) {
	for _, rewritten := range []bool{false, true} {
		name := "unchanged"
		if rewritten {
			name = "rewritten"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
			db, err := factory.Open(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			project := filepath.Join(dir, "projects", "project")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(project, "session.jsonl")
			content := claudeUsageLine("session", "request", false, 1)
			contextLimit := bytes.Replace(claudeUsageLine("session", "request", true, 30), []byte(`"end_turn"`), []byte(`"model_context_window_exceeded"`), 1)
			content = append(content, contextLimit...)
			content = append(content, claudeZeroedHistoryLine(t, true)...)
			content = append(content, claudeUsageLine("session", "next", true, 30)...)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			source, err := beginClaudeCodeUsageSync(ctx, db, claudeTestProvisioner{}, SyncProvisionProvider)
			if err != nil {
				t.Fatal(err)
			}
			files, err := ListClaudeCodeSessionFilesContext(ctx, dir)
			if err != nil || len(files) != 1 {
				t.Fatalf("discover fixture: %d files, %v", len(files), err)
			}
			cursor := importClaudeCodeTestFile(t, ctx, db, source, files[0], 2, claudeCodePricer(pricing.Embedded()))
			if cursor.InvalidLines != 1 || cursor.UnsupportedLines != 1 || cursor.ImportedFacts != 2 {
				t.Fatalf("legacy fixture did not reproduce rejected history: %+v", cursor)
			}
			if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
				SourceID: source.ID, Generation: source.SyncGeneration, CompletedAtUnixMS: 1,
				Finalization: &store.ClaudeCodeUsageSyncFinalization{ProviderID: "claude-code", DiscoveredFileKeys: []store.UsageKey{files[0].SourceKey}},
			}); err != nil {
				t.Fatal(err)
			}
			if rewritten {
				content = bytes.Replace(content, []byte(`"input_tokens":70`), []byte(`"input_tokens":71`), 1)
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
			outcome, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
			if err != nil {
				t.Fatal(err)
			}
			current, err := db.GetClaudeCodeUsageImportFile(ctx, source.ID, files[0].SourceKey)
			if err != nil {
				t.Fatal(err)
			}
			summary, err := db.UsageSummary(ctx, "claude-code")
			if err != nil {
				t.Fatal(err)
			}
			if rewritten {
				if len(outcome.Result.Errors) != 1 || outcome.Result.UpdatedEvents != 0 || !sameClaudeCodeUsageImportProgress(current, cursor) {
					t.Fatalf("upgrade accepted rewritten history: %+v, %+v", outcome, current)
				}
				if summary.TotalTokens != 230 || summary.EstimatedCostMicros != 1328 || summary.IncompleteEventCount != 1 {
					t.Fatalf("rewritten history changed accepted usage: %+v", summary)
				}
				return
			}
			if len(outcome.Result.Errors) != 0 || outcome.Result.UpdatedEvents != 1 || current.ParserRevision != ClaudeCodeUsageParserRevision {
				t.Fatalf("upgrade failed to complete supported usage: %+v, %+v", outcome, current)
			}
			if current.InvalidLines != 0 || current.UnsupportedLines != 0 || current.ImportedFacts != 3 {
				t.Fatalf("upgrade retained obsolete warnings: %+v", current)
			}
			if summary.TotalTokens != 260 || summary.EstimatedCostMicros != 1928 || summary.IncompleteEventCount != 0 || summary.ConflictingEventCount != 0 {
				t.Fatalf("upgraded totals: %+v", summary)
			}
			noWork, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
			if err != nil || noWork.Performed {
				t.Fatalf("upgraded checkpoint was not stable: %+v, %v", noWork, err)
			}
		})
	}
}

func TestClaudeCodeSyncRetainsAcceptedHistoryAndWaitsForTail(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	partial := claudeUsageLine("session", "request", false, 1)
	final := claudeUsageLine("session", "request", true, 30)
	malformed := []byte("{broken-json}\n")
	invalid := append(malformed, bytes.Replace(final, []byte(`"input_tokens":70`), []byte(`"input_tokens":"invalid"`), 1)...)
	tailAt := len(final) / 2
	prefix := append(append(append([]byte{}, partial...), invalid...), final[:tailAt]...)
	if err := os.WriteFile(path, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	first, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncProvisionProvider})
	if err != nil || first.Result.InvalidLines != 2 || first.Result.ImportedEvents != 1 {
		t.Fatalf("initial: %+v, %v", first, err)
	}
	source, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
	if err != nil {
		t.Fatal(err)
	}
	key, err := SourceKey(path)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := db.GetClaudeCodeUsageImportFile(ctx, source.ID, key)
	if err != nil || cursor.ProcessedBytes != int64(len(partial)+len(invalid)) {
		t.Fatalf("tail advanced: %+v, %v", cursor, err)
	}
	reasons, err := store.DecodeClaudeCodeUsageInvalidReasons(cursor.ParserStateJSON, cursor.InvalidLines)
	if err != nil || reasons[store.ClaudeCodeInvalidJSON] != 1 || reasons[store.ClaudeCodeInvalidFields] != 1 {
		t.Fatalf("invalid reasons: %+v, %v", reasons, err)
	}
	if err := os.WriteFile(path, append(prefix, final[tailAt:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	completed, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
	if err != nil || completed.Result.UpdatedEvents != 1 {
		t.Fatalf("completion: %+v, %v", completed, err)
	}
	cursor, err = db.GetClaudeCodeUsageImportFile(ctx, source.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(integration)
	if err != nil {
		t.Fatal(err)
	}
	report, err := NewService(factory, registry).Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Import.InvalidReasons[store.ClaudeCodeInvalidJSON] != 1 || report.Import.InvalidReasons[store.ClaudeCodeInvalidFields] != 1 {
		t.Fatalf("reported reasons after append: %+v, %v", report.Import, err)
	}
	integrity, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
	if err != nil || len(integrity.Issues) != 0 {
		t.Fatalf("diagnostic integrity: %+v, %v", integrity, err)
	}
	work := false
	noWork, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider, OnWorkDetected: func() { work = true }})
	if err != nil || noWork.Performed || work {
		t.Fatalf("unchanged round: %+v, work=%t, %v", noWork, work, err)
	}
	for _, content := range [][]byte{
		bytes.Replace(append(append(append([]byte{}, partial...), invalid...), final...), []byte(`"input_tokens":70`), []byte(`"input_tokens":71`), 1),
		partial,
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		changed, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
		if err != nil || len(changed.Result.Errors) != 1 {
			t.Fatalf("changed history accepted: %+v, %v", changed, err)
		}
		accepted, err := db.GetClaudeCodeUsageImportFile(ctx, source.ID, key)
		if err != nil || !sameClaudeCodeUsageImportProgress(accepted, cursor) {
			t.Fatalf("checkpoint changed: %+v, %v", accepted, err)
		}
		summary, err := db.UsageSummary(ctx, "claude-code")
		if err != nil || summary.TotalTokens != 130 || summary.IncompleteEventCount != 0 {
			t.Fatalf("accepted facts changed: %+v, %v", summary, err)
		}
	}
}

func TestClaudeCodeReportSeparatesMissingOutputCacheDurationAndRates(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	if err := os.WriteFile(filepath.Join(project, "partial.jsonl"), claudeUsageLine("session", "partial", false, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncProvisionProvider}); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(claudeUsageLine("session", "ttl", true, 30), &record); err != nil {
		t.Fatal(err)
	}
	delete(record["message"].(map[string]any)["usage"].(map[string]any), "cache_creation")
	missingTTL, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "ttl.jsonl"), append(missingTTL, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "rate.jsonl"), claudeUsageLine("session", "rate", true, 30), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := pricing.Embedded()
	for i := range catalog.Entries {
		catalog.Entries[i].ShortContext.CacheWrite1h = nil
	}
	if _, err := integration.Sync(withPricingSnapshot(ctx, catalog), factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(integration)
	if err != nil {
		t.Fatal(err)
	}
	report, err := NewService(factory, registry).Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil {
		t.Fatal(err)
	}
	summaries := []UsageAggregateSummary{report.Summary, report.Models[0].Summary}
	for _, point := range report.Trend {
		if point.Summary.EventCount > 0 {
			summaries = append(summaries, point.Summary)
		}
	}
	for _, summary := range summaries {
		if summary.PartialCostEventCount != 3 || summary.MissingOutputCostEventCount != 1 || summary.MissingCacheTTLEventCount != 1 || summary.MissingCacheRateEventCount != 1 {
			t.Fatalf("cost causes: %+v", summary)
		}
	}
}

func TestClaudeCodeDiscoveryExcludesSymlinksAndNestedNonSessionLogs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	child := filepath.Join(project, "session", "subagents")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(project, "session.jsonl"), filepath.Join(child, "agent-worker.jsonl"), filepath.Join(child, "other.jsonl")} {
		if err := os.WriteFile(path, claudeUsageLine("session", "request", true, 30), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(project, "session.jsonl"), filepath.Join(project, "copy.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, filepath.Join(dir, "projects", "linked")); err != nil {
		t.Fatal(err)
	}
	files, err := ListClaudeCodeSessionFilesContext(ctx, dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("discovery: %d, %v", len(files), err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ListClaudeCodeSessionFilesContext(canceled, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestClaudeCodePartialConflictRetainsFirstPricingForValidCompletion(t *testing.T) {
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	partial := claudeUsageLine("session", "request", false, 1)
	if err := os.WriteFile(path, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncProvisionProvider}); err != nil {
		t.Fatal(err)
	}
	contradictory := bytes.Replace(partial, []byte(`"input_tokens":70`), []byte(`"input_tokens":71`), 1)
	if err := os.WriteFile(path, append(append([]byte{}, partial...), contradictory...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	summary, err := db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.TotalTokens != 0 || summary.ConflictingEventCount != 1 {
		t.Fatalf("partial conflict: %+v, %v", summary, err)
	}
	integrity, err := db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
	if err != nil || len(integrity.Issues) != 0 {
		t.Fatalf("retained snapshot integrity: %+v, %v", integrity, err)
	}
	catalog := pricing.Embedded()
	catalog.CatalogVersion++
	for i := range catalog.Entries {
		if catalog.Entries[i].Model == "claude-opus-5-5" {
			catalog.Entries[i].ShortContext.Output = "99"
		}
	}
	content := append(append(append([]byte{}, partial...), contradictory...), claudeUsageLine("session", "request", true, 30)...)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Sync(withPricingSnapshot(ctx, catalog), factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	summary, err = db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.TotalTokens != 130 || summary.EstimatedCostMicros != 964 {
		t.Fatalf("pricing changed during conflict: %+v, %v", summary, err)
	}
}
