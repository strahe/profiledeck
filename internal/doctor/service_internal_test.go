package doctor

import (
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPermissionInspectionFailureExcludesRawError(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("private", 1024))
	findings := inspectPathPermission(pathPermissionCheck{
		path: path, want: 0o600, id: "test_permissions", level: LevelError, message: "private path is accessible",
	})
	if len(findings) != 1 || findings[0].ID != "test_permissions_inspect_failed" {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	if findings[0].Details["path"] != path || findings[0].Details["error"] != nil {
		t.Fatalf("expected only the inspected path, got %#v", findings[0].Details)
	}
}

func TestMissingSensitivePathHasNoPermissionFinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "auth.json")
	findings := inspectPathPermission(pathPermissionCheck{
		path: path, want: 0o600, id: "test_permissions", level: LevelError, message: "private path may be accessible",
	})
	if len(findings) != 0 {
		t.Fatalf("missing path findings = %#v", findings)
	}
}
