package doctor

import "testing"

func TestGeneratedMaintenanceLockOwnerIsProviderNeutral(t *testing.T) {
	owner := "future-agent-profile-create-1700000000000-0123456789ab"
	if !isProfileDeckOperationID(owner) || !isMaintenanceLockOwner(owner) {
		t.Fatalf("generated maintenance owner was not recognized: %q", owner)
	}
	for _, invalid := range []string{
		"external-owner",
		"future-agent-profile-create-not-a-time-0123456789ab",
		"future-agent-profile-create-1700000000000-not-hex-value",
	} {
		if isProfileDeckOperationID(invalid) || isMaintenanceLockOwner(invalid) {
			t.Fatalf("invalid maintenance owner was accepted: %q", invalid)
		}
	}
}
