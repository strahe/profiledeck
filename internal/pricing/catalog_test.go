package pricing

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	embedded "github.com/strahe/profiledeck/pricing"
)

func TestEmbeddedCatalogSelectsDatedExactModels(t *testing.T) {
	catalog, err := Parse(embedded.CatalogJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		provider, model, date string
		want                  bool
	}{
		{"codex", "gpt-6-sol", "2026-09-21", false},
		{"codex", "gpt-6-sol", "2026-09-22", true},
		{"codex", "chat-latest", "2026-09-23", false},
		{"grok-build", "grok-4.7", "2026-09-21", true},
		{"grok-build", "grok-4.7-build", "2026-09-23", false},
		{"grok-build", "grok-4.7-build-fast", "2026-09-23", false},
		{"claude-code", "claude-opus-5-5", "2026-09-21", false},
		{"claude-code", "claude-opus-5-5", "2026-09-22", true},
		{"codex", "gpt-6.1-sol", "2026-10-28", false},
		{"codex", "gpt-6.1-sol", "2026-10-29", true},
		{"codex", "gpt-6.1-terra", "2026-10-28", false},
		{"codex", "gpt-6.1-terra", "2026-10-29", true},
		{"claude-code", "claude-sonnet-5-5", "2026-10-28", false},
		{"claude-code", "claude-sonnet-5-5", "2026-10-29", true},
		{"claude-code", "claude-haiku-5-5", "2026-10-28", false},
		{"claude-code", "claude-haiku-5-5", "2026-10-29", true},
		{"claude-code", "claude-fable-5-1", "2026-10-28", false},
		{"claude-code", "claude-fable-5-1", "2026-10-29", true},
		{"grok-build", "grok-build-0.1", "2026-10-28", false},
		{"grok-build", "grok-build-0.1", "2026-10-29", true},
	} {
		at, _ := time.Parse("2006-01-02", test.date)
		_, ok := catalog.Select(test.provider, test.model, at.UnixMilli())
		if ok != test.want {
			t.Fatalf("%s %s on %s: selected=%t", test.provider, test.model, test.date, ok)
		}
	}
}

func TestCatalogKeepsHistoricalPriceBoundary(t *testing.T) {
	catalog := Embedded()
	for _, test := range []struct {
		date  string
		input int64
	}{
		{"2026-07-08", 0},
		{"2026-08-20", 5_000_000},
		{"2026-08-21", 4_000_000},
	} {
		at, _ := time.Parse("2006-01-02", test.date)
		selected, ok := catalog.Select("codex", "gpt-5.6-sol", at.UnixMilli())
		if ok != (test.input != 0) || ok && selected.Rates.Input != test.input {
			t.Fatalf("historical rate on %s = %+v, selected=%t", test.date, selected.Rates, ok)
		}
	}
}

func TestCatalogRejectsOverlappingPeriodsAndVersionRollback(t *testing.T) {
	catalog := Embedded()
	duplicate := catalog.Entries[0]
	catalog.Entries = append(catalog.Entries, duplicate)
	data, _ := json.Marshal(catalog)
	if _, err := Parse(data); err == nil {
		t.Fatal("overlapping price periods accepted")
	}
	if err := catalog.ValidateCompatibleVersion(catalog.CatalogVersion + 1); err == nil {
		t.Fatal("older catalog accepted")
	}
}

func TestCatalogRejectsUnknownFieldsAndUnsafeSource(t *testing.T) {
	data := append([]byte(nil), embedded.CatalogJSON...)
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Entries[0].SourceURL = "https://example.com/pricing"
	changed, _ := json.Marshal(catalog)
	if _, err := Parse(changed); err == nil {
		t.Fatal("untrusted price source accepted")
	}
	if _, err := Parse([]byte(`{"schema_version":1,"catalog_version":1,"entries":[],"extra":true}`)); err == nil {
		t.Fatal("unknown catalog field accepted")
	}
	catalog = Embedded()
	catalog.Entries = append(catalog.Entries, Entry{Provider: "grok-build", Model: "grok-4.7-build", Basis: "xai-standard-api-short-context", EffectiveFrom: "2026-09-23", SourceURL: "https://docs.x.ai/developers/pricing", VerifiedAt: "2026-09-23", ShortContext: Rates{Input: "2", Output: "6"}})
	changed, _ = json.Marshal(catalog)
	if _, err := Parse(changed); err == nil {
		t.Fatal("unverified Grok Build model mapping accepted")
	}
}

