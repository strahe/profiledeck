package usage

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCodexPricingBackfillRespectsCacheEvidenceAcrossPolicyDate(t *testing.T) {
	ctx := context.Background()
	environment := newUsageTestEnvironment(t, t.TempDir(), t.TempDir())
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.SyncCodex(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.BeginUsageSync(ctx, ProviderCodex, SourceCodexSessionJSONL, CodexUsageIdentityRevision)
	if err != nil {
		t.Fatal(err)
	}
	zero, writes, retainedCost, retainedVersion := int64(0), int64(100), int64(123), int64(1)
	before := time.Date(2026, 10, 19, 23, 59, 59, 999_000_000, time.UTC).UnixMilli()
	cutoff := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).UnixMilli()
	current := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC).UnixMilli()
	facts := []store.CreateUsageFactParams{
		{EventKey: usageTestEventKey("before-retention-policy"), OccurredAtUnixMS: before, CacheWriteInputTokens: &writes},
		{EventKey: usageTestEventKey("missing-write-count"), OccurredAtUnixMS: cutoff},
		{EventKey: usageTestEventKey("zero-writes"), OccurredAtUnixMS: current, CacheWriteInputTokens: &zero},
		{EventKey: usageTestEventKey("unpriced-writes"), OccurredAtUnixMS: current, CacheWriteInputTokens: &writes},
		{
			EventKey: usageTestEventKey("classified-cost"), OccurredAtUnixMS: current,
			EstimatedCostMicros: &retainedCost, CostStatus: store.UsageCostStatusEstimated,
			PricingCatalogVersion: &retainedVersion,
		},
		{EventKey: usageTestEventKey("unreviewed-model"), ModelKey: "gpt-6.2-sol", OccurredAtUnixMS: current, CacheWriteInputTokens: &writes},
	}
	for i := range facts {
		facts[i].SourceID = source.ID
		facts[i].SessionKey = "session"
		if facts[i].ModelKey == "" {
			facts[i].ModelKey = "gpt-6-sol"
		}
		facts[i].InputTokens = 1_000
		facts[i].CachedInputTokens = 100
		facts[i].OutputTokens = 1_000
		facts[i].TotalTokens = 2_000
	}
	inserted, err := db.InsertUsageFacts(ctx, store.InsertUsageFactsParams{
		SourceID: source.ID, Generation: source.SyncGeneration, Facts: facts,
	})
	if err != nil || inserted.Inserted != len(facts) {
		t.Fatalf("insert historical facts: %+v, %v", inserted, err)
	}
	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID: source.ID, Generation: source.SyncGeneration, CompletedAtUnixMS: current,
		Finalization: &store.CodexUsageSyncFinalization{},
	}); err != nil {
		t.Fatal(err)
	}
	catalog := pricing.Embedded()
	addedUnreviewedModel := false
	for _, entry := range catalog.Entries {
		if entry.Model == "gpt-6-sol" && entry.EffectiveFrom == "2026-10-29" {
			entry.Model = "gpt-6.2-sol"
			catalog.Entries = append(catalog.Entries, entry)
			addedUnreviewedModel = true
			break
		}
	}
	if !addedUnreviewedModel {
		t.Fatal("missing baseline for unreviewed model pricing")
	}
	ctx = withPricingSnapshot(ctx, catalog)

	outcome, err := environment.service.sync(ctx, UsageSyncRequest{ProviderID: ProviderCodex}, SyncOptions{ProvisionMode: SyncExistingProvider})
	if err != nil || !outcome.Performed {
		t.Fatalf("eligible historical facts did not trigger backfill: %+v, %v", outcome, err)
	}
	summary, err := db.UsageSummary(ctx, ProviderCodex)
	if err != nil || summary.EventCount != 6 || summary.UnknownCostEvents != 3 ||
		summary.PartialCostEvents != 1 || summary.EstimatedCostEventCount != 2 || summary.EstimatedCostMicros != 23_613 {
		t.Fatalf("backfill crossed its date or evidence boundary, or repriced classified cost: %+v, %v", summary, err)
	}
	source, err = db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		outcome, err = environment.service.sync(ctx, UsageSyncRequest{ProviderID: ProviderCodex}, SyncOptions{ProvisionMode: SyncExistingProvider})
		if err != nil || outcome.Performed {
			t.Fatalf("unverified cache evidence or model triggered empty backfill: %+v, %v", outcome, err)
		}
		currentSource, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
		if err != nil || currentSource != source {
			t.Fatalf("empty backfill changed completed source state: %+v, %v", currentSource, err)
		}
	}
}

