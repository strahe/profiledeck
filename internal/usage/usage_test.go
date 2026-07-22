package usage

import (
	"math"
	"testing"

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
		{model: "chat-latest", want: 34_550_000},
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

func TestEstimateCostMicrosReportsGPT56BaseCostAsPartial(t *testing.T) {
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
		{model: "gpt-5.6-sol", want: 34_550_000},
		{model: "gpt-5.6-terra", want: 17_275_000},
		{model: "gpt-5.6-luna", want: 6_910_000},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, status := EstimateCostMicros(tt.model, tokens)
			if status != CostStatusPartial || got == nil || *got != tt.want {
				t.Fatalf("expected partial base cost %d, got status=%q cost=%v", tt.want, status, got)
			}
		})
	}
}

func TestEstimateCostMicrosDoesNotGuessModelAliases(t *testing.T) {
	tokens := TokenCounts{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13}
	for _, model := range []string{"openai/gpt-5.3-codex", "gpt-5.3-codex-2026-07-06"} {
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
