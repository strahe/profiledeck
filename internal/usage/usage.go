package usage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pricing "github.com/strahe/profiledeck/internal/pricing"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	ProviderCodex              = "codex"
	SourceCodexSessionJSONL    = "codex-session-jsonl"
	CostStatusEstimated        = store.UsageCostStatusEstimated
	CostStatusPartial          = store.UsageCostStatusPartial
	CostStatusUnknown          = store.UsageCostStatusUnknown
	ReportedCostStatusReported = store.UsageReportedCostStatusReported
	ReportedCostStatusPartial  = store.UsageReportedCostStatusPartial
	ReportedCostStatusUnknown  = store.UsageReportedCostStatusUnknown
	CodexUsageParserRevision   = int64(1)
	PricingBasis               = "openai-standard-api"
	PricingSourceURL           = "https://developers.openai.com/api/docs/pricing"
	PricingVerifiedAt          = "2026-09-12"
	GrokBuildPricingBasis      = "xai-standard-api-short-context"
	GrokBuildPricingSource     = "https://docs.x.ai/developers/pricing"
	GrokBuildPricingVerified   = "2026-09-12"
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
	EventKey                 store.UsageKey
	SessionID                string
	Model                    string
	OccurredAtUnixMS         int64
	InputTokens              int64
	CachedInputTokens        int64
	OutputTokens             int64
	TotalTokens              int64
	EstimatedCostMicros      *int64
	CostStatus               store.UsageCostStatus
	ReportedCostUSDTicks     *int64
	ReportedCostStatus       store.UsageReportedCostStatus
	PricingCatalogVersion    *int64
	CacheWriteInputTokens    *int64
	CacheCreationInputTokens *int64
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

var bundledPricingCatalog = pricing.Embedded()

type pricingContextKey struct{}

func withPricingSnapshot(ctx context.Context, catalog pricing.Catalog) context.Context {
	return context.WithValue(ctx, pricingContextKey{}, catalog)
}

func pricingSnapshot(ctx context.Context) pricing.Catalog {
	if catalog, ok := ctx.Value(pricingContextKey{}).(pricing.Catalog); ok {
		return catalog
	}
	return bundledPricingCatalog
}

func EstimateCostMicros(model string, tokens TokenCounts) (*int64, store.UsageCostStatus) {
	cost, status, _ := estimateCostAt(bundledPricingCatalog, ProviderCodex, model, time.Now().UnixMilli(), tokens, nil, nil)
	return cost, status
}

func EstimateGrokBuildCostMicros(model string, tokens TokenCounts) (*int64, store.UsageCostStatus) {
	cost, status, _ := estimateCostAt(bundledPricingCatalog, "grok-build", model, time.Now().UnixMilli(), tokens, nil, nil)
	return cost, status
}

func estimateCostAt(catalog pricing.Catalog, provider, model string, occurredAt int64,
	tokens TokenCounts, cacheWrite, perCallInput *int64,
) (*int64, store.UsageCostStatus, *int64) {
	selected, ok := catalog.Select(provider, pricingCatalogModelID(provider, model), occurredAt)
	if !ok || tokens.InputTokens < 0 || tokens.CachedInputTokens < 0 ||
		tokens.CachedInputTokens > tokens.InputTokens || tokens.OutputTokens < 0 {
		return nil, CostStatusUnknown, nil
	}
	rates := selected.Rates
	partial := false
	if selected.Long != nil && provider == ProviderCodex {
		if perCallInput == nil {
			partial = true
		} else if *perCallInput > selected.Above {
			rates = *selected.Long
		}
	}
	if tokens.CachedInputTokens > 0 && rates.CachedInput == nil {
		return nil, CostStatusUnknown, nil
	}
	fresh := tokens.InputTokens - tokens.CachedInputTokens
	if cacheWrite != nil {
		if *cacheWrite < 0 || *cacheWrite > fresh {
			return nil, CostStatusUnknown, nil
		}
		fresh -= *cacheWrite
	} else if rates.CacheWrite != nil {
		partial = true
	}
	cost, ok := roundedTokenCostMicrosSafe(fresh, rates.Input)
	if !ok {
		return nil, CostStatusUnknown, nil
	}
	if rates.CachedInput != nil {
		cached, valid := roundedTokenCostMicrosSafe(tokens.CachedInputTokens, *rates.CachedInput)
		if !valid {
			return nil, CostStatusUnknown, nil
		}
		cost, ok = addCostMicros(cost, cached)
		if !ok {
			return nil, CostStatusUnknown, nil
		}
	}
	if cacheWrite != nil && *cacheWrite > 0 {
		if rates.CacheWrite == nil {
			partial = true
		} else {
			writeCost, valid := roundedTokenCostMicrosSafe(*cacheWrite, *rates.CacheWrite)
			if !valid {
				return nil, CostStatusUnknown, nil
			}
			cost, ok = addCostMicros(cost, writeCost)
			if !ok {
				return nil, CostStatusUnknown, nil
			}
		}
	}
	output, ok := roundedTokenCostMicrosSafe(tokens.OutputTokens, rates.Output)
	if !ok {
		return nil, CostStatusUnknown, nil
	}
	cost, ok = addCostMicros(cost, output)
	if !ok {
		return nil, CostStatusUnknown, nil
	}
	version := selected.Version
	if partial {
		return &cost, CostStatusPartial, &version
	}
	return &cost, CostStatusEstimated, &version
}

func pricingModelID(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func pricingCatalogModelID(provider, model string) string {
	model = pricingModelID(model)
	// Grok Build's standard 4.7 session label is distinct from its Fast variant.
	if provider == "grok-build" && model == "grok-4.7-build" {
		return "grok-4.7"
	}
	return model
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

func USDStringFromTicks(ticks int64) string {
	if ticks < 0 {
		ticks = 0
	}
	return fmt.Sprintf("%d.%010d", ticks/10_000_000_000, ticks%10_000_000_000)
}
