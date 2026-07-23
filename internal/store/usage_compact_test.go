package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
)

func testUsageKey(payload string) UsageKey {
	return UsageKey(sha256.Sum256([]byte(payload)))
}

func testUsageFactBatch(source UsageSource, facts []CreateUsageFactParams) InsertUsageFactsParams {
	return InsertUsageFactsParams{
		SourceID:   source.ID,
		Generation: source.SyncGeneration,
		Facts:      facts,
	}
}

func testCodexUsageImport(source UsageSource, params CommitCodexUsageImportParams) CommitCodexUsageImportParams {
	params.Generation = source.SyncGeneration
	return params
}

func createUsageProviderFixture(t *testing.T, ctx context.Context, db *Store, providerID string) {
	t.Helper()
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID: providerID, Name: providerID, AdapterID: providerID, MetadataJSON: `{}`,
	}); err != nil && !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("create Usage Provider fixture %q: %v", providerID, err)
	}
}

func TestUsageWritesRequireProviderAndCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)

	if _, err := db.BeginUsageSync(ctx, "codex", "missing-provider-source", 1); !errors.Is(err, ErrUsageProviderMissing) {
		t.Fatalf("begin sync without Provider error = %v, want ErrUsageProviderMissing", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("create Provider: %v", err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin sync: %v", err)
	}
	fact := CreateUsageFactParams{
		EventKey: testUsageKey("provider-owned-fact"), SourceID: source.ID,
		ModelKey: "model", TotalTokens: 1, CostStatus: UsageCostStatusUnknown,
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{fact})); err != nil || result.Inserted != 1 {
		t.Fatalf("insert fact: result=%#v err=%v", result, err)
	}
	if err := db.DeleteProvider(ctx, "codex"); err != nil {
		t.Fatalf("delete Provider: %v", err)
	}
	if _, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Provider deletion retained source: %v", err)
	}
	if _, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{fact})); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("stale source write error = %v, want ErrUsageSyncSuperseded", err)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.EventCount != 0 {
		t.Fatalf("Provider deletion retained Usage: summary=%#v err=%v", summary, err)
	}
}

func TestUsageWriteTransactionRollsBackWhenProviderIsDeleted(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("create Provider: %v", err)
	}
	err := db.WithTransaction(ctx, func(tx *Store) error {
		source, err := tx.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
		if err != nil {
			return err
		}
		fact := CreateUsageFactParams{
			EventKey: testUsageKey("rolled-back-provider-fact"), SourceID: source.ID,
			ModelKey: "gpt-5.6-sol", TotalTokens: 5, CostStatus: UsageCostStatusUnknown,
		}
		if _, err := tx.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{fact})); err != nil {
			return err
		}
		if err := tx.DeleteProvider(ctx, "codex"); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	if _, err := db.GetProvider(ctx, "codex"); err != nil {
		t.Fatalf("rollback did not restore Provider: %v", err)
	}
	if _, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback retained transient Usage source: %v", err)
	}
}

func TestUsageFactConflictRollsBackFileAndOtherFacts(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	base := CreateUsageFactParams{
		EventKey: testUsageKey("base"), SourceID: source.ID,
		SessionKey: "session", ModelKey: "model", InputTokens: 10, TotalTokens: 10,
		CostStatus: UsageCostStatusUnknown,
	}
	baseFile := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("base-file"), ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("base-digest"),
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{base}, File: baseFile,
	})); err != nil {
		t.Fatalf("commit base import: %v", err)
	}
	otherSource, err := db.BeginUsageSync(ctx, "codex", "codex-secondary-jsonl", 1)
	if err != nil {
		t.Fatalf("begin secondary source: %v", err)
	}
	tokenConflict := base
	tokenConflict.InputTokens = 11
	tokenConflict.TotalTokens = 11
	sessionConflict := base
	sessionConflict.SessionKey = "different-session"
	sourceConflict := base
	sourceConflict.SourceID = otherSource.ID

	for _, test := range []struct {
		name        string
		source      UsageSource
		conflicting CreateUsageFactParams
	}{
		{name: "tokens", source: source, conflicting: tokenConflict},
		{name: "session", source: source, conflicting: sessionConflict},
		{name: "source", source: otherSource, conflicting: sourceConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			newFact := base
			newFact.EventKey = testUsageKey("must-roll-back-" + test.name)
			newFact.SourceID = test.source.ID
			newFile := baseFile
			newFile.SourceID = test.source.ID
			newFile.FileKey = testUsageKey("must-roll-back-file-" + test.name)
			newFile.ImportedFacts = 2
			newFile.EventDigest = testUsageKey("must-roll-back-digest-" + test.name)
			if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(test.source, CommitCodexUsageImportParams{
				Facts: []CreateUsageFactParams{newFact, test.conflicting}, File: newFile,
			})); !errors.Is(err, ErrUsageFactConflict) {
				t.Fatalf("conflicting duplicate error = %v, want ErrUsageFactConflict", err)
			}
			summary, err := db.UsageSummary(ctx, "codex")
			if err != nil || summary.EventCount != 1 || summary.TotalTokens != 10 {
				t.Fatalf("conflicting import changed facts: summary=%#v err=%v", summary, err)
			}
			if _, err := db.GetCodexUsageImportFile(ctx, test.source.ID, newFile.FileKey); !errors.Is(err, ErrNotFound) {
				t.Fatalf("conflicting import advanced cursor: %v", err)
			}
		})
	}
}

