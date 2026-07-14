package plan

import (
	"fmt"
	"strings"
)

// Registry resolves a fixed set of plan adapters. It is immutable after construction.
type Registry struct {
	adapters           map[string]Adapter
	managedProviders   map[string]string
	managedProviderIDs []string
}

func NewRegistry(adapters ...Adapter) (Registry, error) {
	registry := Registry{
		adapters:         make(map[string]Adapter, len(adapters)),
		managedProviders: make(map[string]string),
	}
	for _, adapter := range adapters {
		if adapter == nil {
			return Registry{}, fmt.Errorf("switch plan adapter is invalid")
		}
		adapterID := adapter.ID()
		if adapterID == "" || adapterID != strings.TrimSpace(adapterID) {
			return Registry{}, fmt.Errorf("switch plan adapter id %q is invalid", adapterID)
		}
		if _, exists := registry.adapters[adapterID]; exists {
			return Registry{}, fmt.Errorf("switch plan adapter %q is duplicated", adapterID)
		}
		registry.adapters[adapterID] = adapter
		for _, providerID := range adapter.ManagedProviderIDs() {
			if providerID == "" || providerID != strings.TrimSpace(providerID) {
				return Registry{}, fmt.Errorf("switch plan adapter %q has an invalid managed Provider id %q", adapterID, providerID)
			}
			if owner, exists := registry.managedProviders[providerID]; exists {
				return Registry{}, fmt.Errorf("Provider %q is managed by both switch plan adapters %q and %q", providerID, owner, adapterID)
			}
			registry.managedProviders[providerID] = adapterID
			registry.managedProviderIDs = append(registry.managedProviderIDs, providerID)
		}
	}
	return registry, nil
}

func MustRegistry(adapters ...Adapter) Registry {
	registry, err := NewRegistry(adapters...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry Registry) Adapter(id string) (Adapter, bool) {
	adapter, ok := registry.adapters[id]
	return adapter, ok
}

func (registry Registry) ManagedAdapter(providerID string) (Adapter, bool) {
	adapterID, ok := registry.managedProviders[providerID]
	if !ok {
		return nil, false
	}
	return registry.Adapter(adapterID)
}

func (registry Registry) ManagedAdapterID(providerID string) (string, bool) {
	adapterID, ok := registry.managedProviders[providerID]
	return adapterID, ok
}

func (registry Registry) ManagedProviderIDs() []string {
	return append([]string(nil), registry.managedProviderIDs...)
}
