package usage

import (
	"math"
	"testing"
)

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
