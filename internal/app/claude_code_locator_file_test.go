//go:build linux || windows

package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
)

func TestClaudeCodeFirstCapturePersistsFileLocatorAcrossEnvironmentChanges(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", firstRoot)
	firstPath := filepath.Join(firstRoot, claudecodeconfig.CredentialsFile)
	writeClaudeCodeCredential(t, firstPath, testClaudeCodePayload("first", "first-refresh", 4102444800000))
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", secondRoot)
	writeClaudeCodeCredential(t, filepath.Join(secondRoot, claudecodeconfig.CredentialsFile), testClaudeCodePayload("second", "second-refresh", 4102444800000))
	detect, err := ClaudeCodeDetect(ctx, ClaudeCodeDetectRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if detect.CredentialStatus != "valid" || !detect.ProviderEnabled || !strings.Contains(strings.Join(detect.Warnings, "\n"), "different CLAUDE_CONFIG_DIR") {
		t.Fatalf("detect after environment change = %#v", detect)
	}
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Path != firstPath || plan.Operations[0].Action != planActionNoop {
		t.Fatalf("saved locator plan = %#v", plan.Operations)
	}
}
