package usage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/store"
)

func TestUsageImportErrorMessageExcludesRawCause(t *testing.T) {
	const sentinel = "SECRET_OS_DIAGNOSTIC"
	for _, err := range []error{
		errors.New(sentinel),
		&os.PathError{Op: "open", Path: "/private/" + sentinel, Err: errors.New("permission denied")},
	} {
		message := sanitizedUsageImportError(err)
		if message != "Codex session file could not be read" || strings.Contains(message, sentinel) || strings.Contains(message, "permission denied") {
			t.Fatalf("public usage import message = %q", message)
		}
	}
}

func TestUsageSyncCodexImportsAndSkipsUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}}}`,
	}, "\n"))

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ScannedFiles != 1 || first.ImportedEvents != 2 || first.SkippedDuplicateEvents != 0 || first.SkippedUnchangedFiles != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	second, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected second usage sync to succeed, got %v", err)
	}
	if second.ScannedFiles != 1 || second.ImportedEvents != 0 || second.SkippedUnchangedFiles != 1 {
		t.Fatalf("unexpected second sync result: %#v", second)
	}

	summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 2 || summary.InputTokens != 150 || summary.CachedInputTokens != 25 || summary.OutputTokens != 15 || summary.TotalTokens != 165 {
		t.Fatalf("unexpected summary tokens: %#v", summary)
	}
	if summary.CostStatus != "estimated" || summary.EstimatedCostUSD == nil || summary.UnknownCostEventCount != 0 {
		t.Fatalf("expected estimated cost summary, got %#v", summary)
	}
	if summary.Source != "codex-session-jsonl" || strings.Join(summary.Sources, ",") != "codex-session-jsonl" {
		t.Fatalf("unexpected summary sources: %#v", summary)
	}
}

func TestUsageSyncCodexRefreshesLastSyncedForUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	activePath := writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-freshness"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`,
	}, "\n"))
	archiveDir := filepath.Join(codexDir, "archived_sessions")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		t.Fatalf("expected archive fixture dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "archived.jsonl"), []byte(strings.Join([]string{
		`{"type":"session_meta","session_id":"session-archived"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":3}}}}`,
	}, "\n")), 0o600); err != nil {
		t.Fatalf("expected archive fixture write to succeed, got %v", err)
	}
	initResult, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil {
		t.Fatalf("expected initial sync to succeed, got %v", err)
	}

	rawDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected raw fixture database open, got %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, "UPDATE usage_import_cursors SET updated_at_unix_ms = 1"); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected cursor timestamp fixture update, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("expected raw fixture database close, got %v", err)
	}

	second, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil || second.SkippedUnchangedFiles != 2 {
		t.Fatalf("expected unchanged sync, result=%#v err=%v", second, err)
	}
	rawDB, err = sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected raw fixture database reopen, got %v", err)
	}
	var touched int
	if err := rawDB.QueryRowContext(ctx, "SELECT COUNT(1) FROM usage_import_cursors WHERE updated_at_unix_ms > 1").Scan(&touched); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected cursor freshness query to succeed, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("expected raw fixture database close, got %v", err)
	}
	if touched != 1 {
		t.Fatalf("expected one freshness cursor touch for two unchanged files, got %d", touched)
	}
	report, err := newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{Range: UsageRangeAll})
	if err != nil {
		t.Fatalf("expected usage report after unchanged sync, got %v", err)
	}
	if report.Import.LastSyncedAtUnixMS <= 1 {
		t.Fatalf("expected unchanged sync to refresh last sync time, got %#v", report.Import)
	}

	rawDB, err = sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected raw fixture database reopen, got %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, "UPDATE usage_import_cursors SET updated_at_unix_ms = 1"); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected cursor timestamp reset, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("expected raw fixture database close, got %v", err)
	}
	appendAppUsageFixture(t, activePath, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":4}}}}`)
	third, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil || third.ImportedEvents != 1 || third.SkippedUnchangedFiles != 1 {
		t.Fatalf("expected one changed and one unchanged cursor, result=%#v err=%v", third, err)
	}
	rawDB, err = sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected raw fixture database reopen, got %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, "SELECT COUNT(1) FROM usage_import_cursors WHERE updated_at_unix_ms > 1").Scan(&touched); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected changed cursor timestamp query, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("expected raw fixture database close, got %v", err)
	}
	if touched != 1 {
		t.Fatalf("expected changed cursor commit without an extra freshness touch, got %d writes", touched)
	}
}

