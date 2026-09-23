package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	embedded "github.com/strahe/profiledeck/pricing"
)

const MaxCatalogBytes = 256 << 10

var modelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type Rates struct {
	Input       string  `json:"input"`
	CachedInput *string `json:"cached_input"`
	CacheWrite  *string `json:"cache_write"`
	Output      string  `json:"output"`
}

type LongContext struct {
	AboveInputTokens int64 `json:"above_input_tokens"`
	Rates            Rates `json:"rates"`
}

type Entry struct {
	Provider       string       `json:"provider"`
	Model          string       `json:"model"`
	Basis          string       `json:"basis"`
	EffectiveFrom  string       `json:"effective_from"`
	EffectiveUntil string       `json:"effective_until,omitempty"`
	SourceURL      string       `json:"source_url"`
	VerifiedAt     string       `json:"verified_at"`
	ShortContext   Rates        `json:"short_context"`
	LongContext    *LongContext `json:"long_context,omitempty"`
}

type Catalog struct {
	SchemaVersion  int     `json:"schema_version"`
	CatalogVersion int64   `json:"catalog_version"`
	Entries        []Entry `json:"entries"`
}

type TokenRates struct {
	Input       int64
	CachedInput *int64
	CacheWrite  *int64
	Output      int64
}

type Selected struct {
	Rates   TokenRates
	Long    *TokenRates
	Above   int64
	Basis   string
	Source  string
	Version int64
}

type EffectivePeriod struct {
	FromUnixMS  int64
	UntilUnixMS int64
}

func (catalog Catalog) EffectivePeriods(provider, model string) []EffectivePeriod {
	if strings.Contains(model, "latest") {
		return nil
	}
	var periods []EffectivePeriod
	for _, entry := range catalog.Entries {
		if entry.Provider != provider || entry.Model != model {
			continue
		}
		from, _ := time.Parse("2006-01-02", entry.EffectiveFrom)
		period := EffectivePeriod{FromUnixMS: from.UnixMilli(), UntilUnixMS: math.MaxInt64}
		if entry.EffectiveUntil != "" {
			until, _ := time.Parse("2006-01-02", entry.EffectiveUntil)
			period.UntilUnixMS = until.UnixMilli()
		}
		periods = append(periods, period)
	}
	return periods
}

func Embedded() Catalog {
	catalog, err := Parse(embedded.CatalogJSON)
	if err != nil {
		panic(err)
	}
	return catalog
}

