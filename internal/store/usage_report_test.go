package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"
)

type usageSummarySnapshotBarrier struct {
	reached     chan struct{}
	reachedOnce sync.Once
	release     chan struct{}
}

var (
	usageSummaryBarrierRegistration struct {
		sync.Once
		err error
	}
	usageSummarySnapshotBarriers sync.Map
)

func registerUsageSummarySnapshotBarrier(t *testing.T) {
	t.Helper()
	usageSummaryBarrierRegistration.Do(func() {
		usageSummaryBarrierRegistration.err = sqlite.RegisterScalarFunction(
			"profiledeck_usage_summary_snapshot_barrier",
			1,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("usage summary snapshot barrier requires one argument")
				}
				sourceKey, ok := args[0].(string)
				if !ok {
					return args[0], nil
				}
				if value, ok := usageSummarySnapshotBarriers.Load(sourceKey); ok {
					barrier := value.(*usageSummarySnapshotBarrier)
					barrier.reachedOnce.Do(func() { close(barrier.reached) })
					<-barrier.release
				}
				return sourceKey, nil
			},
		)
	})
	if usageSummaryBarrierRegistration.err != nil {
		t.Fatalf("register usage summary snapshot barrier: %v", usageSummaryBarrierRegistration.err)
	}
}

func TestUsageReportAggregatesRangeModelsBucketsAndImportHealth(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}

	cost10 := int64(10)
	cost20 := int64(20)
	cost5 := int64(5)
	facts := []CreateUsageFactParams{
		{EventKey: testUsageKey("event-a1"), SourceID: source.ID, SessionKey: "session-a", ModelKey: "model-a", OccurredAtUnixMS: 1_000, InputTokens: 100, CachedInputTokens: 40, OutputTokens: 20, TotalTokens: 120, EstimatedCostMicros: &cost10, CostStatus: UsageCostStatusEstimated},
		{EventKey: testUsageKey("event-a2"), SourceID: source.ID, SessionKey: "session-a", ModelKey: "model-a", OccurredAtUnixMS: 1_500, InputTokens: 50, CachedInputTokens: 10, OutputTokens: 10, TotalTokens: 60, CostStatus: UsageCostStatusUnknown},
		{EventKey: testUsageKey("event-b1"), SourceID: source.ID, SessionKey: "session-b", ModelKey: "model-b", OccurredAtUnixMS: 2_500, InputTokens: 80, CachedInputTokens: 80, OutputTokens: 20, TotalTokens: 100, EstimatedCostMicros: &cost20, CostStatus: UsageCostStatusEstimated},
		{EventKey: testUsageKey("event-undated"), SourceID: source.ID, SessionKey: "session-c", ModelKey: "model-b", InputTokens: 30, OutputTokens: 5, TotalTokens: 35, EstimatedCostMicros: &cost5, CostStatus: UsageCostStatusEstimated},
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, facts)); err != nil || result.Inserted != len(facts) {
		t.Fatalf("expected usage fixture insert, result=%#v err=%v", result, err)
	}
	fileKey := testUsageKey("source-a")
	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: CodexUsageImportFile{
		SourceID: source.ID, FileKey: fileKey, SizeBytes: 100,
		InvalidLines: 3, UnsupportedLines: 4, ParserRevision: 1, IdentityRevision: 1,
		EventDigest: testUsageKey("cursor"),
	}})); err != nil {
		t.Fatalf("expected import cursor fixture, got %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: source.ID, Generation: source.SyncGeneration, CompletedAtUnixMS: 123,
		Finalization: &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{fileKey}},
	}); err != nil {
		t.Fatalf("expected usage source summary fixture, got %v", err)
	}

	start := int64(1_000)
	report, err := db.UsageReport(ctx, UsageReportQuery{
		ProviderID:  "codex",
		StartUnixMS: &start,
		EndUnixMS:   3_000,
		Buckets: []UsageTimeBucket{
			{StartUnixMS: 1_000, EndUnixMS: 2_000},
			{StartUnixMS: 2_000, EndUnixMS: 2_400},
			{StartUnixMS: 2_400, EndUnixMS: 3_000},
		},
	})
	if err != nil {
		t.Fatalf("expected usage report query, got %v", err)
	}
	if report.Summary.EventCount != 3 || report.Summary.SessionCount != 2 || report.Summary.FreshInputTokens != 100 || report.Summary.TotalTokens != 280 {
		t.Fatalf("unexpected ranged aggregate: %#v", report.Summary)
	}
	if report.Summary.EstimatedCostMicros != 30 || report.Summary.EstimatedTokenCount != 220 || report.Summary.UnknownCostEvents != 1 || report.Summary.UndatedEventCount != 1 {
		t.Fatalf("unexpected ranged cost and undated aggregate: %#v", report.Summary)
	}
	if len(report.Trend) != 3 || report.Trend[0].TotalTokens != 180 || report.Trend[1].EventCount != 0 || report.Trend[2].TotalTokens != 100 {
		t.Fatalf("unexpected zero-filled trend: %#v", report.Trend)
	}
	for _, point := range report.Trend {
		if point.UndatedEventCount != 0 {
			t.Fatalf("undated facts must not appear in trend buckets: %#v", report.Trend)
		}
	}
	if len(report.Models) != 2 || report.Models[0].Model != "model-a" || report.Models[0].SessionCount != 1 || report.Models[1].Model != "model-b" {
		t.Fatalf("unexpected model summary or ordering: %#v", report.Models)
	}
	if report.ImportSummary.TrackedFiles != 1 || report.ImportSummary.InvalidLines != 3 || report.ImportSummary.UnsupportedLines != 4 || report.ImportSummary.LastSyncedAtUnixMS == 0 {
		t.Fatalf("unexpected import summary: %#v", report.ImportSummary)
	}

	all, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex", EndUnixMS: 3_000})
	if err != nil {
		t.Fatalf("expected all-time usage report, got %v", err)
	}
	if all.Summary.EventCount != 4 || all.Summary.SessionCount != 3 || all.Summary.UndatedEventCount != 1 || all.Summary.TotalTokens != 315 {
		t.Fatalf("expected undated event in all-time totals, got %#v", all.Summary)
	}
}

