package targetformat

import "testing"

func TestNewRegistryRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		definitions []Definition
	}{
		{
			name:        "empty format",
			definitions: []Definition{{Format: "", Strategies: []string{"replace"}}},
		},
		{
			name:        "non-canonical format",
			definitions: []Definition{{Format: " text", Strategies: []string{"replace"}}},
		},
		{
			name: "duplicate format",
			definitions: []Definition{
				{Format: "text", Strategies: []string{"replace"}},
				{Format: "text", Strategies: []string{"merge"}},
			},
		},
		{
			name:        "missing strategy",
			definitions: []Definition{{Format: "text"}},
		},
		{
			name:        "empty strategy",
			definitions: []Definition{{Format: "text", Strategies: []string{""}}},
		},
		{
			name:        "non-canonical strategy",
			definitions: []Definition{{Format: "text", Strategies: []string{"replace "}}},
		},
		{
			name:        "duplicate pair",
			definitions: []Definition{{Format: "text", Strategies: []string{"replace", "replace"}}},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewRegistry(test.definitions...); err == nil {
				t.Fatal("NewRegistry() accepted an invalid definition")
			}
		})
	}
}

func TestBuiltinRegistryFormatStrategyContract(t *testing.T) {
	t.Parallel()

	registry := BuiltinRegistry()
	allowed := []struct {
		format   string
		strategy string
	}{
		{FormatText, StrategyReplaceFile},
		{FormatJSON, StrategyReplaceFile},
		{FormatJSON, StrategyJSONMerge},
		{FormatTOML, StrategyReplaceFile},
		{FormatTOML, StrategyTOMLMerge},
		{FormatEnv, StrategyReplaceFile},
		{FormatEnv, StrategyEnvMerge},
	}
	for _, pair := range allowed {
		if !registry.AllowsCanonical(pair.format, pair.strategy) {
			t.Errorf("AllowsCanonical(%q, %q) = false, want true", pair.format, pair.strategy)
		}
	}

	rejected := []struct {
		format   string
		strategy string
	}{
		{FormatText, StrategyJSONMerge},
		{FormatJSON, StrategyTOMLMerge},
		{FormatTOML, StrategyEnvMerge},
		{FormatEnv, StrategyJSONMerge},
		{"unknown", StrategyReplaceFile},
		{FormatJSON, "unknown"},
		{" " + FormatJSON, StrategyJSONMerge},
		{FormatJSON, StrategyJSONMerge + " "},
	}
	for _, pair := range rejected {
		if registry.AllowsCanonical(pair.format, pair.strategy) {
			t.Errorf("AllowsCanonical(%q, %q) = true, want false", pair.format, pair.strategy)
		}
	}
}