func TestUsageSyncCodexImportsOnlyAppendedEvents(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	path := writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}}}`,
	}, "\n"))

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ImportedEvents != 2 || first.SkippedDuplicateEvents != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}
	appendAppUsageFixture(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":28,"output_tokens":18,"total_tokens":198}}}}`)
	second, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected second usage sync to succeed, got %v", err)
	}
	if second.ScannedFiles != 1 || second.ImportedEvents != 1 || second.SkippedDuplicateEvents != 0 || second.SkippedUnchangedFiles != 0 {
		t.Fatalf("expected appended sync to store only one new event, got %#v", second)
	}

	summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 3 || summary.InputTokens != 180 || summary.CachedInputTokens != 28 || summary.OutputTokens != 18 || summary.TotalTokens != 198 {
		t.Fatalf("unexpected summary after append sync: %#v", summary)
	}
}

func TestUsageSyncCodexDoesNotDoubleCountCopiedSessionFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	firstCodexDir := t.TempDir()
	secondCodexDir := t.TempDir()
	content := strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n")
	writeAppUsageFixture(t, firstCodexDir, content)
	writeAppUsageFixture(t, secondCodexDir, content)

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := newUsageTestEnvironment(t, configDir, firstCodexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ImportedEvents != 1 || first.SkippedDuplicateEvents != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	second, err := newUsageTestEnvironment(t, configDir, secondCodexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected copied usage sync to succeed, got %v", err)
	}
	if second.ImportedEvents != 0 || second.SkippedDuplicateEvents != 1 {
		t.Fatalf("expected copied session to dedupe existing event, got %#v", second)
	}

	summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 100 || summary.CachedInputTokens != 20 || summary.OutputTokens != 10 || summary.TotalTokens != 110 {
		t.Fatalf("expected copied session not to double count usage, got %#v", summary)
	}
}

func TestUsageSyncCodexDoesNotDoubleCountMultiLevelForkHistory(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "01", "parent.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-01T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-01T00:01:00Z"}`,
	}, "\n"))
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "02", "child.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"response_item","payload":{"type":"message"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"output_tokens":3,"total_tokens":33}}},"timestamp":"2026-07-02T00:02:00Z"}`,
	}, "\n"))
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "03", "grandchild.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-grandchild","forked_from_id":"session-child"}}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-03T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-03T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"output_tokens":3,"total_tokens":33}}},"timestamp":"2026-07-03T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-grandchild","forked_from_id":"session-child"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":40,"cached_input_tokens":6,"output_tokens":4,"total_tokens":44}}},"timestamp":"2026-07-03T00:03:00Z"}`,
	}, "\n"))

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected fork usage sync, got %v", err)
	}
	if result.ScannedFiles != 3 || result.ImportedEvents != 4 || result.SkippedDuplicateEvents != 5 {
		t.Fatalf("expected only post-fork usage to be added, got %#v", result)
	}
	report, err := newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{Range: UsageRangeAll})
	if err != nil {
		t.Fatalf("expected fork usage report, got %v", err)
	}
	if report.Summary.EventCount != 4 || report.Summary.SessionCount != 3 || report.Summary.InputTokens != 220 ||
		report.Summary.CachedInputTokens != 36 || report.Summary.OutputTokens != 22 || report.Summary.TotalTokens != 242 {
		t.Fatalf("unexpected deduplicated fork summary: %#v", report.Summary)
	}
}