func TestCodexUsageImportCursorCASAllowsOneConcurrentAdvance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiledeck.db")
	firstDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, firstDB)
	if _, err := firstDB.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	createUsageProviderFixture(t, ctx, firstDB, "codex")
	source, err := firstDB.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	initial := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("concurrent-file"),
		ModifiedUnixMS: 1, SizeBytes: 1, ParserRevision: 1, IdentityRevision: 1,
		EventDigest: testUsageKey("initial-digest"),
	}
	if _, err := firstDB.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: initial})); err != nil {
		t.Fatalf("commit initial cursor: %v", err)
	}
	expected, err := firstDB.GetCodexUsageImportFile(ctx, source.ID, initial.FileKey)
	if err != nil {
		t.Fatalf("read initial cursor: %v", err)
	}
	secondDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, secondDB)

	firstAdvance := initial
	firstAdvance.ModifiedUnixMS = 2
	firstAdvance.SizeBytes = 2
	firstAdvance.EventDigest = testUsageKey("first-advance")
	secondAdvance := initial
	secondAdvance.ModifiedUnixMS = 3
	secondAdvance.SizeBytes = 3
	secondAdvance.EventDigest = testUsageKey("second-advance")

	start := make(chan struct{})
	errorsByAdvance := make(chan error, 2)
	var wait sync.WaitGroup
	for _, advance := range []struct {
		db   *Store
		file CodexUsageImportFile
	}{{firstDB, firstAdvance}, {secondDB, secondAdvance}} {
		wait.Add(1)
		go func(db *Store, file CodexUsageImportFile) {
			defer wait.Done()
			<-start
			_, commitErr := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{
				File: file, Expected: &expected,
			}))
			errorsByAdvance <- commitErr
		}(advance.db, advance.file)
	}
	close(start)
	wait.Wait()
	close(errorsByAdvance)
	var succeeded, conflicted int
	for advanceErr := range errorsByAdvance {
		switch {
		case advanceErr == nil:
			succeeded++
		case errors.Is(advanceErr, ErrUsageCursorConflict):
			conflicted++
		default:
			t.Fatalf("concurrent cursor advance error: %v", advanceErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent cursor outcomes succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	current, err := firstDB.GetCodexUsageImportFile(ctx, source.ID, initial.FileKey)
	if err != nil || current.ModifiedUnixMS <= initial.ModifiedUnixMS {
		t.Fatalf("cursor did not advance exactly once: cursor=%#v err=%v", current, err)
	}
}

func TestUsageSyncGenerationPreventsStaleFinalizationAndPreservesFacts(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	first, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin first sync: %v", err)
	}
	fact := CreateUsageFactParams{
		EventKey: testUsageKey("historical"), SourceID: first.ID,
		ModelKey: "model", InputTokens: 5, TotalTokens: 5, CostStatus: UsageCostStatusUnknown,
	}
	staleFile := CodexUsageImportFile{
		SourceID: first.ID, FileKey: testUsageKey("stale-file"), ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("stale-digest"),
	}
	currentFile := staleFile
	currentFile.FileKey = testUsageKey("current-file")
	currentFile.ImportedFacts = 0
	currentFile.EventDigest = testUsageKey("current-digest")
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(first, CommitCodexUsageImportParams{Facts: []CreateUsageFactParams{fact}, File: staleFile})); err != nil {
		t.Fatalf("commit historical fact: %v", err)
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(first, CommitCodexUsageImportParams{File: currentFile})); err != nil {
		t.Fatalf("commit current cursor: %v", err)
	}
	second, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin second sync: %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: first.ID, Generation: first.SyncGeneration,
		CompletedAtUnixMS: 10,
		Finalization:      &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{staleFile.FileKey}},
	}); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("stale generation error=%v", err)
	}
	if _, err := db.GetCodexUsageImportFile(ctx, first.ID, currentFile.FileKey); err != nil {
		t.Fatalf("stale generation deleted a current cursor: %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: second.ID, Generation: second.SyncGeneration,
		CompletedAtUnixMS: 20,
		Finalization:      &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{currentFile.FileKey}},
	}); err != nil {
		t.Fatalf("latest generation completion error=%v", err)
	}
	if _, err := db.GetCodexUsageImportFile(ctx, first.ID, staleFile.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("latest generation retained stale cursor: %v", err)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.EventCount != 1 || summary.TotalTokens != 5 {
		t.Fatalf("stale cursor cleanup removed facts: summary=%#v err=%v", summary, err)
	}
	completed, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil || completed.LastCompletedAtUnixMS != 20 || completed.TrackedUnits != 1 {
		t.Fatalf("latest generation did not own source status: source=%#v err=%v", completed, err)
	}
}

