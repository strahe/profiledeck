//go:build linux

package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/switching"
)

func TestClaudeCodeLinuxSwitchRepairsCredentialMode(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := initClaudeCodeTestRuntime(ctx, configDir); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	if _, err := newClaudeCodeTestEnvironment(t, configDir).claudeCode.CreateProfile(ctx, CreateClaudeCodeProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := newClaudeCodeTestEnvironment(t, configDir).switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate || plan.Operations[0].StatusReason != planReasonTargetModeDifferent {
		t.Fatalf("permission repair plan = %#v", plan.Operations)
	}
	if _, err := newClaudeCodeTestEnvironment(t, configDir).switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: claudecodeconfig.ProviderID, ProfileID: "work",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestClaudeCodeLinuxRollbackKeepsCredentialModePrivate(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := initClaudeCodeTestRuntime(ctx, configDir); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-a", "refresh-a", 4102444800000))
	if _, err := newClaudeCodeTestEnvironment(t, configDir).claudeCode.CreateProfile(ctx, CreateClaudeCodeProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-b", "refresh-b", 4102444800000))
	if _, err := newClaudeCodeTestEnvironment(t, configDir).claudeCode.CreateProfile(ctx, CreateClaudeCodeProfileRequest{ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}

	switched, err := newClaudeCodeTestEnvironment(t, configDir).switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClaudeCodeTestEnvironment(t, configDir).switching.Rollback(ctx, switching.ApplyRollbackRequest{BackupID: switched.OperationID, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "access-b")
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rolled back credential mode = %o, want 0600", info.Mode().Perm())
	}
}
