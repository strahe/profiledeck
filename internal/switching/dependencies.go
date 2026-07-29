package switching

import (
	"github.com/strahe/profiledeck/internal/providercoord"
	"github.com/strahe/profiledeck/internal/switching/plan"
	"github.com/strahe/profiledeck/internal/switching/target"
)

// Dependencies are immutable switch collaborators supplied at application composition.
type Dependencies struct {
	Targets      target.Registry
	Adapters     plan.Registry
	Coordinators providercoord.Registry
}

type DependencyOption func(*Dependencies)

// WithProviderCoordinators installs provider-specific external write guards.
func WithProviderCoordinators(registry providercoord.Registry) DependencyOption {
	return func(dependencies *Dependencies) {
		dependencies.Coordinators = registry
	}
}

func NewDependencies(targets target.Registry, adapters plan.Registry, options ...DependencyOption) Dependencies {
	dependencies := Dependencies{Targets: targets, Adapters: adapters}
	for _, option := range options {
		if option != nil {
			option(&dependencies)
		}
	}
	return dependencies
}
