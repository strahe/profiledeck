// Package targetformat owns the versioned runtime registry for generic file
// target formats and update strategies.
package targetformat

import (
	"fmt"
	"strings"
)

const (
	FormatText = "text"
	FormatJSON = "json"
	FormatTOML = "toml"
	FormatEnv  = "env"

	StrategyReplaceFile = "replace-file"
	StrategyJSONMerge   = "json-merge"
	StrategyTOMLMerge   = "toml-merge"
	StrategyEnvMerge    = "env-merge"
)

type Definition struct {
	Format     string
	Strategies []string
}

// Registry is immutable after construction so Store writes and integrity
// checks always resolve the same format/strategy contract.
type Registry struct {
	formats    map[string]struct{}
	strategies map[string]struct{}
	pairs      map[string]struct{}
}

func NewRegistry(definitions ...Definition) (Registry, error) {
	registry := Registry{
		formats:    make(map[string]struct{}, len(definitions)),
		strategies: make(map[string]struct{}),
		pairs:      make(map[string]struct{}),
	}
	for _, definition := range definitions {
		format := strings.TrimSpace(definition.Format)
		if format == "" || format != definition.Format {
			return Registry{}, fmt.Errorf("target format must be canonical")
		}
		if _, exists := registry.formats[format]; exists {
			return Registry{}, fmt.Errorf("target format %q is duplicated", format)
		}
		if len(definition.Strategies) == 0 {
			return Registry{}, fmt.Errorf("target format %q has no strategies", format)
		}
		registry.formats[format] = struct{}{}
		for _, rawStrategy := range definition.Strategies {
			strategy := strings.TrimSpace(rawStrategy)
			if strategy == "" || strategy != rawStrategy {
				return Registry{}, fmt.Errorf("target strategy must be canonical")
			}
			key := pairKey(format, strategy)
			if _, exists := registry.pairs[key]; exists {
				return Registry{}, fmt.Errorf("target format/strategy pair %q/%q is duplicated", format, strategy)
			}
			registry.strategies[strategy] = struct{}{}
			registry.pairs[key] = struct{}{}
		}
	}
	return registry, nil
}

func MustRegistry(definitions ...Definition) Registry {
	registry, err := NewRegistry(definitions...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry Registry) HasFormat(format string) bool {
	_, ok := registry.formats[strings.TrimSpace(format)]
	return ok
}

func (registry Registry) HasStrategy(strategy string) bool {
	_, ok := registry.strategies[strings.TrimSpace(strategy)]
	return ok
}

func (registry Registry) Allows(format, strategy string) bool {
	format = strings.TrimSpace(format)
	strategy = strings.TrimSpace(strategy)
	_, ok := registry.pairs[pairKey(format, strategy)]
	return ok
}

func (registry Registry) AllowsCanonical(format, strategy string) bool {
	return format == strings.TrimSpace(format) &&
		strategy == strings.TrimSpace(strategy) &&
		registry.Allows(format, strategy)
}

func pairKey(format, strategy string) string {
	return format + "\x00" + strategy
}

var builtinRegistry = MustRegistry(
	Definition{
		Format: FormatText,
		Strategies: []string{
			StrategyReplaceFile,
		},
	},
	Definition{
		Format: FormatJSON,
		Strategies: []string{
			StrategyReplaceFile,
			StrategyJSONMerge,
		},
	},
	Definition{
		Format: FormatTOML,
		Strategies: []string{
			StrategyReplaceFile,
			StrategyTOMLMerge,
		},
	},
	Definition{
		Format: FormatEnv,
		Strategies: []string{
			StrategyReplaceFile,
			StrategyEnvMerge,
		},
	},
)

func BuiltinRegistry() Registry {
	return builtinRegistry
}