func TestLegacyCatalogDoesNotInferOneHourCacheRate(t *testing.T) {
	catalog := Embedded()
	catalog.SchemaVersion = 1
	for _, entry := range catalog.Entries {
		if entry.Provider == "claude-code" && entry.Model == "claude-opus-5-5" {
			catalog.Entries = []Entry{entry}
			break
		}
	}
	for i := range catalog.Entries {
		catalog.Entries[i].ShortContext.CacheWrite1h = nil
		if catalog.Entries[i].LongContext != nil {
			catalog.Entries[i].LongContext.Rates.CacheWrite1h = nil
		}
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse("2006-01-02", "2026-09-24")
	selected, ok := legacy.Select("claude-code", "claude-opus-5-5", at.UnixMilli())
	if !ok || selected.Rates.CacheWrite == nil || *selected.Rates.CacheWrite != 5_000_000 || selected.Rates.CacheWrite1h != nil {
		t.Fatalf("legacy rates: %+v", selected.Rates)
	}
	rate := "8"
	catalog.Entries[0].ShortContext.CacheWrite1h = &rate
	data, _ = json.Marshal(catalog)
	if _, err := Parse(data); err == nil {
		t.Fatal("v1 accepted one-hour rate")
	}
}

func TestCatalogSplitsPricePeriodsAtUsageRequirementBoundary(t *testing.T) {
	catalog := Embedded()
	at := func(date string) int64 {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.UnixMilli()
	}
	want := []EffectivePeriod{
		{FromUnixMS: at("2026-09-22"), UntilUnixMS: at("2026-10-20")},
		{FromUnixMS: at("2026-10-20"), UntilUnixMS: at("2026-10-29"), Requirements: UsageRequirements{UnpricedCacheWrites: true}},
		{FromUnixMS: at("2026-10-29"), UntilUnixMS: math.MaxInt64, Requirements: UsageRequirements{UnpricedCacheWrites: true}},
	}
	periods := catalog.EffectivePeriods("codex", "gpt-6-sol")
	if !reflect.DeepEqual(periods, want) {
		t.Fatalf("effective periods = %+v, want %+v", periods, want)
	}
	for _, period := range periods {
		for _, timestamp := range []int64{period.FromUnixMS, period.UntilUnixMS - 1} {
			if period.UntilUnixMS == math.MaxInt64 && timestamp == math.MaxInt64-1 {
				continue
			}
			selected, ok := catalog.Select("codex", "gpt-6-sol", timestamp)
			if !ok || selected.Requirements != period.Requirements {
				t.Fatalf("selection at %d = %+v, selected=%t; period=%+v", timestamp, selected.Requirements, ok, period)
			}
		}
	}
	for _, schema := range []int{1, 2} {
		legacy := Catalog{SchemaVersion: schema, CatalogVersion: 1, Entries: []Entry{catalog.Entries[0]}}
		for _, entry := range catalog.Entries {
			if entry.Model == "gpt-6-sol" && entry.EffectiveFrom == "2026-09-22" {
				legacy.Entries[0] = entry
				legacy.Entries[0].EffectiveUntil = ""
				break
			}
		}
		data, _ := json.Marshal(legacy)
		parsed, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, date := range []string{"2026-10-20", "2026-10-29"} {
			selected, ok := parsed.Select("codex", "gpt-6-sol", at(date))
			if !ok || !selected.Requirements.UnpricedCacheWrites {
				t.Fatalf("schema %d at %s bypassed cache requirements: %+v", schema, date, selected)
			}
		}
	}
}

func TestHaikuCatalogRequiresCompleteContextRates(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Catalog)
	}{
		{"schema-v1", func(c *Catalog) { c.SchemaVersion = 1 }},
		{"schema-v2", func(c *Catalog) { c.SchemaVersion = 2 }},
		{"missing-high-tier", func(c *Catalog) { c.Entries[0].LongContext = nil }},
		{"wrong-threshold", func(c *Catalog) { c.Entries[0].LongContext.AboveInputTokens = 272_000 }},
		{"missing-low-cache-read", func(c *Catalog) { c.Entries[0].ShortContext.CachedInput = nil }},
		{"missing-low-cache-write", func(c *Catalog) { c.Entries[0].ShortContext.CacheWrite = nil }},
		{"missing-low-hour-write", func(c *Catalog) { c.Entries[0].ShortContext.CacheWrite1h = nil }},
		{"missing-high-cache-read", func(c *Catalog) { c.Entries[0].LongContext.Rates.CachedInput = nil }},
		{"missing-high-cache-write", func(c *Catalog) { c.Entries[0].LongContext.Rates.CacheWrite = nil }},
		{"missing-high-hour-write", func(c *Catalog) { c.Entries[0].LongContext.Rates.CacheWrite1h = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := Embedded()
			for _, entry := range catalog.Entries {
				if entry.Provider == "claude-code" && entry.Model == "claude-haiku-5-5" {
					catalog.Entries = []Entry{entry}
					break
				}
			}
			test.mutate(&catalog)
			data, _ := json.Marshal(catalog)
			if _, err := Parse(data); err == nil {
				t.Fatal("incomplete Haiku pricing contract accepted")
			}
		})
	}
}

func TestGrokCatalogAllowsOnlyVerifiedBuildModels(t *testing.T) {
	for _, model := range []string{"grok-build-0.1", "grok-code-fast-1", "grok-code-fast", "grok-code-fast-1-0825", "grok-build-0.2", "grok-code-fast-2", "grok-4.7-build-fast"} {
		for _, schema := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s/schema-%d", model, schema), func(t *testing.T) {
				catalog := Catalog{SchemaVersion: schema, CatalogVersion: 1, Entries: []Entry{{Provider: "grok-build", Model: model, Basis: "xai-standard-api-short-context", EffectiveFrom: "2026-10-29", SourceURL: "https://docs.x.ai/developers/pricing", VerifiedAt: "2026-10-29", ShortContext: Rates{Input: "1", Output: "2"}}}}
				data, _ := json.Marshal(catalog)
				_, err := Parse(data)
				want := schema == 3 && (model == "grok-build-0.1" || model == "grok-code-fast-1" || model == "grok-code-fast" || model == "grok-code-fast-1-0825")
				if (err == nil) != want {
					t.Fatalf("accepted=%t, want=%t: %v", err == nil, want, err)
				}
			})
		}
	}
}
