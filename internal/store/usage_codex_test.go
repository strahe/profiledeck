package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUsageIntegrityRejectsCodexCursorBoundToAnotherProvider(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "future-agent")
	source, err := db.BeginUsageSync(ctx, "future-agent", "local-log", 1)
	if err != nil {
		t.Fatalf("begin non-Codex usage sync: %v", err)
	}
	if _, err := db.executor().ExecContext(ctx, `
		INSERT INTO codex_usage_import_files (
			source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
			invalid_lines, unsupported_lines, parser_revision, identity_revision,
			event_digest, updated_at_unix_ms
		) VALUES (?, ?, 0, 0, 0, 0, 0, 1, 1, ?, 0)
	`, source.ID, testUsageKey("foreign-provider-file"), testUsageKey("foreign-provider-digest")); err != nil {
		t.Fatalf("create cross-Provider cursor fixture: %v", err)
	}
	assertIntegrityIssue(t, ctx, db, IntegrityIssueReferences)
}

func TestCodexUsageImportFileUsesCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}
	fileKey := testUsageKey("source-key")
	first := CodexUsageImportFile{
		SourceID: source.ID, FileKey: fileKey, ModifiedUnixMS: 100, SizeBytes: 20,
		InvalidLines: 2, UnsupportedLines: 3,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("first"),
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: first})); err != nil {
		t.Fatalf("expected cursor insert, got %v", err)
	}
	stored, err := db.GetCodexUsageImportFile(ctx, source.ID, fileKey)
	if err != nil {
		t.Fatalf("expected cursor query, got %v", err)
	}
	second := first
	second.ModifiedUnixMS = 200
	second.SizeBytes = 30
	second.InvalidLines = 5
	second.UnsupportedLines = 6
	second.EventDigest = testUsageKey("second")
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: second, Expected: &stored})); err != nil {
		t.Fatalf("expected cursor CAS update, got %v", err)
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: first, Expected: &stored})); !errors.Is(err, ErrUsageCursorConflict) {
		t.Fatalf("expected stale cursor state to conflict, got %v", err)
	}
	current, err := db.GetCodexUsageImportFile(ctx, source.ID, fileKey)
	if err != nil || current.ModifiedUnixMS != 200 || current.ImportedFacts != 0 || current.EventDigest != second.EventDigest {
		t.Fatalf("unexpected cursor after stale update: %#v err=%v", current, err)
	}
}

func TestCodexUsageFactPrefixMatchesPersistedSemantics(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	facts := []CreateUsageFactParams{
		{
			EventKey:         testUsageKey("prefix-first"),
			SourceID:         source.ID,
			SessionKey:       "session-prefix",
			ModelKey:         "gpt-5.3-codex",
			OccurredAtUnixMS: 1_000,
			InputTokens:      10,
			OutputTokens:     2,
			TotalTokens:      12,
			CostStatus:       UsageCostStatusUnknown,
		},
		{
			EventKey:         testUsageKey("prefix-second"),
			SourceID:         source.ID,
			SessionKey:       "session-prefix",
			ModelKey:         "gpt-5.3-codex",
			OccurredAtUnixMS: 2_000,
			InputTokens:      20,
			OutputTokens:     4,
			TotalTokens:      24,
			CostStatus:       UsageCostStatusUnknown,
		},
	}
	if _, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, facts)); err != nil {
		t.Fatalf("insert usage facts: %v", err)
	}

	matched, err := db.CodexUsageFactPrefixMatches(ctx, source.ID, facts)
	if err != nil || !matched {
		t.Fatalf("matching prefix = %v, err = %v", matched, err)
	}

	costChanged := append([]CreateUsageFactParams(nil), facts...)
	estimatedCost := int64(123)
	costChanged[0].EstimatedCostMicros = &estimatedCost
	costChanged[0].CostStatus = UsageCostStatusEstimated
	matched, err = db.CodexUsageFactPrefixMatches(ctx, source.ID, costChanged)
	if err != nil || !matched {
		t.Fatalf("derived cost change matched prefix = %v, err = %v", matched, err)
	}

	timestampChanged := append([]CreateUsageFactParams(nil), facts...)
	timestampChanged[0].OccurredAtUnixMS++
	matched, err = db.CodexUsageFactPrefixMatches(ctx, source.ID, timestampChanged)
	if err != nil || matched {
		t.Fatalf("timestamp change matched prefix = %v, err = %v", matched, err)
	}

	modelChanged := append([]CreateUsageFactParams(nil), facts...)
	modelChanged[0].ModelKey = "gpt-5.4"
	matched, err = db.CodexUsageFactPrefixMatches(ctx, source.ID, modelChanged)
	if err != nil || matched {
		t.Fatalf("model change matched prefix = %v, err = %v", matched, err)
	}

	missing := append([]CreateUsageFactParams(nil), facts...)
	missing[1].EventKey = testUsageKey("prefix-missing")
	matched, err = db.CodexUsageFactPrefixMatches(ctx, source.ID, missing)
	if err != nil || matched {
		t.Fatalf("missing fact matched prefix = %v, err = %v", matched, err)
	}
}