func TestUsageSyncCodexKeepsParentTimestampWhenParentArrivesAfterFork(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	childDir := t.TempDir()
	parentDir := t.TempDir()
	writeAppUsageFixture(t, childDir, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"output_tokens":3,"total_tokens":33}}},"timestamp":"2026-07-02T00:02:00Z"}`,
	}, "\n"))
	writeAppUsageFixture(t, parentDir, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-01T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-01T00:01:00Z"}`,
	}, "\n"))

	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if result, err := newUsageTestEnvironment(t, configDir, childDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 3 {
		t.Fatalf("expected child-first import, result=%#v err=%v", result, err)
	}
	if result, err := newUsageTestEnvironment(t, configDir, parentDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 0 || result.SkippedDuplicateEvents != 2 {
		t.Fatalf("expected later parent import to deduplicate, result=%#v err=%v", result, err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("expected usage database open, got %v", err)
	}
	defer rawDB.Close()
	var earliest int64
	var latest int64
	if err := rawDB.QueryRowContext(ctx, `SELECT MIN(occurred_at_unix_ms), MAX(occurred_at_unix_ms)
		FROM usage_events WHERE session_id = 'session-parent'`).Scan(&earliest, &latest); err != nil {
		t.Fatalf("expected parent timestamp query, got %v", err)
	}
	if earliest != time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || latest != time.Date(2026, 7, 1, 0, 1, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("expected original parent timestamps, earliest=%d latest=%d", earliest, latest)
	}
}

func TestUsageSyncCodexConcurrentRunsRemainIdempotent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	firstCodexDir := t.TempDir()
	secondCodexDir := t.TempDir()
	writeAppUsageFixture(t, firstCodexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-concurrent"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n"))
	writeAppUsageFixture(t, secondCodexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-concurrent"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"response_item","payload":{"type":"message"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n"))
	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	start := make(chan struct{})
	errorsByRun := make(chan error, 2)
	var wg sync.WaitGroup
	for _, codexDir := range []string{firstCodexDir, secondCodexDir} {
		wg.Add(1)
		go func(codexDir string) {
			defer wg.Done()
			<-start
			_, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
			errorsByRun <- err
		}(codexDir)
	}
	close(start)
	wg.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		if err != nil {
			t.Fatalf("expected concurrent sync to succeed, got %v", err)
		}
	}

	summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected concurrent sync summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.TotalTokens != 110 {
		t.Fatalf("expected one idempotent event after concurrent sync, got %#v", summary)
	}
}

func TestUsageSyncCodexBackfillsExistingGPT56PartialCost(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db, err := store.Open(ctx, initialized.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected fixture store open, got %v", err)
	}
	if result, err := db.InsertUsageEvents(ctx, []store.CreateUsageEventParams{{
		ID: "existing-gpt-5.6", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source",
		SessionID: "session", Model: "gpt-5.6-sol", OccurredAtUnixMS: time.Now().UnixMilli(),
		InputTokens: 1_000_000, CachedInputTokens: 100_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000,
		CostStatus: store.UsageCostStatusUnknown,
	}}); err != nil || result.Inserted != 1 {
		_ = db.Close()
		t.Fatalf("expected unknown historical usage fixture, result=%#v err=%v", result, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected fixture store close, got %v", err)
	}

	if _, err := newUsageTestEnvironment(t, configDir, t.TempDir()).service.SyncCodex(ctx); err != nil {
		t.Fatalf("expected sync to backfill GPT-5.6 cost, got %v", err)
	}
	report, err := newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{Range: UsageRangeAll})
	if err != nil {
		t.Fatalf("expected usage report, got %v", err)
	}
	if report.Summary.KnownEstimatedCostUSD != "34.550000" || report.Summary.CostStatus != "partial" ||
		report.Summary.PartialCostEventCount != 1 || report.Summary.UnknownCostEventCount != 0 || report.Summary.PricingCoverage != 1 {
		t.Fatalf("unexpected GPT-5.6 partial pricing summary: %#v", report.Summary)
	}
	if len(report.Models) != 1 || report.Models[0].Model != "gpt-5.6-sol" || report.Models[0].Summary.KnownEstimatedCostUSD != "34.550000" {
		t.Fatalf("unexpected GPT-5.6 model summary: %#v", report.Models)
	}
	if len(report.Trend) != 1 || report.Trend[0].Summary.KnownEstimatedCostUSD != "34.550000" ||
		report.Trend[0].Summary.CostStatus != "partial" || report.Trend[0].Summary.PartialCostEventCount != 1 {
		t.Fatalf("expected GPT-5.6 base cost in the trend bucket, got %#v", report.Trend)
	}
	legacy, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected legacy usage summary, got %v", err)
	}
	if legacy.CostStatus != "unknown" || legacy.EstimatedCostUSD != nil || legacy.UnknownCostEventCount != 1 {
		t.Fatalf("expected legacy summary to preserve its conservative contract, got %#v", legacy)
	}
}

