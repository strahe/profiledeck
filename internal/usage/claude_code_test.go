package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if _, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider}); err != nil {
		t.Fatal(err)
	}
	report, err = service.Report(ctx, UsageReportRequest{ProviderID: "claude-code", Range: UsageRangeAll})
	if err != nil || report.Summary.EventCount != 1 || report.Summary.TotalTokens != 130 {
		t.Fatalf("fork counted twice: %+v, %v", report.Summary, err)
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