func TestCodexUsageFinalizationCountsOnlyPersistedCursors(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	cursor := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("tracked-cursor"),
		InvalidLines: 2, UnsupportedLines: 3,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("tracked-digest"),
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: cursor})); err != nil {
		t.Fatalf("commit tracked cursor: %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: source.ID, Generation: source.SyncGeneration, CompletedAtUnixMS: 10,
		Finalization: &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{
			cursor.FileKey,
			testUsageKey("discovered-without-cursor"),
		}},
	}); err != nil {
		t.Fatalf("complete usage sync: %v", err)
	}

	completed, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil {
		t.Fatalf("read completed usage source: %v", err)
	}
	if completed.TrackedUnits != 1 || completed.InvalidRecords != 2 || completed.UnsupportedRecords != 3 {
		t.Fatalf("completion counters must describe persisted cursors: %#v", completed)
	}
}

func TestCommitCodexUsageImportIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}

	cost := int64(42)
	fact := CreateUsageFactParams{
		EventKey:            testUsageKey("event-atomic"),
		SourceID:            source.ID,
		SessionKey:          "session-atomic",
		ModelKey:            "gpt-5.3-codex",
		OccurredAtUnixMS:    1_000,
		InputTokens:         10,
		CachedInputTokens:   2,
		OutputTokens:        3,
		TotalTokens:         13,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
	}
	cursor := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("source-atomic"),
		ModifiedUnixMS: 100, SizeBytes: 200, ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("event-digest"),
	}
	invalidProgress := cursor
	invalidProgress.FileKey = testUsageKey("source-invalid-progress")
	invalidProgress.ImportedFacts = 2
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{fact}, File: invalidProgress,
	})); err == nil {
		t.Fatal("expected cursor progress without matching facts to fail")
	}
	if _, err := db.GetCodexUsageImportFile(ctx, source.ID, invalidProgress.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid progress advanced cursor: %v", err)
	}

	first, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{Facts: []CreateUsageFactParams{fact}, File: cursor}))
	if err != nil || first.Inserted != 1 || first.Duplicates != 0 {
		t.Fatalf("expected first atomic import to insert one event, result=%#v err=%v", first, err)
	}
	stored, err := db.GetCodexUsageImportFile(ctx, source.ID, cursor.FileKey)
	if err != nil {
		t.Fatalf("expected committed cursor, got %v", err)
	}
	duplicateCursor := cursor
	duplicateCursor.FileKey = testUsageKey("source-atomic-copy")
	second, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{fact}, File: duplicateCursor,
	}))
	if err != nil || second.Inserted != 0 || second.Duplicates != 1 {
		t.Fatalf("expected repeated atomic import to deduplicate, result=%#v err=%v", second, err)
	}
	earlierDuplicate := fact
	earlierDuplicate.OccurredAtUnixMS = 500
	rollbackCursor := cursor
	rollbackCursor.SizeBytes = -1
	rollbackCursor.ImportedFacts = 2
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{earlierDuplicate}, File: rollbackCursor, Expected: &stored,
	})); err == nil {
		t.Fatalf("expected invalid cursor to roll back canonical observation update")
	}
	var canonicalOccurredAt int64
	if err := db.executor().QueryRowContext(ctx, `SELECT occurred_at_unix_ms
		FROM usage_facts WHERE event_key = ?`, fact.EventKey).Scan(&canonicalOccurredAt); err != nil {
		t.Fatalf("expected canonical event after rollback, got %v", err)
	}
	if canonicalOccurredAt != fact.OccurredAtUnixMS {
		t.Fatalf("expected failed cursor to roll back canonical update, occurred=%d", canonicalOccurredAt)
	}

	failingFact := fact
	failingFact.EventKey = testUsageKey("event-rolled-back")
	failingCursor := cursor
	failingCursor.FileKey = testUsageKey("source-rolled-back")
	failingCursor.SizeBytes = -1
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{failingFact}, File: failingCursor,
	})); err == nil {
		t.Fatalf("expected invalid cursor to fail the atomic import")
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary after rollback, got %v", err)
	}
	if summary.EventCount != 1 {
		t.Fatalf("expected failed import event to roll back, got %#v", summary)
	}
	if _, err := db.GetCodexUsageImportFile(ctx, source.ID, failingCursor.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected failed import cursor not to advance, got %v", err)
	}
	failingCursor.SizeBytes = 1
	retry, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{failingFact}, File: failingCursor,
	}))
	if err != nil || retry.Inserted != 1 || retry.Duplicates != 0 {
		t.Fatalf("expected rolled-back event ID to remain insertable, result=%#v err=%v", retry, err)
	}
}