func TestUsageSyncCodexDoesNotDoubleCountSessionMovedToArchive(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	path := writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-archive"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n"))
	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	first, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil || first.ImportedEvents != 1 {
		t.Fatalf("expected initial session import, result=%#v err=%v", first, err)
	}

	archivePath := filepath.Join(codexDir, "archived_sessions", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o700); err != nil {
		t.Fatalf("expected archive directory setup, got %v", err)
	}
	if err := os.Rename(path, archivePath); err != nil {
		t.Fatalf("expected session move to archive, got %v", err)
	}
	second, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected archived session sync, got %v", err)
	}
	if second.ScannedFiles != 1 || second.ImportedEvents != 0 || second.SkippedDuplicateEvents != 1 {
		t.Fatalf("expected moved archived session to deduplicate, got %#v", second)
	}
}

func TestUsageReportCountsFallbackSessionsByDerivedIdentity(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	content := `{"type":"event_msg","payload":{"type":"token_count","info":{"model":"gpt-5.3-codex","total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`
	for _, day := range []string{"06", "07"} {
		path := filepath.Join(codexDir, "sessions", "2026", "07", day, "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("expected fallback session directory, got %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("expected fallback session fixture, got %v", err)
		}
	}
	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 2 {
		t.Fatalf("expected two fallback session events, result=%#v err=%v", result, err)
	}
	report, err := newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{Range: UsageRangeAll})
	if err != nil {
		t.Fatalf("expected fallback session report, got %v", err)
	}
	if report.Summary.EventCount != 2 || report.Summary.SessionCount != 2 {
		t.Fatalf("expected distinct fallback sessions, got %#v", report.Summary)
	}
}

func TestUsageSummaryReportsUnknownCostForUnknownModel(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"unknown-model"}`,
		`{"type":"event_msg","payload":{"type":"token_count","last_token_usage":{"input_tokens":5,"output_tokens":2}}}`,
	}, "\n"))

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil {
		t.Fatalf("expected usage sync to succeed, got %v", err)
	}

	summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 5 || summary.OutputTokens != 2 {
		t.Fatalf("unexpected summary tokens: %#v", summary)
	}
	if summary.CostStatus != "unknown" || summary.EstimatedCostUSD != nil || summary.UnknownCostEventCount != 1 {
		t.Fatalf("expected unknown cost summary, got %#v", summary)
	}
}

func TestSummarySourceReflectsAggregatedSources(t *testing.T) {
	if got := summarySource(nil); got != "" {
		t.Fatalf("expected empty source for no sources, got %q", got)
	}
	if got := summarySource([]string{"codex-session-jsonl"}); got != "codex-session-jsonl" {
		t.Fatalf("expected single source, got %q", got)
	}
	if got := summarySource([]string{"codex-archive-jsonl", "codex-session-jsonl"}); got != "multiple" {
		t.Fatalf("expected multiple source marker, got %q", got)
	}
}

func writeAppUsageFixture(t *testing.T, codexDir, content string) string {
	t.Helper()
	path := filepath.Join(codexDir, "sessions", "2026", "07", "06", "session.jsonl")
	writeAppUsageFile(t, path, content)
	return path
}

func writeAppUsageFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("expected fixture dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected fixture write to succeed, got %v", err)
	}
}

func appendAppUsageFixture(t *testing.T, path, line string) {
	t.Helper()
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("expected fixture append open to succeed, got %v", err)
	}
	defer handle.Close()
	if _, err := handle.WriteString("\n" + line); err != nil {
		t.Fatalf("expected fixture append write to succeed, got %v", err)
	}
}