func TestUsageSummaryPreservesPersistedCostStatusMeanings(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate usage store: %v", err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	model, err := db.executor().ExecContext(ctx, `
		INSERT INTO usage_models (source_id, model_key) VALUES (?, ?)
	`, source.ID, "model")
	if err != nil {
		t.Fatalf("insert usage model: %v", err)
	}
	modelID, err := model.LastInsertId()
	if err != nil {
		t.Fatalf("read usage model ID: %v", err)
	}
	if _, err := db.executor().ExecContext(ctx, `
		INSERT INTO usage_facts (
			event_key, source_id, model_id, total_tokens, estimated_cost_micros, cost_status
		) VALUES
			(?, ?, ?, 10, NULL, 0),
			(?, ?, ?, 20, 2, 1),
			(?, ?, ?, 30, 3, 2)
	`,
		testUsageKey("persisted-unknown"), source.ID, modelID,
		testUsageKey("persisted-estimated"), source.ID, modelID,
		testUsageKey("persisted-partial"), source.ID, modelID,
	); err != nil {
		t.Fatalf("insert persisted cost statuses: %v", err)
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("read usage summary: %v", err)
	}
	if summary.UnknownCostEvents != 1 || summary.EstimatedCostEventCount != 1 ||
		summary.PartialCostEvents != 1 || summary.EstimatedCostMicros != 5 {
		t.Fatalf("unexpected persisted cost status semantics: %#v", summary)
	}
}

func TestUsageSummaryKeepsSourcesAndFactsInOneSnapshot(t *testing.T) {
	registerUsageSummarySnapshotBarrier(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate usage store: %v", err)
	}
	rawDB := db.db.DB
	rawDB.SetMaxOpenConns(1)
	var journalMode string
	if err := rawDB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL mode for concurrent snapshot test, mode=%q err=%v", journalMode, err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	writer := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, writer)

	if _, err := rawDB.ExecContext(ctx, `
		CREATE TEMP VIEW usage_sources AS
		SELECT id, provider_id,
			profiledeck_usage_summary_snapshot_barrier(source_key) AS source_key,
			identity_revision, sync_generation, last_completed_at_unix_ms,
			tracked_units, invalid_records, unsupported_records
		FROM main.usage_sources
	`); err != nil {
		t.Fatalf("create usage summary snapshot fixture: %v", err)
	}
	barrier := &usageSummarySnapshotBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	usageSummarySnapshotBarriers.Store(source.SourceKey, barrier)
	defer usageSummarySnapshotBarriers.Delete(source.SourceKey)
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()

	type summaryResult struct {
		summary UsageSummary
		err     error
	}
	resultCh := make(chan summaryResult, 1)
	go func() {
		summary, summaryErr := db.UsageSummary(ctx, "codex")
		resultCh <- summaryResult{summary: summary, err: summaryErr}
	}()
	select {
	case <-barrier.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("usage summary did not reach the snapshot barrier")
	}
	if result, err := writer.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{{
		EventKey: testUsageKey("summary-concurrent-fact"), SourceID: source.ID,
		SessionKey: "session", ModelKey: "model", InputTokens: 10, TotalTokens: 10,
		CostStatus: UsageCostStatusUnknown,
	}})); err != nil || result.Inserted != 1 {
		t.Fatalf("insert concurrent usage fact: result=%#v err=%v", result, err)
	}
	close(barrier.release)
	released = true

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("read usage summary: %v", result.err)
	}
	if result.summary.EventCount != 0 || len(result.summary.Sources) != 0 {
		t.Fatalf("usage summary mixed source and fact snapshots: %#v", result.summary)
	}
	after, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("read usage summary after concurrent import: %v", err)
	}
	if after.EventCount != 1 || len(after.Sources) != 1 || after.Sources[0] != source.SourceKey {
		t.Fatalf("expected concurrent fact after snapshot completed: %#v", after)
	}
}

func TestUsageReportTransactionKeepsAConsistentReadSnapshot(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	var journalMode string
	if err := db.db.DB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL mode for concurrent snapshot test, mode=%q err=%v", journalMode, err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}

	cost := int64(1)
	first := CreateUsageFactParams{EventKey: testUsageKey("snapshot-first"), SourceID: source.ID, SessionKey: "session-first", ModelKey: "model-a", OccurredAtUnixMS: 1_000, InputTokens: 10, TotalTokens: 10, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{first})); err != nil || result.Inserted != 1 {
		t.Fatalf("expected first snapshot fixture, result=%#v err=%v", result, err)
	}

	writer := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, writer)
	second := first
	second.EventKey = testUsageKey("snapshot-second")
	second.SessionKey = "session-second"
	second.OccurredAtUnixMS = 1_500
	start := int64(500)
	var snapshot UsageReportSnapshot
	err = db.WithTransaction(ctx, func(txStore *Store) error {
		if _, err := txStore.EarliestDatedUsageUnixMS(ctx, "codex"); err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		writeDone := make(chan error, 1)
		go func() {
			_, writeErr := writer.InsertUsageFacts(writeCtx, testUsageFactBatch(source, []CreateUsageFactParams{second}))
			writeDone <- writeErr
		}()
		if err := <-writeDone; err != nil {
			return fmt.Errorf("concurrent fixture write failed: %w", err)
		}
		var reportErr error
		snapshot, reportErr = txStore.UsageReport(ctx, UsageReportQuery{
			ProviderID:  "codex",
			StartUnixMS: &start,
			EndUnixMS:   2_000,
			Buckets:     []UsageTimeBucket{{StartUnixMS: 500, EndUnixMS: 2_000}},
		})
		return reportErr
	})
	if err != nil {
		t.Fatalf("expected concurrent snapshot report, got %v", err)
	}
	if snapshot.Summary.EventCount != 1 || snapshot.Trend[0].EventCount != 1 || len(snapshot.Models) != 1 {
		t.Fatalf("expected all report queries to retain the first snapshot, got %#v", snapshot)
	}
	after, err := db.UsageSummary(ctx, "codex")
	if err != nil || after.EventCount != 2 {
		t.Fatalf("expected concurrent event after report transaction, summary=%#v err=%v", after, err)
	}
}

func TestUsageReportPreservesSafeAndRejectsUnsafeModelLabels(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}
	cost := int64(1)
	facts := []CreateUsageFactParams{
		{EventKey: testUsageKey("unsafe-model"), SourceID: source.ID, SessionKey: "session-a", ModelKey: "SECRET PROMPT VALUE", InputTokens: 1, TotalTokens: 1, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated},
		{EventKey: testUsageKey("blank-model"), SourceID: source.ID, SessionKey: "session-b", InputTokens: 1, TotalTokens: 1, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated},
		{EventKey: testUsageKey("safe-model"), SourceID: source.ID, SessionKey: "session-c", ModelKey: "OpenAI/GPT-5.3-Codex", InputTokens: 3, TotalTokens: 3, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated},
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, facts)); err != nil || result.Inserted != 3 {
		t.Fatalf("expected model fixtures, result=%#v err=%v", result, err)
	}
	report, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected safe usage report, got %v", err)
	}
	if len(report.Models) != 2 || report.Models[0].Model != "OpenAI/GPT-5.3-Codex" || report.Models[0].EventCount != 1 ||
		report.Models[1].Model != "unknown" || report.Models[1].EventCount != 2 {
		t.Fatalf("expected exact safe labels and merged unsafe labels, got %#v", report.Models)
	}
}
