package switching

import (
	"github.com/strahe/profiledeck/internal/switching/plan"
	"github.com/strahe/profiledeck/internal/switching/target"
)

// Dependencies are immutable switch collaborators supplied at application composition.
type Dependencies struct {
	Targets  target.Registry
	Adapters plan.Registry
}

func NewDependencies(targets target.Registry, adapters plan.Registry) Dependencies {
	return Dependencies{Targets: targets, Adapters: adapters}
}
