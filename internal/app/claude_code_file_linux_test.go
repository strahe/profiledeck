//go:build linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
)

func TestClaudeCodeLinuxSwitchRepairsCredentialMode(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate || plan.Operations[0].StatusReason != planReasonTargetModeDifferent {
		t.Fatalf("permission repair plan = %#v", plan.Operations)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work",
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
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-a", "refresh-a", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-b", "refresh-b", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}

	switched, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: switched.OperationID, Confirm: true}); err != nil {
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
