package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageSchemaSupportsPartialCost(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source to initialize, got %v", err)
	}
	partialCost := int64(42)
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{{
		EventKey: testUsageKey("partial-usage"), SourceID: source.ID,
		SessionKey: "session", ModelKey: "gpt-5.6-sol", TotalTokens: 1,
		EstimatedCostMicros: &partialCost, CostStatus: UsageCostStatusPartial,
	}})); err != nil || result.Inserted != 1 {
		t.Fatalf("expected base usage schema to accept partial cost, result=%#v err=%v", result, err)
	}
	storeStatus, err := db.Status(ctx)
	if err != nil || !storeStatus.SchemaHealthy {
		t.Fatalf("expected usage schema to remain healthy, status=%#v err=%v", storeStatus, err)
	}
}

func TestUsageFactsAreIdempotentAndSummarized(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	createUsageProviderFixture(t, ctx, db, "codex")
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected usage source, got %v", err)
	}

	cost := int64(123)
	params := []CreateUsageFactParams{{
		EventKey:            testUsageKey("event-1"),
		SourceID:            source.ID,
		SessionKey:          "session-1",
		ModelKey:            "gpt-5.3-codex",
		InputTokens:         10,
		CachedInputTokens:   2,
		OutputTokens:        3,
		TotalTokens:         13,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
	}}

	first, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, params))
	if err != nil {
		t.Fatalf("expected first usage insert to succeed, got %v", err)
	}
	if first.Inserted != 1 || first.Duplicates != 0 {
		t.Fatalf("expected inserted=1 duplicates=0, got %#v", first)
	}
	second, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, params))
	if err != nil {
		t.Fatalf("expected duplicate usage insert to succeed, got %v", err)
	}
	if second.Inserted != 0 || second.Duplicates != 1 {
		t.Fatalf("expected inserted=0 duplicates=1, got %#v", second)
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 10 || summary.CachedInputTokens != 2 || summary.OutputTokens != 3 || summary.TotalTokens != 13 {
		t.Fatalf("unexpected usage summary: %#v", summary)
	}
	if summary.EstimatedCostMicros != cost || summary.UnknownCostEvents != 0 || summary.EstimatedCostEventCount != 1 {
		t.Fatalf("unexpected usage cost summary: %#v", summary)
	}
	if len(summary.Sources) != 1 || summary.Sources[0] != "codex-session-jsonl" {
		t.Fatalf("unexpected usage sources: %#v", summary.Sources)
	}
}

func TestUsageFactsDeduplicateStableKeyWithEarlierObservationAndMonotonicCost(t *testing.T) {
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

	cost := int64(123)
	forkCopy := CreateUsageFactParams{
		EventKey: testUsageKey("stable-event"), SourceID: source.ID,
		SessionKey: "session-parent", ModelKey: "gpt-5.3-codex", OccurredAtUnixMS: 2_000,
		InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13,
		EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated,
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{forkCopy})); err != nil || result.Inserted != 1 {
		t.Fatalf("expected fork observation insert, result=%#v err=%v", result, err)
	}
	parent := forkCopy
	parent.ModelKey = "openai/gpt-5.3-codex-2026-07-01"
	parent.OccurredAtUnixMS = 1_000
	parent.EstimatedCostMicros = nil
	parent.CostStatus = UsageCostStatusUnknown
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{parent})); err != nil || result.Inserted != 0 || result.Duplicates != 1 {
		t.Fatalf("expected parent observation to share the stable event ID, result=%#v err=%v", result, err)
	}

	var model string
	var occurredAt int64
	var estimatedCost sql.NullInt64
	var costStatus int
	if err := db.executor().QueryRowContext(ctx, `SELECT m.model_key, f.occurred_at_unix_ms,
		f.estimated_cost_micros, f.cost_status
		FROM usage_facts f
		JOIN usage_models m ON m.id = f.model_id
		WHERE f.event_key = ?`, forkCopy.EventKey,
	).Scan(&model, &occurredAt, &estimatedCost, &costStatus); err != nil {
		t.Fatalf("expected canonical usage event, got %v", err)
	}
	if model != parent.ModelKey || occurredAt != 1_000 || !estimatedCost.Valid || estimatedCost.Int64 != cost || costStatus != int(UsageCostStatusEstimated) {
		t.Fatalf("expected earliest observation fields on the existing fact, model=%q occurred=%d cost=%#v status=%d", model, occurredAt, estimatedCost, costStatus)
	}

	unknownFirst := parent
	unknownFirst.EventKey = testUsageKey("stable-event-unknown-first")
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{unknownFirst})); err != nil || result.Inserted != 1 {
		t.Fatalf("expected unknown parent observation insert, result=%#v err=%v", result, err)
	}
	pricedLater := forkCopy
	pricedLater.EventKey = unknownFirst.EventKey
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{pricedLater})); err != nil || result.Inserted != 0 || result.Duplicates != 1 {
		t.Fatalf("expected priced fork observation to share the stable event ID, result=%#v err=%v", result, err)
	}
	if err := db.executor().QueryRowContext(ctx, `SELECT m.model_key, f.occurred_at_unix_ms,
		f.estimated_cost_micros, f.cost_status
		FROM usage_facts f
		JOIN usage_models m ON m.id = f.model_id
		WHERE f.event_key = ?`, unknownFirst.EventKey,
	).Scan(&model, &occurredAt, &estimatedCost, &costStatus); err != nil {
		t.Fatalf("expected upgraded canonical usage event, got %v", err)
	}
	if model != unknownFirst.ModelKey || occurredAt != unknownFirst.OccurredAtUnixMS || !estimatedCost.Valid ||
		estimatedCost.Int64 != cost || costStatus != int(UsageCostStatusEstimated) {
		t.Fatalf("expected later duplicate to upgrade only cost, model=%q occurred=%d cost=%#v status=%d", model, occurredAt, estimatedCost, costStatus)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.EventCount != 2 || summary.TotalTokens != 26 || summary.EstimatedCostEventCount != 2 {
		t.Fatalf("expected two independently keyed facts with monotonic costs, summary=%#v err=%v", summary, err)
	}
}