func TestSupersededUsageSyncCannotWriteAfterLatestCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiledeck.db")
	staleDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, staleDB)
	if _, err := staleDB.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	createUsageProviderFixture(t, ctx, staleDB, "codex")
	stale, err := staleDB.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin stale sync: %v", err)
	}

	latestDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, latestDB)
	latest, err := latestDB.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin latest sync: %v", err)
	}
	if err := latestDB.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: latest.ID, Generation: latest.SyncGeneration, CompletedAtUnixMS: 20,
		Finalization: &CodexUsageSyncFinalization{},
	}); err != nil {
		t.Fatalf("complete latest sync: %v", err)
	}

	fact := CreateUsageFactParams{
		EventKey: testUsageKey("superseded-fact"), SourceID: stale.ID,
		ModelKey: "model", TotalTokens: 1, CostStatus: UsageCostStatusUnknown,
	}
	if _, err := staleDB.InsertUsageFacts(ctx, testUsageFactBatch(stale, []CreateUsageFactParams{fact})); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("superseded generic fact write error=%v", err)
	}
	file := CodexUsageImportFile{
		SourceID: stale.ID, FileKey: testUsageKey("superseded-file"), ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("superseded-digest"),
	}
	if _, err := staleDB.CommitCodexUsageImport(ctx, testCodexUsageImport(stale, CommitCodexUsageImportParams{
		Facts: []CreateUsageFactParams{fact}, File: file,
	})); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("superseded Codex commit error=%v", err)
	}
	if err := staleDB.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: stale.ID, Generation: stale.SyncGeneration, CompletedAtUnixMS: 10,
		Finalization: &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{file.FileKey}},
	}); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("superseded completion error=%v", err)
	}

	if _, err := latestDB.GetCodexUsageImportFile(ctx, stale.ID, file.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded sync advanced cursor: %v", err)
	}
	summary, err := latestDB.UsageSummary(ctx, "codex")
	if err != nil || summary.EventCount != 0 {
		t.Fatalf("superseded sync inserted facts: summary=%#v err=%v", summary, err)
	}
	completed, err := latestDB.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil || completed.SyncGeneration != latest.SyncGeneration || completed.LastCompletedAtUnixMS != 20 || completed.TrackedUnits != 0 {
		t.Fatalf("superseded sync changed latest source state: source=%#v err=%v", completed, err)
	}
}

