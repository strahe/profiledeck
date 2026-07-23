//go:build unix

package switching

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
)

func TestBuildPlanRejectsNonRegularTarget(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir)

	fifoPath := filepath.Join(t.TempDir(), "target.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("fifo not available: %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-fifo",
		Path:       fifoPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"ok"}`,
	}); err != nil {
		t.Fatalf("expected fifo target create to succeed, got %v", err)
	}

	_, err := newSwitchingTestEnvironment(t, configDir).service.BuildPlan(ctx, BuildPlanRequest{ProviderID: "provider-a", ProfileID: "profile-a"})
	assertErrorCode(t, err, apperror.TargetReadFailed)
}
