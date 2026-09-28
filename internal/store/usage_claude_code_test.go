package store

import (
	"context"
	"errors"
	"testing"
)

func TestClaudeCodeUsageMigrationIntegrity(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
	if err != nil || len(report.Issues) != 0 {
		t.Fatalf("integrity: %+v, %v", report, err)
	}
}

func TestClaudeCodeUsageReconciliationAcrossFilesAndConflictOrders(t *testing.T) {
	for _, order := range [][]int{{0, 1, 2}, {2, 0, 1}, {1, 2, 0}} {
		ctx := context.Background()
		db := migratedTestStore(t, ctx)
		createUsageProviderFixture(t, ctx, db, "claude-code")
		source, err := db.BeginUsageSync(ctx, "claude-code", "claude-code-session-jsonl", 1)
		if err != nil {
			t.Fatal(err)
		}
		write := int64(10)
		partial := CreateUsageFactParams{EventKey: testUsageKey("shared-request"), SourceID: source.ID, SessionKey: "derived-" + testUsageKey("session-a").String(), ModelKey: "claude-opus-5-5", InputTokens: 100, CachedInputTokens: 20, TotalTokens: 100, CacheCreationInputTokens: &write, CacheWriteInputTokens: &write, TokenStatus: UsageTokensPartial}
		final := partial
		final.TokenStatus = UsageTokensComplete
		final.OutputTokens = 30
		final.TotalTokens = 130
		conflicting := final
		conflicting.OutputTokens = 40
		conflicting.TotalTokens = 140
		candidates := []CreateUsageFactParams{partial, final, conflicting}
		for i, index := range order {
			fact := candidates[index]
			params := claudeCodeTestImport(source, fact, testUsageKey(string(rune('a'+i))))
			if _, err := db.CommitClaudeCodeUsageImport(ctx, params); err != nil {
				t.Fatal(err)
			}
		}
		summary, err := db.UsageSummary(ctx, "claude-code")
		if err != nil || summary.EventCount != 1 || summary.TotalTokens != 0 || summary.ConflictingEventCount != 1 {
			t.Fatalf("order %v: %+v, %v", order, summary, err)
		}
		if _, err := db.CommitClaudeCodeUsageImport(ctx, claudeCodeTestImport(source, final, testUsageKey("retry"))); err != nil {
			t.Fatal(err)
		}
		summary, err = db.UsageSummary(ctx, "claude-code")
		if err != nil || summary.ConflictingEventCount != 1 || summary.TotalTokens != 0 {
			t.Fatalf("conflict disappeared: %+v, %v", summary, err)
		}
		closeTestStore(t, db)
	}
}

func TestClaudeCodePartialConflictCanCompleteAndStaleCursorRollsBackUpgrade(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "claude-code")
	source, err := db.BeginUsageSync(ctx, "claude-code", "claude-code-session-jsonl", 1)
	if err != nil {
		t.Fatal(err)
	}
	write := int64(0)
	partial := CreateUsageFactParams{EventKey: testUsageKey("partial"), SourceID: source.ID, SessionKey: "derived-" + testUsageKey("session").String(), ModelKey: "claude-opus-5-5", InputTokens: 100, TotalTokens: 100, TokenStatus: UsageTokensPartial, CacheCreationInputTokens: &write, CacheWriteInputTokens: &write}
	first := claudeCodeTestImport(source, partial, testUsageKey("first"))
	if _, err := db.CommitClaudeCodeUsageImport(ctx, first); err != nil {
		t.Fatal(err)
	}
	cursor, err := db.GetClaudeCodeUsageImportFile(ctx, source.ID, first.File.FileKey)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := partial
	conflicting.InputTokens = 120
	conflicting.TotalTokens = 120
	if _, err := db.CommitClaudeCodeUsageImport(ctx, claudeCodeTestImport(source, conflicting, testUsageKey("second"))); err != nil {
		t.Fatal(err)
	}
	final := partial
	final.TokenStatus = UsageTokensComplete
	final.OutputTokens = 30
	final.TotalTokens = 130
	stale := claudeCodeTestImport(source, final, first.File.FileKey)
	stale.Expected = &cursor
	stale.Expected.UpdatedAtUnixMS--
	stale.File.ImportedFacts = 2
	if _, err := db.CommitClaudeCodeUsageImport(ctx, stale); !errors.Is(err, ErrUsageCursorConflict) {
		t.Fatalf("stale cursor: %v", err)
	}
	summary, err := db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.ConflictingEventCount != 1 || summary.TotalTokens != 0 {
		t.Fatalf("failed CAS committed upgrade: %+v, %v", summary, err)
	}
	if _, err := db.CommitClaudeCodeUsageImport(ctx, claudeCodeTestImport(source, final, testUsageKey("third"))); err != nil {
		t.Fatal(err)
	}
	summary, err = db.UsageSummary(ctx, "claude-code")
	if err != nil || summary.TotalTokens != 130 || summary.ConflictingEventCount != 0 || summary.IncompleteEventCount != 0 {
		t.Fatalf("final did not resolve partial conflict: %+v, %v", summary, err)
	}
}

func claudeCodeTestImport(source UsageSource, fact CreateUsageFactParams, key UsageKey) CommitClaudeCodeUsageImportParams {
	return CommitClaudeCodeUsageImportParams{ProviderID: "claude-code", Generation: source.SyncGeneration, Facts: []CreateUsageFactParams{fact}, File: ClaudeCodeUsageImportFile{SourceID: source.ID, FileKey: key, ImportedFacts: 1, ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("events"), CheckpointRevision: 1, MetadataDigest: testUsageKey("metadata"), BoundaryDigest: testUsageKey("boundary"), CheckpointEventDigest: testUsageKey("observations"), ParserStateJSON: "{}"}}
}

func TestClaudeCodeUsageDiagnosticsRejectUnrecognizedDataAndInvalidCounts(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "claude-code")
	source, err := db.BeginUsageSync(ctx, "claude-code", "claude-code-session-jsonl", 1)
	if err != nil {
		t.Fatal(err)
	}
	params := claudeCodeTestImport(source, CreateUsageFactParams{}, testUsageKey("diagnostics"))
	params.Facts = nil
	params.File.ImportedFacts = 0
	params.File.InvalidLines = 1
	params.File.ParserStateJSON = `{"cache_tokens_mismatch":1}`
	if _, err := db.CommitClaudeCodeUsageImport(ctx, params); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{`{"prompt":1}`, `{"cache_tokens_mismatch":-1}`, `{"cache_tokens_mismatch":2}`, `{"cache_tokens_mismatch":"1"}`, `{"cache_tokens_mismatch":null}`, `{"cache_tokens_mismatch":-1,"cache_tokens_mismatch":1}`} {
		params.File.FileKey = testUsageKey("rejected")
		params.File.ParserStateJSON = state
		if _, err := db.CommitClaudeCodeUsageImport(ctx, params); err == nil {
			t.Fatal("invalid diagnostics accepted")
		}
		if _, err := db.executor().ExecContext(ctx, `UPDATE claude_code_usage_import_files SET parser_state_json=? WHERE source_id=?`, state, source.ID); err != nil {
			t.Fatal(err)
		}
		report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
		if err != nil || len(report.Issues) == 0 {
			t.Fatalf("invalid diagnostic integrity: %+v, %v", report, err)
		}
	}
}