func TestUsageFactsPreferDatedCanonicalObservation(t *testing.T) {
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

	tests := []struct {
		name        string
		firstModel  string
		firstTime   int64
		secondModel string
		secondTime  int64
		wantModel   string
		wantTime    int64
	}{
		{
			name:       "dated observation replaces undated",
			firstModel: "undated-model", firstTime: 0,
			secondModel: "dated-model", secondTime: 1_000,
			wantModel: "dated-model", wantTime: 1_000,
		},
		{
			name:       "undated observation cannot replace dated",
			firstModel: "dated-model", firstTime: 1_000,
			secondModel: "undated-model", secondTime: 0,
			wantModel: "dated-model", wantTime: 1_000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := CreateUsageFactParams{
				EventKey: testUsageKey(test.name), SourceID: source.ID,
				SessionKey: "session", ModelKey: test.firstModel, OccurredAtUnixMS: test.firstTime,
				InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13,
				CostStatus: UsageCostStatusUnknown,
			}
			if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{fact})); err != nil || result.Inserted != 1 {
				t.Fatalf("insert first observation: result=%#v err=%v", result, err)
			}
			fact.ModelKey = test.secondModel
			fact.OccurredAtUnixMS = test.secondTime
			if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{fact})); err != nil || result.Duplicates != 1 {
				t.Fatalf("deduplicate second observation: result=%#v err=%v", result, err)
			}

			var model string
			var occurredAt int64
			if err := db.executor().QueryRowContext(ctx, `SELECT m.model_key, f.occurred_at_unix_ms
				FROM usage_facts f
				JOIN usage_models m ON m.id = f.model_id
				WHERE f.event_key = ?`, fact.EventKey,
			).Scan(&model, &occurredAt); err != nil {
				t.Fatalf("read canonical observation: %v", err)
			}
			if model != test.wantModel || occurredAt != test.wantTime {
				t.Fatalf("canonical observation model=%q time=%d, want model=%q time=%d", model, occurredAt, test.wantModel, test.wantTime)
			}
		})
	}
}

func TestUsageSummaryReportsDistinctSources(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	createUsageProviderFixture(t, ctx, db, "codex")
	archiveSource, err := db.BeginUsageSync(ctx, "codex", "codex-archive-jsonl", 1)
	if err != nil {
		t.Fatalf("expected archive usage source, got %v", err)
	}
	sessionSource, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("expected session usage source, got %v", err)
	}

	cost := int64(1)
	archiveFact := CreateUsageFactParams{
		EventKey:            testUsageKey("event-1"),
		SourceID:            archiveSource.ID,
		InputTokens:         1,
		TotalTokens:         1,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
	}
	sessionFact := CreateUsageFactParams{
		EventKey:            testUsageKey("event-2"),
		SourceID:            sessionSource.ID,
		InputTokens:         1,
		TotalTokens:         1,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
	}
	archiveResult, err := db.InsertUsageFacts(ctx, testUsageFactBatch(archiveSource, []CreateUsageFactParams{archiveFact}))
	if err != nil || archiveResult.Inserted != 1 {
		t.Fatalf("expected archive usage event insert to succeed, result=%#v err=%v", archiveResult, err)
	}
	sessionResult, err := db.InsertUsageFacts(ctx, testUsageFactBatch(sessionSource, []CreateUsageFactParams{sessionFact}))
	if err != nil || sessionResult.Inserted != 1 {
		t.Fatalf("expected session usage event insert to succeed, result=%#v err=%v", sessionResult, err)
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if strings.Join(summary.Sources, ",") != "codex-archive-jsonl,codex-session-jsonl" {
		t.Fatalf("unexpected sorted usage sources: %#v", summary.Sources)
	}
}