func TestCompleteUsageSyncSupportsProviderSpecificIntegrations(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "future-agent")
	first, err := db.BeginUsageSync(ctx, "future-agent", "local-log", 1)
	if err != nil {
		t.Fatalf("begin first usage sync: %v", err)
	}
	latest, err := db.BeginUsageSync(ctx, "future-agent", "local-log", 1)
	if err != nil {
		t.Fatalf("begin latest usage sync: %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: latest.ID, Generation: latest.SyncGeneration,
	}); err == nil {
		t.Fatal("missing finalization completed without error")
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: latest.ID, Generation: latest.SyncGeneration,
		Finalization: &StaticUsageSyncFinalization{TrackedUnits: -1},
	}); err == nil {
		t.Fatal("invalid static finalization completed without error")
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: first.ID, Generation: first.SyncGeneration, CompletedAtUnixMS: 10,
		Finalization: &StaticUsageSyncFinalization{},
	}); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("stale generic completion error=%v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: latest.ID, Generation: latest.SyncGeneration, CompletedAtUnixMS: 20,
		Finalization: &StaticUsageSyncFinalization{
			TrackedUnits: 3, InvalidRecords: 4, UnsupportedRecords: 5,
		},
	}); err != nil {
		t.Fatalf("latest generic completion error=%v", err)
	}
	completed, err := db.GetUsageSource(ctx, "future-agent", "local-log")
	if err != nil || completed.LastCompletedAtUnixMS != 20 || completed.TrackedUnits != 3 ||
		completed.InvalidRecords != 4 || completed.UnsupportedRecords != 5 {
		t.Fatalf("generic completion source=%#v err=%v", completed, err)
	}
}

func TestUsageIdentityRevisionMismatchWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	if _, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 2); !errors.Is(err, ErrUsageIdentityRevision) {
		t.Fatalf("identity mismatch error = %v", err)
	}
	unchanged, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil || unchanged.IdentityRevision != 1 || unchanged.SyncGeneration != source.SyncGeneration {
		t.Fatalf("identity mismatch changed source: source=%#v err=%v", unchanged, err)
	}

	file := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("file"), ParserRevision: 1,
		IdentityRevision: 1, EventDigest: testUsageKey("digest"),
	}
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: file})); err != nil {
		t.Fatalf("commit cursor: %v", err)
	}
	if _, err := db.executor().ExecContext(ctx, `UPDATE codex_usage_import_files SET identity_revision = 2`); err != nil {
		t.Fatalf("create incompatible cursor fixture: %v", err)
	}
	assertIntegrityIssue(t, ctx, db, IntegrityIssueReferences)
	err = db.WithTransaction(ctx, func(txStore *Store) error {
		attempted, beginErr := txStore.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
		if beginErr != nil {
			return beginErr
		}
		return txStore.ValidateCodexUsageImportIdentity(ctx, attempted.ID, 1)
	})
	if !errors.Is(err, ErrUsageIdentityRevision) {
		t.Fatalf("cursor identity mismatch error = %v", err)
	}
	unchanged, err = db.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil || unchanged.SyncGeneration != source.SyncGeneration {
		t.Fatalf("cursor identity mismatch advanced generation: source=%#v err=%v", unchanged, err)
	}
}

type usageQueryCountingExecutor struct {
	dbExecutor
	queries int
}

func (executor *usageQueryCountingExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	executor.queries++
	return executor.dbExecutor.QueryContext(ctx, query, args...)
}

func (executor *usageQueryCountingExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	executor.queries++
	return executor.dbExecutor.QueryRowContext(ctx, query, args...)
}

func TestUsageReportQueryCountDoesNotScaleWithBuckets(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	if _, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{{
		EventKey: testUsageKey("fact"), SourceID: source.ID, SessionKey: "session",
		ModelKey: "model", OccurredAtUnixMS: 1, InputTokens: 1, TotalTokens: 1,
		CostStatus: UsageCostStatusUnknown,
	}})); err != nil {
		t.Fatalf("insert usage fact: %v", err)
	}

	var expectedQueries int
	for _, bucketCount := range []int{1, 30, 512} {
		buckets := make([]UsageTimeBucket, bucketCount)
		for index := range buckets {
			buckets[index] = UsageTimeBucket{StartUnixMS: int64(index), EndUnixMS: int64(index + 1)}
		}
		var queryCount int
		if err := db.WithTransaction(ctx, func(txStore *Store) error {
			counter := &usageQueryCountingExecutor{dbExecutor: txStore.exec}
			txStore.exec = counter
			start := int64(0)
			_, reportErr := txStore.UsageReport(ctx, UsageReportQuery{
				ProviderID: "codex", StartUnixMS: &start, EndUnixMS: int64(bucketCount), Buckets: buckets,
			})
			queryCount = counter.queries
			return reportErr
		}); err != nil {
			t.Fatalf("query %d buckets: %v", bucketCount, err)
		}
		if expectedQueries == 0 {
			expectedQueries = queryCount
		}
		if queryCount != expectedQueries {
			t.Fatalf("bucket count %d used %d queries, want fixed %d", bucketCount, queryCount, expectedQueries)
		}
	}
}

