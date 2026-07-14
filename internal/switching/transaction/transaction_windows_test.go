//go:build windows

package transaction

import (
	"context"
	"os"
	"testing"

	"github.com/strahe/profiledeck/internal/switching/target"
)

func TestVerifyAppliedDoesNotComparePOSIXPermissionsOnWindows(t *testing.T) {
	t.Parallel()

	verifyCalled := false
	backend := transactionTestBackend{
		id: "windows-mode-test",
		verify: func(_ context.Context, _ target.Spec, _ target.Snapshot) error {
			verifyCalled = true
			return nil
		},
		inspect: func(context.Context, target.Spec) (target.Snapshot, error) {
			t.Fatal("VerifyApplied() inspected POSIX permissions on Windows")
			return target.Snapshot{}, nil
		},
	}
	executor := New(target.MustRegistry(backend))
	err := executor.VerifyApplied(context.Background(), []Operation{{
		TargetID:       "target-a",
		BackendID:      backend.id,
		Action:         ActionUpdate,
		DesiredContent: "after",
		DesiredMode:    os.FileMode(0o600),
		UseDesiredMode: true,
		Spec:           transactionTestSpec{id: "target-a", backendID: backend.id},
		Snapshot:       target.Snapshot{Exists: true, Fingerprint: target.SHA256String("before")},
	}})
	if err != nil {
		t.Fatalf("VerifyApplied() error = %v", err)
	}
	if !verifyCalled {
		t.Fatal("VerifyApplied() skipped target content verification")
	}
}
