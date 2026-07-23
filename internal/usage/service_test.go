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

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/store"
)

type pausedUsageIntegration struct {
	started chan struct{}
	resume  chan struct{}
}

func (*pausedUsageIntegration) ProviderID() string {
	return ProviderCodex
}

func (*pausedUsageIntegration) SourceIDs() []string {
	return []string{SourceCodexSessionJSONL}
}

func (*pausedUsageIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{}
}

func (integration *pausedUsageIntegration) Sync(
	ctx context.Context,
	stores store.Factory,
	_ SyncProvisionMode,
) (UsageSyncResult, error) {
	close(integration.started)
	select {
	case <-ctx.Done():
		return UsageSyncResult{}, ctx.Err()
	case <-integration.resume:
	}
	db, err := stores.OpenHealthy(ctx, false)
	if err != nil {
		return UsageSyncResult{}, err
	}
	defer db.Close()
	if _, err := db.BeginUsageSync(ctx, ProviderCodex, SourceCodexSessionJSONL, CodexUsageIdentityRevision); err != nil {
		return UsageSyncResult{}, err
	}
	return UsageSyncResult{ProviderID: ProviderCodex, Source: SourceCodexSessionJSONL}, nil
}

func TestBackgroundUsageSyncDoesNotRecreateProviderDeletedAfterDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	configDir := t.TempDir()
	environment := newUsageTestEnvironment(t, configDir, "")
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Provider store: %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: ProviderCodex, Name: "Codex", AdapterID: ProviderCodex, MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create enabled Provider: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Provider store: %v", err)
	}

	integration := &pausedUsageIntegration{started: make(chan struct{}), resume: make(chan struct{})}
	service := NewService(environment.runtime.StoreFactory(), MustRegistry(integration))
	errCh := make(chan error, 1)
	go func() {
		_, syncErr := service.SyncCodexBackground(ctx)
		errCh <- syncErr
	}()
	select {
	case <-ctx.Done():
		t.Fatalf("sync did not reach Integration: %v", ctx.Err())
	case <-integration.started:
	}

	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("reopen Provider store: %v", err)
	}
	if err := db.DeleteProvider(ctx, ProviderCodex); err != nil {
		_ = db.Close()
		t.Fatalf("delete Provider during sync: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close deleted Provider store: %v", err)
	}
	close(integration.resume)

	select {
	case <-ctx.Done():
		t.Fatalf("sync did not finish: %v", ctx.Err())
	case err := <-errCh:
		if err != nil {
			t.Fatalf("background sync should treat a deleted Provider as inactive: %v", err)
		}
	}
	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("inspect usage source: %v", err)
	}
	defer db.Close()
	if _, err := db.GetProvider(ctx, ProviderCodex); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("background sync recreated the deleted Provider: %v", err)
	}
	if _, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("background sync created a usage source for the deleted Provider: %v", err)
	}
}

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
	if _, err := rawDB.ExecContext(ctx, `
		UPDATE codex_usage_import_files SET updated_at_unix_ms = 1;
		UPDATE usage_sources SET last_completed_at_unix_ms = 1
	`); err != nil {
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
	if err := rawDB.QueryRowContext(ctx, "SELECT COUNT(1) FROM codex_usage_import_files WHERE updated_at_unix_ms > 1").Scan(&touched); err != nil {
		_ = rawDB.Close()
		t.Fatalf("expected cursor freshness query to succeed, got %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("expected raw fixture database close, got %v", err)
	}
	if touched != 0 {
		t.Fatalf("expected unchanged sync not to touch file cursors, got %d writes", touched)
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
	if _, err := rawDB.ExecContext(ctx, `
		UPDATE codex_usage_import_files SET updated_at_unix_ms = 1;
		UPDATE usage_sources SET last_completed_at_unix_ms = 1
	`); err != nil {
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
	if err := rawDB.QueryRowContext(ctx, "SELECT COUNT(1) FROM codex_usage_import_files WHERE updated_at_unix_ms > 1").Scan(&touched); err != nil {
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

func TestUsageSyncCodexRejectsTruncatedAndRewrittenHistory(t *testing.T) {
	for _, test := range []struct {
		name    string
		rewrite func(t *testing.T, path, original string)
	}{
		{
			name: "truncated file",
			rewrite: func(t *testing.T, path, original string) {
				t.Helper()
				lines := strings.Split(original, "\n")
				if err := os.WriteFile(path, []byte(strings.Join(lines[:3], "\n")), 0o600); err != nil {
					t.Fatalf("truncate fixture: %v", err)
				}
			},
		},
		{
			name: "rewritten prefix",
			rewrite: func(t *testing.T, path, original string) {
				t.Helper()
				rewritten := strings.Replace(original, `"input_tokens":10`, `"input_tokens":11`, 1)
				if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
					t.Fatalf("rewrite fixture: %v", err)
				}
				future := time.Now().Add(2 * time.Second)
				if err := os.Chtimes(path, future, future); err != nil {
					t.Fatalf("advance rewritten fixture timestamp: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			configDir := t.TempDir()
			codexDir := t.TempDir()
			original := strings.Join([]string{
				`{"type":"session_meta","session_id":"session-history"}`,
				`{"type":"turn_context","model":"gpt-5.3-codex"}`,
				`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`,
				`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":4}}}}`,
			}, "\n")
			path := writeAppUsageFixture(t, codexDir, original)
			if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
				t.Fatalf("initialize runtime: %v", err)
			}
			if result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 2 {
				t.Fatalf("initial sync result=%#v err=%v", result, err)
			}
			test.rewrite(t, path, original)
			result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
			if err != nil || result.ImportedEvents != 0 || len(result.Errors) != 1 || result.Errors[0].Message != codexHistoryChangedMessage {
				t.Fatalf("changed history sync result=%#v err=%v", result, err)
			}
			summary, err := newUsageTestEnvironment(t, configDir, "").service.Summary(ctx, UsageSummaryRequest{})
			if err != nil || summary.EventCount != 2 || summary.TotalTokens != 24 {
				t.Fatalf("changed history altered facts: summary=%#v err=%v", summary, err)
			}
		})
	}
}

func TestUsageSyncCodexRevalidatesCompatibleParserRevision(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-parser"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`,
	}, "\n"))
	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	service := newUsageTestEnvironment(t, configDir, codexDir).service
	if _, err := service.SyncCodex(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE codex_usage_import_files SET parser_revision = ?`, CodexUsageParserRevision+1); err != nil {
		_ = rawDB.Close()
		t.Fatalf("change parser revision fixture: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	result, err := service.SyncCodex(ctx)
	if err != nil || result.ImportedEvents != 0 || result.SkippedUnchangedFiles != 0 || len(result.Errors) != 0 {
		t.Fatalf("compatible parser sync result=%#v err=%v", result, err)
	}
	rawDB, err = sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen fixture database: %v", err)
	}
	defer rawDB.Close()
	var revision int64
	if err := rawDB.QueryRowContext(ctx, `SELECT parser_revision FROM codex_usage_import_files`).Scan(&revision); err != nil || revision != CodexUsageParserRevision {
		t.Fatalf("parser revision after validation=%d err=%v", revision, err)
	}
}

func TestUsageSyncCodexRejectsIdentityRevisionWithoutWrites(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`)
	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	service := newUsageTestEnvironment(t, configDir, codexDir).service
	if _, err := service.SyncCodex(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE usage_sources SET identity_revision = ?`, CodexUsageIdentityRevision-1); err != nil {
		_ = rawDB.Close()
		t.Fatalf("change identity revision fixture: %v", err)
	}
	var generationBefore, cursorUpdatedBefore int64
	if err := rawDB.QueryRowContext(ctx, `SELECT sync_generation FROM usage_sources`).Scan(&generationBefore); err != nil {
		_ = rawDB.Close()
		t.Fatalf("read generation fixture: %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, `SELECT updated_at_unix_ms FROM codex_usage_import_files`).Scan(&cursorUpdatedBefore); err != nil {
		_ = rawDB.Close()
		t.Fatalf("read cursor fixture: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	_, err = service.SyncCodex(ctx)
	assertAppErrorCode(t, err, apperror.UsageMigrationRequired)
	rawDB, err = sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen fixture database: %v", err)
	}
	defer rawDB.Close()
	var generationAfter, cursorUpdatedAfter int64
	if err := rawDB.QueryRowContext(ctx, `SELECT sync_generation FROM usage_sources`).Scan(&generationAfter); err != nil {
		t.Fatalf("read generation after rejection: %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, `SELECT updated_at_unix_ms FROM codex_usage_import_files`).Scan(&cursorUpdatedAfter); err != nil {
		t.Fatalf("read cursor after rejection: %v", err)
	}
	if generationAfter != generationBefore || cursorUpdatedAfter != cursorUpdatedBefore {
		t.Fatalf("identity rejection wrote state: generation %d->%d cursor %d->%d", generationBefore, generationAfter, cursorUpdatedBefore, cursorUpdatedAfter)
	}
}

func TestUsageSyncCodexDoesNotDoubleCountCopiedSessionFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	content := strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n")
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "01", "original.jsonl"), content)

	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ImportedEvents != 1 || first.SkippedDuplicateEvents != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "02", "copy.jsonl"), content)
	second, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
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
	codexDir := t.TempDir()
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "02", "child.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"output_tokens":3,"total_tokens":33}}},"timestamp":"2026-07-02T00:02:00Z"}`,
	}, "\n"))
	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 3 {
		t.Fatalf("expected child-first import, result=%#v err=%v", result, err)
	}
	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "01", "parent.jsonl"), strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-01T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-01T00:01:00Z"}`,
	}, "\n"))
	if result, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx); err != nil || result.ImportedEvents != 0 || result.SkippedDuplicateEvents != 2 {
		t.Fatalf("expected later parent import to deduplicate, result=%#v err=%v", result, err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("expected usage database open, got %v", err)
	}
	defer rawDB.Close()
	var earliest int64
	var latest int64
	if err := rawDB.QueryRowContext(ctx, `SELECT MIN(f.occurred_at_unix_ms), MAX(f.occurred_at_unix_ms)
		FROM usage_facts f
		JOIN usage_sessions s ON s.id = f.session_id AND s.source_id = f.source_id
		WHERE s.session_key = 'session-parent'`).Scan(&earliest, &latest); err != nil {
		t.Fatalf("expected parent timestamp query, got %v", err)
	}
	if earliest != time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || latest != time.Date(2026, 7, 1, 0, 1, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("expected original parent timestamps, earliest=%d latest=%d", earliest, latest)
	}
}