func TestUpdateUnknownUsageFactCostsIsFilteredAtomicAndIdempotent(t *testing.T) {
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
	facts := []CreateUsageFactParams{
		{EventKey: testUsageKey("candidate-a"), SourceID: source.ID, ModelKey: "gpt-5.6-sol", InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110, CostStatus: UsageCostStatusUnknown},
		{EventKey: testUsageKey("candidate-b"), SourceID: source.ID, ModelKey: "other-model", InputTokens: 50, OutputTokens: 5, TotalTokens: 55, CostStatus: UsageCostStatusUnknown},
	}
	if result, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, facts)); err != nil || result.Inserted != len(facts) {
		t.Fatalf("expected usage candidates, result=%#v err=%v", result, err)
	}

	models, err := db.ListUnknownUsageCostModels(ctx, "codex")
	if err != nil || len(models) != 2 || models[0].Model != "gpt-5.6-sol" || models[1].Model != "other-model" {
		t.Fatalf("unexpected unknown-cost models: %#v err=%v", models, err)
	}
	candidates, err := db.ListUnknownUsageFactCostCandidates(ctx, "codex", models[0].SourceID, models[0].ModelID, 0, 10)
	if err != nil || len(candidates) != 1 || candidates[0].CachedInputTokens != 20 {
		t.Fatalf("unexpected paged candidates: %#v err=%v", candidates, err)
	}
	updated, err := db.UpdateUnknownUsageFactCosts(ctx, "codex", []UpdateUsageFactCostParams{{
		ID: candidates[0].ID, EstimatedCostMicros: 123, CostStatus: UsageCostStatusPartial,
	}})
	if err != nil || updated != 1 {
		t.Fatalf("expected one partial cost update, updated=%d err=%v", updated, err)
	}
	updated, err = db.UpdateUnknownUsageFactCosts(ctx, "codex", []UpdateUsageFactCostParams{{
		ID: candidates[0].ID, EstimatedCostMicros: 456, CostStatus: UsageCostStatusPartial,
	}})
	if err != nil || updated != 0 {
		t.Fatalf("expected repeated backfill not to overwrite classified cost, updated=%d err=%v", updated, err)
	}
	var cost int64
	var status int
	if err := db.executor().QueryRowContext(ctx, "SELECT estimated_cost_micros, cost_status FROM usage_facts WHERE id = ?", candidates[0].ID).Scan(&cost, &status); err != nil {
		t.Fatalf("expected updated usage event, got %v", err)
	}
	if cost != 123 || status != int(UsageCostStatusPartial) {
		t.Fatalf("unexpected persisted partial cost: cost=%d status=%d", cost, status)
	}
	report, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex", EndUnixMS: 1})
	if err != nil {
		t.Fatalf("expected usage report after partial update, got %v", err)
	}
	if report.Summary.EstimatedCostMicros != 123 || report.Summary.EstimatedTokenCount != 110 ||
		report.Summary.EstimatedCostEventCount != 0 || report.Summary.PartialCostEventCount != 1 || report.Summary.UnknownCostEvents != 1 {
		t.Fatalf("unexpected partial cost aggregate: %#v", report.Summary)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.PartialCostEvents != 1 || summary.UnknownCostEvents != 1 || summary.EstimatedCostMicros != 123 {
		t.Fatalf("unexpected partial legacy summary: %#v err=%v", summary, err)
	}

	models, err = db.ListUnknownUsageCostModels(ctx, "codex")
	if err != nil || len(models) != 1 || models[0].Model != "other-model" {
		t.Fatalf("expected only the unclassified model, models=%#v err=%v", models, err)
	}
	other, err := db.ListUnknownUsageFactCostCandidates(ctx, "codex", models[0].SourceID, models[0].ModelID, 0, 10)
	if err != nil || len(other) != 1 {
		t.Fatalf("expected second unknown candidate, candidates=%#v err=%v", other, err)
	}
	_, err = db.UpdateUnknownUsageFactCosts(ctx, "codex", []UpdateUsageFactCostParams{
		{ID: other[0].ID, EstimatedCostMicros: 99, CostStatus: UsageCostStatusEstimated},
		{ID: 0, EstimatedCostMicros: 1, CostStatus: UsageCostStatusPartial},
	})
	if err == nil {
		t.Fatalf("expected invalid batch to fail")
	}
	if err := db.executor().QueryRowContext(ctx, "SELECT cost_status FROM usage_facts WHERE id = ?", other[0].ID).Scan(&status); err != nil {
		t.Fatalf("expected rolled back usage event, got %v", err)
	}
	if status != int(UsageCostStatusUnknown) {
		t.Fatalf("expected invalid batch to roll back, got %d", status)
	}
}
