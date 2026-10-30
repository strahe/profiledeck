package usage

import (
	"math"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
)

func TestEventIDUsesStableLogicalDimensions(t *testing.T) {
	tokens := TokenCounts{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13}
	base := EventID(ProviderCodex, SourceCodexSessionJSONL, 1, "session-a", "openai/gpt-5.3-codex-2026-07-01", tokens)
	if base.IsZero() {
		t.Fatalf("expected a stable event ID")
	}
	if normalized := EventID(ProviderCodex, SourceCodexSessionJSONL, 1, "session-a", "gpt-5.3-codex", tokens); normalized != base {
		t.Fatalf("expected equivalent model labels to keep the same event ID")
	}
	for name, candidate := range map[string]store.UsageKey{
		"provider": EventID("other", SourceCodexSessionJSONL, 1, "session-a", "gpt-5.3-codex", tokens),
		"source":   EventID(ProviderCodex, "other-source", 1, "session-a", "gpt-5.3-codex", tokens),
		"session":  EventID(ProviderCodex, SourceCodexSessionJSONL, 1, "session-b", "gpt-5.3-codex", tokens),
		"ordinal":  EventID(ProviderCodex, SourceCodexSessionJSONL, 2, "session-a", "gpt-5.3-codex", tokens),
	} {
		if candidate == base {
			t.Fatalf("expected %s to scope event identity", name)
		}
	}
}

func TestEstimateCostMicrosCoversCurrentOpenAIAndCodexModels(t *testing.T) {
	tokens := TokenCounts{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      1_000_000,
		TotalTokens:       2_000_000,
	}
	tests := []struct {
		model string
		want  int64
	}{
		{model: "gpt-5.5", want: 34_550_000},
		{model: "gpt-5.4", want: 17_275_000},
		{model: "gpt-5.4-mini", want: 5_182_500},
		{model: "gpt-5.4-nano", want: 1_432_000},
		{model: "gpt-5.3-codex", want: 15_592_500},
		{model: "gpt-5.2", want: 15_592_500},
		{model: "gpt-5.1", want: 11_137_500},
		{model: "gpt-5", want: 11_137_500},
		{model: "gpt-5-mini", want: 2_227_500},
		{model: "gpt-5-nano", want: 445_500},
		{model: "gpt-4.1", want: 9_850_000},
		{model: "gpt-4.1-mini", want: 1_970_000},
		{model: "gpt-4.1-nano", want: 492_500},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, status := EstimateCostMicros(tt.model, tokens)
			if status != CostStatusEstimated || got == nil {
				t.Fatalf("expected estimated cost for %s, got status=%q cost=%v", tt.model, status, got)
			}
			if *got != tt.want {
				t.Fatalf("expected cost %d, got %d", tt.want, *got)
			}
		})
	}
}

func TestEstimateCostMicrosRequiresCacheWriteEvidence(t *testing.T) {
	tokens := TokenCounts{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      1_000_000,
		TotalTokens:       2_000_000,
	}
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		t.Run(model, func(t *testing.T) {
			got, status := EstimateCostMicros(model, tokens)
			if status != CostStatusUnknown || got != nil {
				t.Fatalf("expected missing cache-write evidence to remain unknown, got status=%q cost=%v", status, got)
			}
		})
	}
}