func TestUsageReportMergesModelsAndIsolatesSessionsAcrossSources(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "future-agent")
	firstSource, err := db.BeginUsageSync(ctx, "future-agent", "primary-log", 1)
	if err != nil {
		t.Fatalf("begin first source: %v", err)
	}
	secondSource, err := db.BeginUsageSync(ctx, "future-agent", "secondary-log", 1)
	if err != nil {
		t.Fatalf("begin second source: %v", err)
	}
	firstFact := CreateUsageFactParams{
		EventKey: testUsageKey("primary-fact"), SourceID: firstSource.ID,
		SessionKey: "shared-label", ModelKey: "shared-model", OccurredAtUnixMS: 1,
		InputTokens: 10, TotalTokens: 10, CostStatus: UsageCostStatusUnknown,
	}
	secondFact := CreateUsageFactParams{
		EventKey: testUsageKey("secondary-fact"), SourceID: secondSource.ID,
		SessionKey: "shared-label", ModelKey: "shared-model", OccurredAtUnixMS: 2,
		InputTokens: 20, TotalTokens: 20, CostStatus: UsageCostStatusUnknown,
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(firstSource, []CreateUsageFactParams{firstFact})); err != nil || result.Inserted != 1 {
		t.Fatalf("insert first-source fact result=%#v err=%v", result, err)
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(secondSource, []CreateUsageFactParams{secondFact})); err != nil || result.Inserted != 1 {
		t.Fatalf("insert second-source fact result=%#v err=%v", result, err)
	}
	report, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "future-agent"})
	if err != nil {
		t.Fatalf("read multi-source report: %v", err)
	}
	if report.Summary.EventCount != 2 || report.Summary.SessionCount != 2 || report.Summary.TotalTokens != 30 {
		t.Fatalf("multi-source summary=%#v", report.Summary)
	}
	if len(report.Models) != 1 || report.Models[0].Model != "shared-model" ||
		report.Models[0].EventCount != 2 || report.Models[0].TotalTokens != 30 {
		t.Fatalf("multi-source models=%#v", report.Models)
	}
	if len(report.Sources) != 2 || report.Sources[0] != "primary-log" || report.Sources[1] != "secondary-log" {
		t.Fatalf("multi-source identities=%#v", report.Sources)
	}
}

func TestCurrentUsageBaselineRejectsLegacyDevelopmentSchemaWithoutMutation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, path, false)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE codex_usage_import_files`,
		`DROP TABLE usage_facts`,
		`DROP TABLE usage_models`,
		`DROP TABLE usage_sessions`,
		`DROP TABLE usage_sources`,
		`CREATE TABLE usage_events (id TEXT PRIMARY KEY, provider_id TEXT NOT NULL)`,
		`CREATE TABLE usage_import_cursors (provider_id TEXT NOT NULL, source_key TEXT NOT NULL)`,
	} {
		if _, err := db.executor().ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy schema fixture: %v", err)
		}
	}
	if result, err := db.Migrate(ctx); err != nil || result.Applied != 0 {
		t.Fatalf("legacy schema migration result=%#v err=%v", result, err)
	}
	status, err := db.Status(ctx)
	if err != nil || status.SchemaHealthy {
		t.Fatalf("legacy schema status=%#v err=%v", status, err)
	}
	var legacyTables, compactTables int
	if err := db.executor().QueryRowContext(ctx, `
		SELECT SUM(name IN ('usage_events', 'usage_import_cursors')),
			SUM(name IN ('usage_sources', 'usage_sessions', 'usage_models', 'usage_facts', 'codex_usage_import_files'))
		FROM sqlite_master WHERE type = 'table'
	`).Scan(&legacyTables, &compactTables); err != nil {
		t.Fatalf("inspect legacy schema: %v", err)
	}
	if legacyTables != 2 || compactTables != 0 {
		t.Fatalf("legacy schema was modified: legacy=%d compact=%d", legacyTables, compactTables)
	}
	closeTestStore(t, db)
	opened, err := NewFactory(path).OpenHealthy(ctx, true)
	if opened != nil {
		_ = opened.Close()
		t.Fatal("legacy schema opened as healthy")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.StoreSchemaInvalid {
		t.Fatalf("legacy schema open error = %v", err)
	}
}
