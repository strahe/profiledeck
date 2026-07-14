package usage

import (
	"context"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func TestUsageReportRangesUndatedAndPartialPricing(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := newUsageTestEnvironment(t, configDir, "").runtime.Init(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db, err := store.Open(ctx, initResult.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected fixture store open, got %v", err)
	}

	location := time.FixedZone("report-test", -7*60*60)
	now := time.Date(2026, time.July, 10, 12, 30, 0, 0, location)
	costToday := int64(1_000_000)
	costMonth := int64(250_000)
	costOld := int64(100_000)
	costUndated := int64(50_000)
	events := []store.CreateUsageEventParams{
		reportEvent("today", "session-today", "gpt-5.3-codex", now.Add(-time.Hour).UnixMilli(), 100, 40, 20, &costToday, store.UsageCostStatusEstimated),
		reportEvent("week", "session-week", "unknown-model", now.AddDate(0, 0, -3).UnixMilli(), 50, 10, 10, nil, store.UsageCostStatusUnknown),
		reportEvent("month", "session-month", "gpt-5.3-codex", now.AddDate(0, 0, -10).UnixMilli(), 30, 0, 5, &costMonth, store.UsageCostStatusEstimated),
		reportEvent("old", "session-old", "gpt-5.3-codex", now.AddDate(-2, 0, 0).UnixMilli(), 20, 0, 5, &costOld, store.UsageCostStatusEstimated),
		reportEvent("undated", "session-undated", "gpt-5.3-codex", 0, 10, 0, 2, &costUndated, store.UsageCostStatusEstimated),
	}
	if result, insertErr := db.InsertUsageEvents(ctx, events); insertErr != nil || result.Inserted != len(events) {
		_ = db.Close()
		t.Fatalf("expected report fixture insert, result=%#v err=%v", result, insertErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("expected fixture store close, got %v", closeErr)
	}

	tests := []struct {
		name       string
		rangeValue UsageRangePreset
		events     int64
		buckets    int
		unit       string
	}{
		{name: "today", rangeValue: UsageRangeToday, events: 1, buckets: 13, unit: "hour"},
		{name: "seven days", rangeValue: UsageRange7Days, events: 2, buckets: 7, unit: "day"},
		{name: "thirty days", rangeValue: UsageRange30Days, events: 3, buckets: 30, unit: "day"},
		{name: "all", rangeValue: UsageRangeAll, events: 5, buckets: 25, unit: "month"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, reportErr := newUsageTestEnvironment(t, configDir, "").service.usageReportAt(ctx, UsageReportRequest{ProviderID: "codex", Range: test.rangeValue}, now)
			if reportErr != nil {
				t.Fatalf("expected report to succeed, got %v", reportErr)
			}
			if report.Summary.EventCount != test.events || len(report.Trend) != test.buckets || report.Range.BucketUnit != test.unit {
				t.Fatalf("unexpected %s report shape: %#v", test.name, report)
			}
			if report.Summary.UndatedEventCount != 1 {
				t.Fatalf("expected undated count to remain visible, got %#v", report.Summary)
			}
		})
	}

	week, err := newUsageTestEnvironment(t, configDir, "").service.usageReportAt(ctx, UsageReportRequest{Range: UsageRange7Days}, now)
	if err != nil {
		t.Fatalf("expected seven-day report, got %v", err)
	}
	if week.Summary.KnownEstimatedCostUSD != "1.000000" || week.Summary.CostStatus != "unknown" || week.Summary.PricingCoverage != float64(2)/3 {
		t.Fatalf("unexpected partial pricing semantics: %#v", week.Summary)
	}
	if len(week.Models) != 2 || week.Models[0].Model != "gpt-5.3-codex" || week.Models[1].Model != "unknown-model" {
		t.Fatalf("unexpected model summary: %#v", week.Models)
	}
	if week.Pricing.Basis != "openai-standard-api" || week.Pricing.HistoricalRepricing {
		t.Fatalf("unexpected pricing provenance: %#v", week.Pricing)
	}
	all, err := newUsageTestEnvironment(t, configDir, "").service.usageReportAt(ctx, UsageReportRequest{Range: UsageRangeAll}, now)
	if err != nil {
		t.Fatalf("expected all-time report, got %v", err)
	}
	if all.Summary.TotalTokens != 252 || all.Summary.SessionCount != 5 || all.Models[0].Summary.UndatedEventCount != 1 {
		t.Fatalf("expected undated usage in all-time totals and models, got %#v", all)
	}
}

func TestUsageReportTodayUsesDSTBoundaries(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("timezone data unavailable: %v", err)
	}
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := newUsageTestEnvironment(t, configDir, "").runtime.Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	for _, test := range []struct {
		name    string
		now     time.Time
		buckets int
	}{
		{name: "spring forward", now: time.Date(2026, time.March, 9, 0, 0, 0, 0, location).Add(-time.Nanosecond), buckets: 23},
		{name: "fall back", now: time.Date(2026, time.November, 2, 0, 0, 0, 0, location).Add(-time.Nanosecond), buckets: 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, reportErr := newUsageTestEnvironment(t, configDir, "").service.usageReportAt(ctx, UsageReportRequest{Range: UsageRangeToday}, test.now)
			if reportErr != nil {
				t.Fatalf("expected DST report, got %v", reportErr)
			}
			if len(report.Trend) != test.buckets || report.Range.TimeZone != location.String() {
				t.Fatalf("unexpected DST buckets: range=%#v trend=%d", report.Range, len(report.Trend))
			}
		})
	}
}

func TestUsageReportEmptyDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := newUsageTestEnvironment(t, configDir, "").runtime.Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	report, err := newUsageTestEnvironment(t, configDir, "").service.usageReportAt(ctx, UsageReportRequest{}, time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("expected empty default report, got %v", err)
	}
	if report.Range.Preset != UsageRange7Days || report.Summary.EventCount != 0 || len(report.Trend) != 7 || len(report.Models) != 0 {
		t.Fatalf("unexpected empty default report: %#v", report)
	}

	_, err = newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{Range: "14d"})
	assertAppErrorCode(t, err, apperror.UsageInvalid)
	_, err = newUsageTestEnvironment(t, configDir, "").service.Report(ctx, UsageReportRequest{ProviderID: "claude"})
	assertAppErrorCode(t, err, apperror.UsageInvalid)
}

func reportEvent(id, sessionID, model string, occurredAt, input, cached, output int64, cost *int64, status string) store.CreateUsageEventParams {
	return store.CreateUsageEventParams{
		ID: id, ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-" + id,
		SessionID: sessionID, Model: model, OccurredAtUnixMS: occurredAt,
		InputTokens: input, CachedInputTokens: cached, OutputTokens: output,
		TotalTokens: input + output, EstimatedCostMicros: cost, CostStatus: status,
	}
}
