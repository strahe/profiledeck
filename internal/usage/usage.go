package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	ProviderCodex             = "codex"
	SourceCodexSessionJSONL   = "codex-session-jsonl"
	CostStatusEstimated       = "estimated"
	CostStatusPartial         = "partial"
	CostStatusUnknown         = "unknown"
	CodexSessionParserVersion = "codex-session-jsonl-v2"
	PricingBasis              = "openai-standard-api"
	PricingSourceURL          = "https://developers.openai.com/api/docs/pricing"
	PricingVerifiedAt         = "2026-07-10"
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
	CacheWriteMicrosPerMillion  *int64
	OutputMicrosPerMillion      int64
}

// Static price source: OpenAI API pricing, accessed 2026-07-10.
// These local estimates use Standard API prices. For models with multiple
// context tiers, the table uses the short-context rate until Codex logs expose
// enough billing context to select batch, flex, priority, or long-context rates.
// GPT-5.6 logs do not expose cache-write tokens, so their stored amount is the
// verifiable input/cache-read/output subtotal and remains explicitly partial.
var staticPrices = map[string]Price{
	"gpt-5.6-sol":   priceWithCacheWrite(5_000_000, 500_000, 6_250_000, 30_000_000),
	"gpt-5.6-terra": priceWithCacheWrite(2_500_000, 250_000, 3_125_000, 15_000_000),
	"gpt-5.6-luna":  priceWithCacheWrite(1_000_000, 100_000, 1_250_000, 6_000_000),
	"gpt-5.5":       price(5_000_000, 500_000, 30_000_000),
	"gpt-5.5-pro":   priceWithoutCached(30_000_000, 180_000_000),
	"gpt-5.4":       price(2_500_000, 250_000, 15_000_000),
	"gpt-5.4-mini":  price(750_000, 75_000, 4_500_000),
	"gpt-5.4-nano":  price(200_000, 20_000, 1_250_000),
	"gpt-5.4-pro":   priceWithoutCached(30_000_000, 180_000_000),
	"chat-latest":   price(5_000_000, 500_000, 30_000_000),
	"gpt-5.3-codex": price(1_750_000, 175_000, 14_000_000),
	"gpt-5.2":       price(1_750_000, 175_000, 14_000_000),
	"gpt-5.2-pro":   priceWithoutCached(21_000_000, 168_000_000),
	"gpt-5.1":       price(1_250_000, 125_000, 10_000_000),
	"gpt-5":         price(1_250_000, 125_000, 10_000_000),
	"gpt-5-mini":    price(250_000, 25_000, 2_000_000),
	"gpt-5-nano":    price(50_000, 5_000, 400_000),
	"gpt-5-pro":     priceWithoutCached(15_000_000, 120_000_000),
	"gpt-4.1":       price(2_000_000, 500_000, 8_000_000),
	"gpt-4.1-mini":  price(400_000, 100_000, 1_600_000),
	"gpt-4.1-nano":  price(100_000, 25_000, 400_000),
}

func EstimateCostMicros(model string, tokens TokenCounts) (*int64, string) {
	price, ok := staticPrices[pricingModelID(model)]
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
	if price.CacheWriteMicrosPerMillion != nil {
		return &cost, CostStatusPartial
	}
	return &cost, CostStatusEstimated
}

func PartialCostModelIDs() []string {
	models := make([]string, 0)
	for model, price := range staticPrices {
		if price.CacheWriteMicrosPerMillion != nil {
			models = append(models, model)
		}
	}
	sort.Strings(models)
	return models
}

func pricingModelID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func price(inputMicrosPerMillion, cachedInputMicrosPerMillion, outputMicrosPerMillion int64) Price {
	return Price{
		InputMicrosPerMillion:       inputMicrosPerMillion,
		CachedInputMicrosPerMillion: &cachedInputMicrosPerMillion,
		OutputMicrosPerMillion:      outputMicrosPerMillion,
	}
}

func priceWithCacheWrite(inputMicrosPerMillion, cachedInputMicrosPerMillion, cacheWriteMicrosPerMillion, outputMicrosPerMillion int64) Price {
	price := price(inputMicrosPerMillion, cachedInputMicrosPerMillion, outputMicrosPerMillion)
	price.CacheWriteMicrosPerMillion = &cacheWriteMicrosPerMillion
	return price
}

func priceWithoutCached(inputMicrosPerMillion, outputMicrosPerMillion int64) Price {
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

func EventID(providerID, source string, usageOrdinal int64, sessionID, model string, tokens TokenCounts) string {
	if providerID == "" || source == "" || usageOrdinal <= 0 || sessionID == "" {
		return ""
	}
	// Fork persistence rewrites paths, line positions, and timestamps. The
	// primary identity excludes them while provider/source scope and session
	// usage order keep independent events distinct.
	model = normalizeCodexModel(model)
	payload := fmt.Sprintf(
		"profiledeck-usage-event-v2\x00%s\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%d\x00%d",
		providerID,
		source,
		sessionID,
		usageOrdinal,
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

func MetadataJSON(lineIndex, usageOrdinal int64, eventType, usageKind string) string {
	// Codex session events can contain prompts, completions, and credentials; only
	// derived audit fields are persisted.
	raw, err := json.Marshal(map[string]any{
		"parser_version": CodexSessionParserVersion,
		"line_index":     lineIndex,
		"usage_ordinal":  usageOrdinal,
		"event_type":     safeUsageMetadataLabel(eventType),
		"usage_kind":     safeUsageMetadataLabel(usageKind),
	})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func safeUsageMetadataLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 0 || len(value) > 80 || !isSafeUsageIdentifier(value, false) {
		return "unknown"
	}
	return value
}

func roundedTokenCostMicros(tokens, microsPerMillion int64) int64 {
	cost, ok := roundedTokenCostMicrosSafe(tokens, microsPerMillion)
	if !ok {
		return math.MaxInt64
	}
	return cost
}

func roundedTokenCostMicrosSafe(tokens, microsPerMillion int64) (int64, bool) {
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

func addCostMicros(left, right int64) (int64, bool) {
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
