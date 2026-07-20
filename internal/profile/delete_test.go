package profile

import (
	"context"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

type testDeleteParticipant struct {
	providerID string
}

func (participant testDeleteParticipant) ProviderID() string { return participant.providerID }

func (testDeleteParticipant) DeleteProfileData(context.Context, *store.Store, string) error {
	return nil
}

func TestDeleteRegistryRejectsInvalidParticipants(t *testing.T) {
	if _, err := NewDeleteRegistry(nil); err == nil {
		t.Fatal("nil delete participant was accepted")
	}
	if _, err := NewDeleteRegistry(testDeleteParticipant{}); err == nil {
		t.Fatal("empty Provider ID was accepted")
	}
	if _, err := NewDeleteRegistry(
		testDeleteParticipant{providerID: "codex"},
		testDeleteParticipant{providerID: " codex "},
	); err == nil {
		t.Fatal("duplicate Provider participant was accepted")
	}
}
