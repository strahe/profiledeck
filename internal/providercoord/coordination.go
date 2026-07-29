// Package providercoord defines provider-specific guards for external writes.
package providercoord

import (
	"context"
	"fmt"
	"strings"
)

// Target identifies an external object that an operation may inspect or write.
// It contains only coordination inputs and is never a public DTO.
type Target struct {
	ID        string
	BackendID string
	Path      string
}

// Request describes one provider critical section.
type Request struct {
	OperationID          string
	ProviderID           string
	ProviderMetadataJSON string
	Targets              []Target
}

// Guard proves that a caller still owns the provider critical section.
type Guard interface {
	Validate(context.Context) error
	Release()
}

// Coordinator acquires the external coordination primitive for one Provider.
type Coordinator interface {
	Acquire(context.Context, Request) (Guard, error)
}

// Registration binds one Provider to its coordinator.
type Registration struct {
	ProviderID  string
	Coordinator Coordinator
}

// Registry is immutable after construction.
type Registry struct {
	coordinators map[string]Coordinator
}

func NewRegistry(registrations ...Registration) (Registry, error) {
	registry := Registry{coordinators: make(map[string]Coordinator, len(registrations))}
	for _, registration := range registrations {
		providerID := strings.TrimSpace(registration.ProviderID)
		if providerID == "" || providerID != registration.ProviderID {
			return Registry{}, fmt.Errorf("provider coordinator id %q is invalid", registration.ProviderID)
		}
		if registration.Coordinator == nil {
			return Registry{}, fmt.Errorf("provider coordinator %q is invalid", providerID)
		}
		if _, exists := registry.coordinators[providerID]; exists {
			return Registry{}, fmt.Errorf("provider coordinator %q is duplicated", providerID)
		}
		registry.coordinators[providerID] = registration.Coordinator
	}
	return registry, nil
}

func MustRegistry(registrations ...Registration) Registry {
	registry, err := NewRegistry(registrations...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry Registry) Coordinator(providerID string) (Coordinator, bool) {
	coordinator, ok := registry.coordinators[strings.TrimSpace(providerID)]
	return coordinator, ok
}
