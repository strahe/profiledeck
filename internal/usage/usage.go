package usage

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	ProviderCodex            = "codex"
	SourceCodexSessionJSONL  = "codex-session-jsonl"
	CostStatusEstimated      = store.UsageCostStatusEstimated
	CostStatusPartial        = store.UsageCostStatusPartial
	CostStatusUnknown        = store.UsageCostStatusUnknown
	CodexUsageParserRevision = int64(1)
	PricingBasis             = "openai-standard-api"
	PricingSourceURL         = "https://developers.openai.com/api/docs/pricing"
	PricingVerifiedAt        = "2026-09-12"
	GrokBuildPricingBasis    = "xai-standard-api-short-context"
	GrokBuildPricingSource   = "https://docs.x.ai/developers/models/grok-4.5"
	GrokBuildPricingVerified = "2026-07-30"
)

// CodexUsageIdentityRevision changes whenever fact identity semantics change;
// runtime sync rejects checkpoints written under an older revision.
const CodexUsageIdentityRevision = int64(2)

type TokenCounts struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
}

type Event struct {
	EventKey            store.UsageKey
	SessionID           string
	Model               string
	OccurredAtUnixMS    int64
	InputTokens         int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros *int64
	CostStatus          store.UsageCostStatus
}

type SourceFile struct {
	Path               string
	SourceKey          store.UsageKey
	ModifiedUnixMS     int64
	ModifiedUnixNano   int64
	SizeBytes          int64
	Mode               uint32
	FileIdentityDigest store.UsageKey
	MetadataDigest     store.UsageKey
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

// PriceCatalog owns a provider-scoped, immutable price snapshot. Callers can
// estimate against it but cannot mutate or inspect its backing map.
type PriceCatalog struct {
	prices map[string]Price
}

func newPriceCatalog(prices map[string]Price) PriceCatalog {
	copied := make(map[string]Price, len(prices))
	for model, modelPrice := range prices {
		copied[pricingModelID(model)] = modelPrice
	}
	return PriceCatalog{prices: copied}
}

// Static price source: OpenAI API pricing, accessed 2026-09-12.
// These local estimates use Standard API prices. For models with multiple
// context tiers, the table uses the short-context rate until Codex logs expose
// enough billing context to select batch, flex, priority, or long-context rates.
// GPT-5.6 and GPT-6 Astra logs do not expose cache-write tokens, so their stored
// amount is the verifiable input/cache-read/output subtotal and remains
// explicitly partial.
var codexPriceCatalog = newPriceCatalog(map[string]Price{
	"gpt-6-astra":   priceWithCacheWrite(10_000_000, 1_000_000, 12_500_000, 50_000_000),
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
})

// Static price source: xAI Grok 4.5 and pricing documentation, accessed
// 2026-07-30. Aggregated Grok Build records do not identify long-context calls,
// so estimates intentionally use only the short-context Standard API tier.
// Grok Build session records may emit grok-4.5-build for Grok 4.5 work.
var grokBuildPriceCatalog = newPriceCatalog(map[string]Price{
	"grok-4.5":          price(2_000_000, 300_000, 6_000_000),
	"grok-4.5-build":    price(2_000_000, 300_000, 6_000_000),
	"grok-4.5-latest":   price(2_000_000, 300_000, 6_000_000),
	"grok-build-latest": price(2_000_000, 300_000, 6_000_000),
})

func EstimateCostMicros(model string, tokens TokenCounts) (*int64, store.UsageCostStatus) {
	return codexPriceCatalog.EstimateCostMicros(model, tokens)
}

func EstimateGrokBuildCostMicros(model string, tokens TokenCounts) (*int64, store.UsageCostStatus) {
	return grokBuildPriceCatalog.EstimateCostMicros(model, tokens)
}

func (catalog PriceCatalog) Supports(model string) bool {
	_, ok := catalog.prices[pricingModelID(model)]
	return ok
}

func (catalog PriceCatalog) EstimateCostMicros(
	model string,
	tokens TokenCounts,
) (*int64, store.UsageCostStatus) {
	price, ok := catalog.prices[pricingModelID(model)]
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

func SourceKey(path string) (store.UsageKey, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return store.UsageKey{}, err
	}
	normalized := filepath.ToSlash(filepath.Clean(abs))
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		normalized = strings.ToLower(normalized)
	}
	sum := sha256.Sum256([]byte(normalized))
	return store.UsageKey(sum), nil
}

func sourceFileFromInfo(path string, sourceKey store.UsageKey, info os.FileInfo) SourceFile {
	file := SourceFile{
		Path:               path,
		SourceKey:          sourceKey,
		ModifiedUnixMS:     info.ModTime().UnixMilli(),
		ModifiedUnixNano:   info.ModTime().UnixNano(),
		SizeBytes:          info.Size(),
		Mode:               uint32(info.Mode()),
		FileIdentityDigest: sourceFileIdentityDigest(path, info),
	}
	file.MetadataDigest = sourceFileMetadataDigest(file)
	return file
}

func sourceFileMetadataDigest(file SourceFile) store.UsageKey {
	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-usage-file-metadata-v1\x00"))
	var encoded [8]byte
	for _, value := range []int64{file.SizeBytes, file.ModifiedUnixNano, int64(file.Mode)} {
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		_, _ = hash.Write(encoded[:])
	}
	_, _ = hash.Write(file.FileIdentityDigest[:])
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}

func EventID(providerID, source string, usageOrdinal int64, sessionID, model string, tokens TokenCounts) store.UsageKey {
	if providerID == "" || source == "" || usageOrdinal <= 0 || sessionID == "" {
		return store.UsageKey{}
	}
	// Fork persistence rewrites paths, line positions, and timestamps. The
	// primary identity excludes them while provider/source scope and session
	// usage order keep independent events distinct.
	model = normalizeCodexModel(model)
	payload := fmt.Sprintf(
		"profiledeck-usage-fact-v1\x00%s\x00%s\x00%d\x00%s\x00%d\x00%s\x00%d\x00%d\x00%d\x00%d",
		providerID,
		source,
		CodexUsageIdentityRevision,
		sessionID,
		usageOrdinal,
		model,
		tokens.InputTokens,
		tokens.CachedInputTokens,
		tokens.OutputTokens,
		tokens.TotalTokens,
	)
	sum := sha256.Sum256([]byte(payload))
	return store.UsageKey(sum)
}

func EventDigest(events []Event, limit int64) store.UsageKey {
	if limit < 0 || limit > int64(len(events)) {
		limit = int64(len(events))
	}
	hash := sha256.New()
	for _, event := range events[:limit] {
		// Preserve the stored cursor digest representation while keeping the
		// Store API strongly typed and free of hex round trips.
		hash.Write([]byte(event.EventKey.String()))
		hash.Write([]byte{0})
	}
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
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
