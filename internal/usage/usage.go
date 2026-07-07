package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ProviderCodex             = "codex"
	SourceCodexSessionJSONL   = "codex-session-jsonl"
	CostStatusEstimated       = "estimated"
	CostStatusUnknown         = "unknown"
	CodexSessionParserVersion = "codex-session-jsonl-v1"
)

type TokenCounts struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
}

type Event struct {
	ID                  string
	ProviderID          string
	Source              string
	SourceKey           string
	SessionID           string
	Model               string
	OccurredAtUnixMS    int64
	InputTokens         int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros *int64
	CostStatus          string
	MetadataJSON        string
}

type SourceFile struct {
	Path           string
	SourceKey      string
	ModifiedUnixMS int64
	SizeBytes      int64
}

type FileParseResult struct {
	Events           []Event
	InvalidLines     int64
	UnsupportedLines int64
}

type Price struct {
	InputMicrosPerMillion       int64
	CachedInputMicrosPerMillion *int64
	OutputMicrosPerMillion      int64
}

// Static price source: OpenAI API pricing, accessed 2026-07-06.
// These local estimates use Standard API prices. For models with multiple
// context tiers, the table uses the short-context rate until Codex logs expose
// enough billing context to select batch, flex, priority, or long-context rates.
var staticPrices = map[string]Price{
	"gpt-5.5":       price(5_000_000, 500_000, 30_000_000),
	"gpt-5.5-pro":   priceWithoutCached(30_000_000, 180_000_000),
	"gpt-5.4":       price(2_500_000, 250_000, 15_000_000),
	"gpt-5.4-mini":  price(750_000, 75_000, 4_500_000),
	"gpt-5.4-nano":  price(200_000, 20_000, 1_250_000),
	"gpt-5.4-pro":   priceWithoutCached(30_000_000, 180_000_000),
	"chat-latest":   price(5_000_000, 500_000, 30_000_000),
	"gpt-5.3-codex": price(1_750_000, 175_000, 14_000_000),
}

func EstimateCostMicros(model string, tokens TokenCounts) (*int64, string) {
	price, ok := staticPrices[normalizeCodexModel(model)]
	if !ok || tokens.CachedInputTokens > tokens.InputTokens {
		return nil, CostStatusUnknown
	}
	if tokens.CachedInputTokens > 0 && price.CachedInputMicrosPerMillion == nil {
		return nil, CostStatusUnknown
	}

	uncachedInputTokens := tokens.InputTokens - tokens.CachedInputTokens
	cost, ok := roundedTokenCostMicrosSafe(uncachedInputTokens, price.InputMicrosPerMillion)
	if !ok {
		return nil, CostStatusUnknown
	}
	if price.CachedInputMicrosPerMillion != nil {
		cachedCost, ok := roundedTokenCostMicrosSafe(tokens.CachedInputTokens, *price.CachedInputMicrosPerMillion)
		if !ok {
			return nil, CostStatusUnknown
		}
		cost, ok = addCostMicros(cost, cachedCost)
		if !ok {
			return nil, CostStatusUnknown
		}
	}
	outputCost, ok := roundedTokenCostMicrosSafe(tokens.OutputTokens, price.OutputMicrosPerMillion)
	if !ok {
		return nil, CostStatusUnknown
	}
	cost, ok = addCostMicros(cost, outputCost)
	if !ok {
		return nil, CostStatusUnknown
	}
	return &cost, CostStatusEstimated
}

func price(inputMicrosPerMillion int64, cachedInputMicrosPerMillion int64, outputMicrosPerMillion int64) Price {
	return Price{
		InputMicrosPerMillion:       inputMicrosPerMillion,
		CachedInputMicrosPerMillion: &cachedInputMicrosPerMillion,
		OutputMicrosPerMillion:      outputMicrosPerMillion,
	}
}

func priceWithoutCached(inputMicrosPerMillion int64, outputMicrosPerMillion int64) Price {
	return Price{
		InputMicrosPerMillion:  inputMicrosPerMillion,
		OutputMicrosPerMillion: outputMicrosPerMillion,
	}
}

func SourceKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	normalized := filepath.ToSlash(filepath.Clean(abs))
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		normalized = strings.ToLower(normalized)
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:]), nil
}

func EventID(providerID string, source string, lineIndex int64, sessionID string, model string, tokens TokenCounts) string {
	// File identity is intentionally excluded so copied or moved Codex session
	// logs remain idempotent; SourceKey is only a file cursor key.
	payload := fmt.Sprintf(
		"%s\x00%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d",
		providerID,
		source,
		lineIndex,
		sessionID,
		model,
		tokens.InputTokens,
		tokens.CachedInputTokens,
		tokens.OutputTokens,
		tokens.TotalTokens,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func EventDigest(events []Event, limit int64) string {
	if limit < 0 || limit > int64(len(events)) {
		limit = int64(len(events))
	}
	hash := sha256.New()
	for _, event := range events[:limit] {
		hash.Write([]byte(event.ID))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func MetadataJSON(lineIndex int64, eventType string, usageKind string) string {
	// Codex session events can contain prompts, completions, and credentials; only
	// derived audit fields are persisted.
	raw, err := json.Marshal(map[string]any{
		"parser_version": CodexSessionParserVersion,
		"line_index":     lineIndex,
		"event_type":     eventType,
		"usage_kind":     usageKind,
	})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func roundedTokenCostMicros(tokens int64, microsPerMillion int64) int64 {
	cost, ok := roundedTokenCostMicrosSafe(tokens, microsPerMillion)
	if !ok {
		return math.MaxInt64
	}
	return cost
}

func roundedTokenCostMicrosSafe(tokens int64, microsPerMillion int64) (int64, bool) {
	if tokens <= 0 || microsPerMillion <= 0 {
		return 0, true
	}

	wholeMillions := tokens / 1_000_000
	remainder := tokens % 1_000_000
	if wholeMillions > math.MaxInt64/microsPerMillion {
		return 0, false
	}
	cost := wholeMillions * microsPerMillion
	if remainder == 0 {
		return cost, true
	}

	if remainder > (math.MaxInt64-500_000)/microsPerMillion {
		return 0, false
	}
	remainderCost := (remainder*microsPerMillion + 500_000) / 1_000_000
	return addCostMicros(cost, remainderCost)
}

func addCostMicros(left int64, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

func USDStringFromMicros(micros int64) string {
	if micros < 0 {
		micros = 0
	}
	return fmt.Sprintf("%d.%06d", micros/1_000_000, micros%1_000_000)
}
