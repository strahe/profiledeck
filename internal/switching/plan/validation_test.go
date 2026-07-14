package plan

import (
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching/target"
)

func TestValidateResultRejectsChangedTargetAndInvalidNoop(t *testing.T) {
	input := Input{Provider: Provider{ID: "provider"}, Profile: Profile{ID: "profile"}}
	path := filepath.Join(t.TempDir(), "target.json")
	preparedSpec := target.FileSpec{ID: "target", Path: path, NeedsContent: true, Label: path}
	snapshot := target.Snapshot{Exists: true, Fingerprint: target.SHA256String("before"), Content: "before"}
	prepared := Prepared{Targets: []PreparedTarget{{Spec: preparedSpec}}}
	snapshots := map[string]target.Snapshot{"target": snapshot}
	base := ApplyOperation{
		Operation: Operation{
			ProviderID: "provider", ProfileID: "profile", TargetID: "target", BackendID: target.BackendFile,
			TargetLabel: path, Path: path, Action: ActionNoop, FileExists: true,
			BeforeSHA256: snapshot.Fingerprint, DesiredSHA256: snapshot.Fingerprint,
			LocatorFingerprint: preparedSpec.LocatorFingerprint(),
		},
		DesiredContent: "before", Spec: preparedSpec, Snapshot: snapshot,
	}

	changedSpec := base
	changedSpec.Spec = target.FileSpec{ID: "target", Path: filepath.Join(t.TempDir(), "other.json"), NeedsContent: true, Label: path}
	assertPlanBuildFailure(t, ValidateResult(input, prepared, snapshots, Result{Operations: []ApplyOperation{changedSpec}}))

	changedPath := base
	changedPath.Path = filepath.Join(t.TempDir(), "misleading.json")
	assertPlanBuildFailure(t, ValidateResult(input, prepared, snapshots, Result{Operations: []ApplyOperation{changedPath}}))

	invalidNoop := base
	invalidNoop.DesiredContent = "different"
	invalidNoop.DesiredSHA256 = target.SHA256String("different")
	assertPlanBuildFailure(t, ValidateResult(input, prepared, snapshots, Result{Operations: []ApplyOperation{invalidNoop}}))
}

type recoverySpec struct {
	id string
}

func (spec recoverySpec) BackendID() string          { return "recovery" }
func (spec recoverySpec) TargetID() string           { return spec.id }
func (spec recoverySpec) SafeLabel() string          { return "recovery target" }
func (spec recoverySpec) LocatorFingerprint() string { return target.SHA256String(spec.id) }
func (spec recoverySpec) Sensitive() bool            { return true }
func (spec recoverySpec) RecoveryLocator() string    { return "saved-locator" }
func (spec recoverySpec) ObjectFingerprint(snapshot target.Snapshot) string {
	return target.SHA256String(snapshot.OpaqueState)
}

func TestValidateResultBindsRecoveryIdentityToInspectedObject(t *testing.T) {
	input := Input{Provider: Provider{ID: "provider"}, Profile: Profile{ID: "profile"}}
	spec := recoverySpec{id: "target"}
	snapshot := target.Snapshot{Exists: true, Fingerprint: target.SHA256String("before"), Content: "before", OpaqueState: "object-reference"}
	prepared := Prepared{Targets: []PreparedTarget{{Spec: spec}}}
	snapshots := map[string]target.Snapshot{"target": snapshot}
	operation := ApplyOperation{
		Operation: Operation{
			ProviderID: "provider", ProfileID: "profile", TargetID: "target", BackendID: spec.BackendID(),
			TargetLabel: spec.SafeLabel(), Action: ActionNoop, FileExists: true, Sensitive: true,
			BeforeSHA256: snapshot.Fingerprint, DesiredSHA256: snapshot.Fingerprint,
			LocatorFingerprint: spec.LocatorFingerprint(), PrivateRecoveryLocator: spec.RecoveryLocator(),
			PrivateObjectFingerprint: spec.ObjectFingerprint(snapshot),
		},
		DesiredContent: "before", Spec: spec, Snapshot: snapshot,
	}
	if err := ValidateResult(input, prepared, snapshots, Result{Operations: []ApplyOperation{operation}}); err != nil {
		t.Fatalf("valid recovery identity rejected: %v", err)
	}

	operation.PrivateObjectFingerprint = target.SHA256String("replacement")
	assertPlanBuildFailure(t, ValidateResult(input, prepared, snapshots, Result{Operations: []ApplyOperation{operation}}))
}

func assertPlanBuildFailure(t *testing.T, err *apperror.Error) {
	t.Helper()
	if err == nil || err.Code != apperror.PlanBuildFailed {
		t.Fatalf("error = %v, want %q", err, apperror.PlanBuildFailed)
	}
}