func Parse(data []byte) (Catalog, error) {
	if len(data) == 0 || len(data) > MaxCatalogBytes {
		return Catalog{}, errors.New("pricing catalog size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, errors.New("pricing catalog JSON is invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Catalog{}, errors.New("pricing catalog has trailing data")
	}
	if catalog.SchemaVersion != 1 || catalog.CatalogVersion <= 0 || len(catalog.Entries) == 0 || len(catalog.Entries) > 2_000 {
		return Catalog{}, errors.New("pricing catalog version or entries are invalid")
	}
	byModel := make(map[string][]Entry)
	for _, entry := range catalog.Entries {
		if err := validateEntry(entry); err != nil {
			return Catalog{}, err
		}
		key := entry.Provider + "\x00" + entry.Model
		byModel[key] = append(byModel[key], entry)
	}
	for _, entries := range byModel {
		sort.Slice(entries, func(i, j int) bool { return entries[i].EffectiveFrom < entries[j].EffectiveFrom })
		for i := 1; i < len(entries); i++ {
			if entries[i-1].EffectiveUntil == "" || entries[i-1].EffectiveUntil > entries[i].EffectiveFrom {
				return Catalog{}, errors.New("pricing periods overlap")
			}
		}
	}
	return catalog, nil
}

func validateEntry(entry Entry) error {
	if !modelIDPattern.MatchString(entry.Model) || strings.Contains(entry.Model, "latest") ||
		(entry.Provider != "codex" && entry.Provider != "grok-build" && entry.Provider != "claude-code") ||
		(entry.Provider == "grok-build" && (strings.Contains(entry.Model, "-build") || strings.Contains(entry.Model, "fast"))) {
		return errors.New("pricing model is invalid")
	}
	wantBasis, wantHost := "openai-standard-api", "developers.openai.com"
	switch entry.Provider {
	case "grok-build":
		wantBasis, wantHost = "xai-standard-api-short-context", "docs.x.ai"
	case "claude-code":
		wantBasis, wantHost = "anthropic-standard-api", "platform.claude.com"
	}
	parsed, err := url.Parse(entry.SourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != wantHost || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || entry.Basis != wantBasis {
		return errors.New("pricing source is invalid")
	}
	from, err := time.Parse("2006-01-02", entry.EffectiveFrom)
	if err != nil {
		return errors.New("pricing start date is invalid")
	}
	if entry.EffectiveUntil != "" {
		until, err := time.Parse("2006-01-02", entry.EffectiveUntil)
		if err != nil || !until.After(from) {
			return errors.New("pricing end date is invalid")
		}
	}
	verified, err := time.Parse("2006-01-02", entry.VerifiedAt)
	if err != nil || verified.Before(from) {
		return errors.New("pricing verification date is invalid")
	}
	if _, err := parseRates(entry.ShortContext); err != nil {
		return err
	}
	if entry.LongContext != nil {
		threshold := int64(272_000)
		if entry.Provider == "grok-build" {
			threshold = 199_999
		}
		if entry.LongContext.AboveInputTokens != threshold {
			return errors.New("pricing context threshold is invalid")
		}
		if _, err := parseRates(entry.LongContext.Rates); err != nil {
			return err
		}
	}
	return nil
}

func parseRates(rates Rates) (TokenRates, error) {
	input, err := decimalMicros(rates.Input)
	if err != nil {
		return TokenRates{}, err
	}
	output, err := decimalMicros(rates.Output)
	if err != nil {
		return TokenRates{}, err
	}
	selected := TokenRates{Input: input, Output: output}
	if rates.CachedInput != nil {
		value, err := decimalMicros(*rates.CachedInput)
		if err != nil {
			return TokenRates{}, err
		}
		selected.CachedInput = &value
	}
	if rates.CacheWrite != nil {
		value, err := decimalMicros(*rates.CacheWrite)
		if err != nil {
			return TokenRates{}, err
		}
		selected.CacheWrite = &value
	}
	return selected, nil
}

func decimalMicros(value string) (int64, error) {
	if value == "" || strings.Trim(value, "0123456789.") != "" || strings.Count(value, ".") > 1 {
		return 0, errors.New("pricing rate is invalid")
	}
	parts := strings.SplitN(value, ".", 2)
	if parts[0] == "" {
		parts[0] = "0"
	}
	if len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 6) {
		return 0, errors.New("pricing rate precision is invalid")
	}
	var whole, fraction int64
	for _, digit := range parts[0] {
		whole = whole*10 + int64(digit-'0')
		if whole > 1_000 {
			return 0, errors.New("pricing rate exceeds limit")
		}
	}
	if len(parts) == 2 {
		for _, digit := range parts[1] {
			fraction = fraction*10 + int64(digit-'0')
		}
		for i := len(parts[1]); i < 6; i++ {
			fraction *= 10
		}
	}
	amount := whole*1_000_000 + fraction
	if amount <= 0 || amount > math.MaxInt32*1_000 {
		return 0, errors.New("pricing rate is out of range")
	}
	return amount, nil
}

func (catalog Catalog) Select(provider, model string, occurredAtUnixMS int64) (Selected, bool) {
	if occurredAtUnixMS <= 0 || strings.Contains(model, "latest") {
		return Selected{}, false
	}
	day := time.UnixMilli(occurredAtUnixMS).UTC().Format("2006-01-02")
	for _, entry := range catalog.Entries {
		if entry.Provider != provider || entry.Model != model || day < entry.EffectiveFrom || entry.EffectiveUntil != "" && day >= entry.EffectiveUntil {
			continue
		}
		rates, err := parseRates(entry.ShortContext)
		if err != nil {
			return Selected{}, false
		}
		result := Selected{Rates: rates, Basis: entry.Basis, Source: entry.SourceURL, Version: catalog.CatalogVersion}
		if entry.LongContext != nil {
			long, err := parseRates(entry.LongContext.Rates)
			if err != nil {
				return Selected{}, false
			}
			result.Long, result.Above = &long, entry.LongContext.AboveInputTokens
		}
		return result, true
	}
	return Selected{}, false
}

func (catalog Catalog) Supports(provider, model string) bool {
	for _, entry := range catalog.Entries {
		if entry.Provider == provider && entry.Model == model {
			return true
		}
	}
	return false
}

func (catalog Catalog) ValidateCompatibleVersion(current int64) error {
	if catalog.CatalogVersion < current {
		return fmt.Errorf("pricing catalog version is older than active version")
	}
	return nil
}
