package pricing

import (
	"encoding/json"
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
	if err := catalog.ValidateCompatibleVersion(2); err == nil {
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
