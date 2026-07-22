package usage

import (
	"context"
	"fmt"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Integration interface {
	ProviderID() string
	SourceIDs() []string
	Sync(context.Context, store.Factory) (UsageSyncResult, error)
	PricingInfo() UsagePricingInfo
}

// Registry is immutable after construction. Provider and source ownership is
// fixed by application composition rather than runtime configuration.
type Registry struct {
	providerIDs []string
	byProvider  map[string]Integration
}

func NewRegistry(integrations ...Integration) (Registry, error) {
	registry := Registry{
		providerIDs: make([]string, 0, len(integrations)),
		byProvider:  make(map[string]Integration, len(integrations)),
	}
	sourceOwners := make(map[string]string)
	for _, integration := range integrations {
		if integration == nil {
			return Registry{}, fmt.Errorf("usage integration is required")
		}
		providerID, appErr := validate.ID(integration.ProviderID(), apperror.UsageInvalid)
		if appErr != nil {
			return Registry{}, fmt.Errorf("usage integration provider is invalid: %w", appErr)
		}
		if _, exists := registry.byProvider[providerID]; exists {
			return Registry{}, fmt.Errorf("usage Provider %q is duplicated", providerID)
		}
		sources := integration.SourceIDs()
		if len(sources) == 0 {
			return Registry{}, fmt.Errorf("usage Provider %q has no sources", providerID)
		}
		seen := make(map[string]struct{}, len(sources))
		for _, rawSourceID := range sources {
			sourceID, sourceErr := validate.ID(rawSourceID, apperror.UsageInvalid)
			if sourceErr != nil {
				return Registry{}, fmt.Errorf("usage Provider %q source is invalid: %w", providerID, sourceErr)
			}
			if _, exists := seen[sourceID]; exists {
				return Registry{}, fmt.Errorf("usage Provider %q source %q is duplicated", providerID, sourceID)
			}
			if owner, exists := sourceOwners[sourceID]; exists {
				return Registry{}, fmt.Errorf("usage source %q is owned by both %q and %q", sourceID, owner, providerID)
			}
			seen[sourceID] = struct{}{}
			sourceOwners[sourceID] = providerID
		}
		registry.providerIDs = append(registry.providerIDs, providerID)
		registry.byProvider[providerID] = integration
	}
	return registry, nil
}

func MustRegistry(integrations ...Integration) Registry {
	registry, err := NewRegistry(integrations...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry Registry) Integration(providerID string) (Integration, bool) {
	integration, ok := registry.byProvider[strings.TrimSpace(providerID)]
	return integration, ok
}

func (registry Registry) ProviderIDs() []string {
	providers := make([]string, len(registry.providerIDs))
	copy(providers, registry.providerIDs)
	return providers
}
