//go:build unix

package app

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBuildPlanRejectsNonRegularTarget(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	fifoPath := filepath.Join(t.TempDir(), "target.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("fifo not available: %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
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

	_, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorTargetReadFailed)
}
