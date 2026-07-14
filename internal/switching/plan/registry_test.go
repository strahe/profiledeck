package plan

import "testing"

type managedTestAdapter struct {
	GenericAdapter
	id        string
	providers []string
}

func (adapter managedTestAdapter) ID() string { return adapter.id }

func (adapter managedTestAdapter) ManagedProviderIDs() []string {
	return append([]string(nil), adapter.providers...)
}

func TestRegistryRejectsManagedProviderOwnershipConflict(t *testing.T) {
	_, err := NewRegistry(
		managedTestAdapter{id: "first", providers: []string{"shared"}},
		managedTestAdapter{id: "second", providers: []string{"shared"}},
	)
	if err == nil {
		t.Fatal("plan Registry accepted duplicate managed Provider ownership")
	}
}

func TestRegistryRejectsAmbiguousManagedProviderIDs(t *testing.T) {
	for _, providerID := range []string{"", " provider"} {
		_, err := NewRegistry(managedTestAdapter{id: "managed", providers: []string{providerID}})
		if err == nil {
			t.Fatalf("plan Registry accepted ambiguous managed Provider id %q", providerID)
		}
	}
}

func TestRegistryReturnsManagedProviderCopies(t *testing.T) {
	registry, err := NewRegistry(managedTestAdapter{id: "managed", providers: []string{"provider"}})
	if err != nil {
		t.Fatalf("create plan Registry: %v", err)
	}
	providerIDs := registry.ManagedProviderIDs()
	providerIDs[0] = "mutated"
	adapterID, ok := registry.ManagedAdapterID("provider")
	if !ok || adapterID != "managed" {
		t.Fatalf("returned Provider IDs mutated Registry: adapter=%q ok=%t", adapterID, ok)
	}
}
