package provider

import (
	"context"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/bootstrap"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

func TestListActiveProviderStatesMapsProvidersAndProfiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	initResult, err := bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	db, err := store.Open(ctx, initResult.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	defer db.Close()
	createProviderAndProfileForActiveState(t, ctx, db, "provider-a", "Provider A", "profile-a", "Profile A")
	createProviderAndProfileForActiveState(t, ctx, db, "provider-b", "Provider B", "profile-b", "Profile B")
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "provider-no-active", Name: "Provider No Active", AdapterID: "generic", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected inactive provider create to succeed, got %v", err)
	}
	completeActiveStateSwitch(t, ctx, db, "switch-a", "provider-a", "profile-a")
	completeActiveStateSwitch(t, ctx, db, "switch-a-repeat", "provider-a", "profile-a")
	completeActiveStateSwitch(t, ctx, db, "switch-b", "provider-b", "profile-b")
	if err := db.DeleteResolvedProviderOperations(ctx, "provider-a"); err != nil {
		t.Fatalf("delete resolved operation history: %v", err)
	}

	service := NewService(runtimeService.StoreFactory(), nil, agent.MustRegistry())
	states, err := service.ListActiveStates(ctx)
	if err != nil {
		t.Fatalf("expected active states list to succeed, got %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected inactive provider to be skipped, got %#v", states)
	}

	byProvider := map[string]ActiveState{}
	for _, state := range states {
		byProvider[state.ProviderID] = state
	}
	assertActiveProviderState(t, byProvider["provider-a"], "Provider A", "profile-a", "Profile A", 2, true)
	assertActiveProviderState(t, byProvider["provider-b"], "Provider B", "profile-b", "Profile B", 1, true)
}

func createProviderAndProfileForActiveState(t *testing.T, ctx context.Context, db *store.Store, providerID, providerName, profileID, profileName string) {
	t.Helper()

	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: providerID, Name: providerName, AdapterID: "generic", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID: profileID, Name: profileName, MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
}

func completeActiveStateSwitch(t *testing.T, ctx context.Context, db *store.Store, operationID, providerID, profileID string) {
	t.Helper()

	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:                    operationID,
		ProviderID:            providerID,
		ProfileIDs:            []string{profileID},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
		MetadataJSON:          `{}`,
	}); err != nil {
		t.Fatalf("expected switch operation create to succeed, got %v", err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:                    operationID,
		ProfileID:             profileID,
		ProviderID:            providerID,
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
		MetadataJSON:          `{}`,
	}); err != nil {
		t.Fatalf("expected switch operation completion to succeed, got %v", err)
	}
}

func assertActiveProviderState(t *testing.T, state ActiveState, providerName, profileID, profileName string, revision int64, profileAvailable bool) {
	t.Helper()

	if state.ProviderName != providerName ||
		state.ProfileID != profileID ||
		state.ProfileName != profileName ||
		state.Revision != revision ||
		state.UpdatedAtUnixMS == 0 ||
		state.ProfileAvailable != profileAvailable {
		t.Fatalf("unexpected active state: %#v", state)
	}
}
