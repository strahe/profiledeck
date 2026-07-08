package app

import (
	"context"
	"database/sql"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

func TestListActiveProviderStatesMapsProvidersAndProfiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createProviderAndProfileForActiveState(t, ctx, configDir, "provider-a", "Provider A", "profile-a", "Profile A")
	createProviderAndProfileForActiveState(t, ctx, configDir, "provider-b", "Provider B", "profile-b", "Profile B")
	createProviderAndProfileForActiveState(t, ctx, configDir, "provider-missing-profile", "Provider Missing Profile", "profile-missing", "Profile Missing")
	if _, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-no-active",
		Name:      "Provider No Active",
		AdapterID: "generic",
	}); err != nil {
		t.Fatalf("expected inactive provider create to succeed, got %v", err)
	}

	db, err := store.Open(ctx, initResult.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	defer db.Close()
	completeActiveStateSwitch(t, ctx, db, "switch-a", "provider-a", "profile-a")
	completeActiveStateSwitch(t, ctx, db, "switch-b", "provider-b", "profile-b")
	completeActiveStateSwitch(t, ctx, db, "switch-missing", "provider-missing-profile", "profile-missing")
	deleteProfileRow(t, initResult.DatabasePath, "profile-missing")

	states, err := ListActiveProviderStates(ctx, ListActiveProviderStatesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected active states list to succeed, got %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("expected inactive provider to be skipped, got %#v", states)
	}

	byProvider := map[string]ActiveProviderState{}
	for _, state := range states {
		byProvider[state.ProviderID] = state
	}
	assertActiveProviderState(t, byProvider["provider-a"], "Provider A", "profile-a", "Profile A", "switch-a", true)
	assertActiveProviderState(t, byProvider["provider-b"], "Provider B", "profile-b", "Profile B", "switch-b", true)
	assertActiveProviderState(t, byProvider["provider-missing-profile"], "Provider Missing Profile", "profile-missing", "", "switch-missing", false)
}

func createProviderAndProfileForActiveState(t *testing.T, ctx context.Context, configDir string, providerID string, providerName string, profileID string, profileName string) {
	t.Helper()

	if _, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        providerID,
		Name:      providerName,
		AdapterID: "generic",
	}); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        profileID,
		Name:      profileName,
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
}

func completeActiveStateSwitch(t *testing.T, ctx context.Context, db *store.Store, operationID string, providerID string, profileID string) {
	t.Helper()

	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected switch operation create to succeed, got %v", err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		ProviderID:   providerID,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected switch operation completion to succeed, got %v", err)
	}
}

func deleteProfileRow(t *testing.T, databasePath string, profileID string) {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("DELETE FROM profiles WHERE id = ?", profileID); err != nil {
		t.Fatalf("expected profile row delete to succeed, got %v", err)
	}
}

func assertActiveProviderState(t *testing.T, state ActiveProviderState, providerName string, profileID string, profileName string, operationID string, profileAvailable bool) {
	t.Helper()

	if state.ProviderName != providerName ||
		state.ProfileID != profileID ||
		state.ProfileName != profileName ||
		state.OperationID != operationID ||
		state.UpdatedAtUnixMS == 0 ||
		state.ProfileAvailable != profileAvailable {
		t.Fatalf("unexpected active state: %#v", state)
	}
}
