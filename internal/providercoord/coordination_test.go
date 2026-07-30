package providercoord

import (
	"context"
	"strings"
	"testing"
)

type testCoordinator struct{}

func (testCoordinator) Acquire(context.Context, Request) (Guard, error) {
	return testGuard{}, nil
}

type testGuard struct{}

func (testGuard) Validate(context.Context) error { return nil }
func (testGuard) Release()                       {}

func TestRegistryResolvesRegisteredCoordinator(t *testing.T) {
	coordinator := testCoordinator{}
	registry, err := NewRegistry(Registration{ProviderID: "provider-a", Coordinator: coordinator})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := registry.Coordinator("provider-a"); !ok {
		t.Fatal("registered coordinator was not resolved")
	}
	if _, ok := registry.Coordinator("provider-b"); ok {
		t.Fatal("unexpected coordinator for unregistered Provider")
	}
}

func TestRegistryRejectsInvalidRegistrations(t *testing.T) {
	tests := []struct {
		name          string
		registrations []Registration
		want          string
	}{
		{
			name:          "empty provider",
			registrations: []Registration{{ProviderID: "", Coordinator: testCoordinator{}}},
			want:          "invalid",
		},
		{
			name:          "trimmed provider",
			registrations: []Registration{{ProviderID: " provider-a", Coordinator: testCoordinator{}}},
			want:          "invalid",
		},
		{
			name:          "nil coordinator",
			registrations: []Registration{{ProviderID: "provider-a"}},
			want:          "invalid",
		},
		{
			name: "duplicate provider",
			registrations: []Registration{
				{ProviderID: "provider-a", Coordinator: testCoordinator{}},
				{ProviderID: "provider-a", Coordinator: testCoordinator{}},
			},
			want: "duplicated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRegistry(test.registrations...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewRegistry error = %v, want %q", err, test.want)
			}
		})
	}
}