func TestCodexCostUsesCacheWriteEvidenceAndEventDate(t *testing.T) {
	zero, writes, perCall := int64(0), int64(25_000), int64(300_000)
	atLimit, aboveLimit := int64(272_000), int64(272_001)
	for _, test := range []struct {
		name       string
		model      string
		date       string
		cacheWrite *int64
		perCall    *int64
		input      int64
		want       int64
		status     store.UsageCostStatus
		singleRate bool
	}{
		{name: "before retention choice", model: "gpt-6-astra", date: "2026-10-19T23:59:59Z", cacheWrite: &writes, perCall: &perCall, want: 5_975_000, status: CostStatusEstimated},
		{name: "zero before retention choice", model: "gpt-6-astra", date: "2026-10-19T23:59:59Z", cacheWrite: &zero, perCall: &perCall, status: CostStatusUnknown},
		{name: "missing count before retention choice", model: "gpt-6-astra", date: "2026-10-19T23:59:59Z", perCall: &perCall, status: CostStatusUnknown},
		{name: "retention choice starts", model: "gpt-6-astra", date: "2026-10-20T00:00:00Z", cacheWrite: &writes, perCall: &perCall, want: 5_350_000, status: CostStatusPartial},
		{name: "unverified zero", model: "gpt-6-astra", date: "2026-10-20T00:00:00Z", cacheWrite: &zero, perCall: &perCall, status: CostStatusUnknown},
		{name: "missing count", model: "gpt-6-astra", date: "2026-10-20T00:00:00Z", perCall: &perCall, status: CostStatusUnknown},
		{name: "missing call evidence", model: "gpt-6-astra", date: "2026-10-20T00:00:00Z", cacheWrite: &writes, want: 2_800_000, status: CostStatusPartial},
		{name: "sol zero lacks source evidence", model: "gpt-6.1-sol", date: "2026-10-29T00:00:00Z", cacheWrite: &zero, perCall: &perCall, status: CostStatusUnknown},
		{name: "sol at short-context limit", model: "gpt-6.1-sol", date: "2026-10-29T00:00:00Z", cacheWrite: &writes, perCall: &atLimit, input: atLimit, want: 499_000, status: CostStatusPartial},
		{name: "sol above short-context limit", model: "gpt-6.1-sol", date: "2026-10-29T00:00:00Z", cacheWrite: &writes, perCall: &aboveLimit, input: aboveLimit, want: 948_004, status: CostStatusPartial},
		{name: "sol writes lack retention", model: "gpt-6.1-sol", date: "2026-10-29T00:00:00Z", cacheWrite: &writes, perCall: &perCall, want: 1_060_000, status: CostStatusPartial, singleRate: true},
		{name: "terra zero lacks source evidence", model: "gpt-6.1-terra", date: "2026-10-29T00:00:00Z", cacheWrite: &zero, perCall: &perCall, status: CostStatusUnknown},
		{name: "terra starts at verification", model: "gpt-6.1-terra", date: "2026-10-29T00:00:00Z", cacheWrite: &writes, perCall: &perCall, want: 530_000, status: CostStatusPartial},
		{name: "unverified historical sol", model: "gpt-6.1-sol", date: "2026-10-28T23:59:59Z", cacheWrite: &zero, perCall: &perCall, status: CostStatusUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, test.date)
			if err != nil {
				t.Fatal(err)
			}
			catalog := pricing.Embedded()
			if test.singleRate {
				short, long := "2.5", "5"
				for i := range catalog.Entries {
					if catalog.Entries[i].Model == test.model {
						catalog.Entries[i].ShortContext.CacheWrite = &short
						catalog.Entries[i].LongContext.Rates.CacheWrite = &long
					}
				}
			}
			input := test.input
			if input == 0 {
				input = 300_000
			}
			cost, status, version := estimateCostAt(catalog, ProviderCodex, test.model, at.UnixMilli(), TokenCounts{
				InputTokens: input, CachedInputTokens: 50_000, OutputTokens: 10_000, TotalTokens: input + 10_000,
			}, test.cacheWrite, test.perCall)
			if status != test.status {
				t.Fatalf("cost status = %v, want %v", status, test.status)
			}
			if test.status == CostStatusUnknown {
				if cost != nil || version != nil {
					t.Fatalf("unknown cost retained price: cost=%v version=%v", cost, version)
				}
			} else if cost == nil || *cost != test.want || version == nil || *version != catalog.CatalogVersion {
				t.Fatalf("cost=%v version=%v, want %d at catalog %d", cost, version, test.want, catalog.CatalogVersion)
			}
		})
	}
}

func TestEstimateCostMicrosDoesNotGuessModelAliases(t *testing.T) {
	tokens := TokenCounts{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13}
	for _, model := range []string{"openai/gpt-5.3-codex", "gpt-5.3-codex-2026-07-06", "chat-latest"} {
		cost, status := EstimateCostMicros(model, tokens)
		if cost != nil || status != CostStatusUnknown {
			t.Fatalf("expected unlisted model %q to remain unknown, status=%q cost=%v", model, status, cost)
		}
	}
}

func TestEstimateCostMicrosHandlesUnavailableCachedPricing(t *testing.T) {
	uncachedOnly, status := EstimateCostMicros("gpt-5.5-pro", TokenCounts{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
		TotalTokens:  2_000_000,
	})
	if status != CostStatusEstimated || uncachedOnly == nil || *uncachedOnly != 210_000_000 {
		t.Fatalf("expected uncached pro usage to be estimated, got status=%q cost=%v", status, uncachedOnly)
	}

	cached, status := EstimateCostMicros("gpt-5.5-pro", TokenCounts{
		InputTokens:       1_000_000,
		CachedInputTokens: 1,
		OutputTokens:      1_000_000,
		TotalTokens:       2_000_000,
	})
	if status != CostStatusUnknown || cached != nil {
		t.Fatalf("expected cached pro usage to be unknown, got status=%q cost=%v", status, cached)
	}
}

func TestEstimateCostMicrosReportsUnknownOnOverflow(t *testing.T) {
	cost, status := EstimateCostMicros("gpt-5.5", TokenCounts{
		InputTokens:  math.MaxInt64,
		OutputTokens: math.MaxInt64,
		TotalTokens:  math.MaxInt64,
	})
	if status != CostStatusUnknown || cost != nil {
		t.Fatalf("expected overflowed cost to be unknown, got status=%q cost=%v", status, cost)
	}
}

func TestRoundedTokenCostMicros(t *testing.T) {
	tests := []struct {
		name             string
		tokens           int64
		microsPerMillion int64
		want             int64
	}{
		{name: "zero tokens", tokens: 0, microsPerMillion: 1_000_000, want: 0},
		{name: "zero price", tokens: 1_000_000, microsPerMillion: 0, want: 0},
		{name: "below half rounds down", tokens: 1, microsPerMillion: 499_999, want: 0},
		{name: "half rounds up", tokens: 1, microsPerMillion: 500_000, want: 1},
		{name: "whole million", tokens: 1_000_000, microsPerMillion: 1_750_000, want: 1_750_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundedTokenCostMicros(tt.tokens, tt.microsPerMillion); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestRoundedTokenCostMicrosSaturatesOnOverflow(t *testing.T) {
	if got := roundedTokenCostMicros(math.MaxInt64, math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("expected overflowed helper result to saturate, got %d", got)
	}
}