func TestUsageSyncCodexConcurrentRunsRemainIdempotentWhenOneRunIsSuperseded(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-concurrent"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n"))
	if _, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	start := make(chan struct{})
	errorsByRun := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := newUsageTestEnvironment(t, configDir, codexDir).service.SyncCodex(ctx)
			errorsByRun <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsByRun)
	var succeeded, superseded int
	for err := range errorsByRun {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, store.ErrUsageSyncSuperseded), errors.Is(err, store.ErrUsageCursorConflict):
			assertAppErrorCode(t, err, apperror.UsageSyncConflict)
			superseded++
		default:
			t.Fatalf("unexpected concurrent sync error: %v", err)
		}
	}
	if succeeded == 0 || succeeded+superseded != 2 {
		t.Fatalf("concurrent sync outcomes succeeded=%d superseded=%d", succeeded, superseded)
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
	codexDir := t.TempDir()
	environment := newUsageTestEnvironment(t, configDir, codexDir)
	initialized, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := environment.service.SyncCodex(ctx); err != nil {
		t.Fatalf("expected explicit sync to provision Provider fixture, got %v", err)
	}
	db, err := store.Open(ctx, initialized.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected fixture store open, got %v", err)
	}
	source, err := db.BeginUsageSync(ctx, ProviderCodex, SourceCodexSessionJSONL, CodexUsageIdentityRevision)
	if err != nil {
		_ = db.Close()
		t.Fatalf("expected usage source fixture, got %v", err)
	}
	if result, err := db.InsertUsageFacts(ctx, store.InsertUsageFactsParams{
		SourceID: source.ID, Generation: source.SyncGeneration, Facts: []store.CreateUsageFactParams{{
			EventKey: usageTestEventKey("existing-gpt-5.6"), SourceID: source.ID,
			SessionKey: "session", ModelKey: "GPT-5.6-SOL", OccurredAtUnixMS: time.Now().UnixMilli(),
			InputTokens: 1_000_000, CachedInputTokens: 100_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000,
			CostStatus: store.UsageCostStatusUnknown,
		}},
	}); err != nil || result.Inserted != 1 {
		_ = db.Close()
		t.Fatalf("expected unknown historical usage fixture, result=%#v err=%v", result, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected fixture store close, got %v", err)
	}

	if _, err := environment.service.SyncCodex(ctx); err != nil {
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
	if len(report.Models) != 1 || report.Models[0].Model != "GPT-5.6-SOL" || report.Models[0].Summary.KnownEstimatedCostUSD != "34.550000" {
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
	initialized, err := bootstrap.NewService(newUsageTestEnvironment(t, configDir, "").runtime, nil, nil).Initialize(ctx)
	if err != nil {
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
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open usage database: %v", err)
	}
	defer rawDB.Close()
	var cursorCount int
	if err := rawDB.QueryRowContext(ctx, `SELECT COUNT(1) FROM codex_usage_import_files`).Scan(&cursorCount); err != nil || cursorCount != 1 {
		t.Fatalf("stale cursor cleanup count=%d err=%v", cursorCount, err)
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

func TestProviderDeleteClearsUsageAndOnlyExplicitSyncRecreatesIt(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"deleted-provider-session"}`,
		`{"type":"turn_context","model":"gpt-5.6-sol"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	}, "\n"))
	environment := newUsageTestEnvironment(t, configDir, codexDir)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncCodex(ctx); err != nil {
		t.Fatalf("explicit sync should provision the Provider: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open usage store: %v", err)
	}
	if err := db.DeleteProvider(ctx, ProviderCodex); err != nil {
		_ = db.Close()
		t.Fatalf("delete Provider with Usage: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close deleted Provider store: %v", err)
	}

	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: ProviderCodex})
	if err != nil || summary.EventCount != 0 || summary.TotalTokens != 0 {
		t.Fatalf("Provider deletion retained Usage: summary=%#v err=%v", summary, err)
	}
	if _, err := environment.service.SyncCodexBackground(ctx); err != nil {
		t.Fatalf("background sync for a deleted Provider should be a no-op: %v", err)
	}
	db, err = environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("inspect background sync: %v", err)
	}
	if _, err := db.GetProvider(ctx, ProviderCodex); !errors.Is(err, store.ErrNotFound) {
		_ = db.Close()
		t.Fatalf("background sync recreated Provider: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close inspection store: %v", err)
	}

	result, err := environment.service.SyncCodex(ctx)
	if err != nil || result.ImportedEvents != 1 {
		t.Fatalf("explicit sync did not recreate and reimport Provider Usage: result=%#v err=%v", result, err)
	}
	summary, err = environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: ProviderCodex})
	if err != nil || summary.EventCount != 1 || summary.TotalTokens != 12 {
		t.Fatalf("recreated Provider Usage summary=%#v err=%v", summary, err)
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