func TestCodexPricingBackfillDoesNotRetryInconsistentCacheCounts(t *testing.T) {
	ctx := context.Background()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, `{"type":"session_meta","session_id":"session-cache-counts"}
{"type":"turn_context","model":"gpt-6.1-sol"}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":100,"cache_write_input_tokens":901,"output_tokens":100,"total_tokens":1100},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":100,"cache_write_input_tokens":901,"output_tokens":100,"total_tokens":1100}}},"timestamp":"2026-10-29T12:00:00Z"}
`)
	environment := newUsageTestEnvironment(t, t.TempDir(), codexDir)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := environment.service.SyncCodex(ctx); err != nil || result.ImportedEvents != 1 {
		t.Fatalf("import inconsistent cache counts: %+v, %v", result, err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := db.UsageSummary(ctx, ProviderCodex)
	if err != nil || summary.EventCount != 1 || summary.UnknownCostEvents != 1 || summary.EstimatedCostMicros != 0 {
		t.Fatalf("inconsistent cache counts received an estimate: %+v, %v", summary, err)
	}
	source, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
	if err != nil {
		t.Fatal(err)
	}
	models, err := db.ListUnknownUsageCostModels(ctx, ProviderCodex)
	if err != nil || len(models) != 1 {
		t.Fatalf("unknown model after import: %+v, %v", models, err)
	}
	facts, err := db.ListUnknownUsageFactCostCandidates(ctx, ProviderCodex, source.ID, store.UsageCostFilter{
		ModelID: models[0].ModelID, UntilUnixMS: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}, 0, 10)
	if err != nil || len(facts) != 1 || facts[0].CacheWriteInputTokens == nil || *facts[0].CacheWriteInputTokens != 901 {
		t.Fatalf("import changed observed cache-write count: %+v, %v", facts, err)
	}
	for range 2 {
		outcome, err := environment.service.sync(ctx, UsageSyncRequest{ProviderID: ProviderCodex}, SyncOptions{ProvisionMode: SyncExistingProvider})
		if err != nil || outcome.Performed {
			t.Fatalf("inconsistent cache counts triggered empty backfill: %+v, %v", outcome, err)
		}
		currentSource, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
		if err != nil || currentSource != source {
			t.Fatalf("empty backfill changed source state: %+v, %v", currentSource, err)
		}
	}
}

func TestHaikuPricingBackfillWaitsForCompleteUsageInSupportedPeriod(t *testing.T) {
	for _, test := range []struct {
		name      string
		final     bool
		before    bool
		wantWork  bool
		wantCost  int64
		wantKnown int64
	}{
		{name: "partial request", final: false},
		{name: "complete request", final: true, wantWork: true, wantCost: 13_750, wantKnown: 1},
		{name: "before verified prices", final: true, before: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			line := claudeHaikuUsageLine(t, 101_000, test.final)
			if test.before {
				line = bytes.Replace(line, []byte("2026-10-29"), []byte("2026-10-28"), 1)
			}
			writeTestFile(t, filepath.Join(dir, "projects", "project", "session.jsonl"), string(line))
			catalog := pricing.Embedded()
			entries := make([]pricing.Entry, 0, len(catalog.Entries))
			for _, entry := range catalog.Entries {
				if entry.Model != "claude-haiku-5-5" {
					entries = append(entries, entry)
				}
			}
			catalog.Entries = entries
			integration := NewClaudeCodeIntegrationWithProvisioner(dir, claudeTestProvisioner{})
			if _, err := integration.Sync(withPricingSnapshot(ctx, catalog), factory, SyncOptions{ProvisionMode: SyncProvisionProvider}); err != nil {
				t.Fatal(err)
			}
			source, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
			if err != nil || outcome.Performed != test.wantWork {
				t.Fatalf("wrong backfill eligibility: %+v, %v", outcome, err)
			}
			summary, err := db.UsageSummary(ctx, "claude-code")
			if err != nil || summary.EventCount != 1 || summary.UnknownCostEvents != 1-test.wantKnown || summary.EstimatedCostMicros != test.wantCost {
				t.Fatalf("wrong completed-request pricing: %+v, %v", summary, err)
			}
			completed, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
			if err != nil || !test.wantWork && completed != source {
				t.Fatalf("ineligible request changed source state: %+v, %v", completed, err)
			}
			for range 2 {
				outcome, err = integration.Sync(ctx, factory, SyncOptions{ProvisionMode: SyncExistingProvider})
				if err != nil || outcome.Performed {
					t.Fatalf("request triggered repeated empty backfill: %+v, %v", outcome, err)
				}
				currentSource, err := db.GetUsageSource(ctx, "claude-code", SourceClaudeCodeSessionJSONL)
				if err != nil || currentSource != completed {
					t.Fatalf("repeated backfill changed source state: %+v, %v", currentSource, err)
				}
			}
		})
	}
}
