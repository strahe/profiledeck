//go:build !windows

package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/doctor"
)

func TestClaudeCodeDoctorReportsWeakFileLoginPermissions(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := initClaudeCodeTestRuntime(ctx, configDir); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := newClaudeCodeTestEnvironment(t, configDir).doctor.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finding := doctor.Finding{}
	for _, candidate := range result.Findings {
		if candidate.ID == "claude_code_credentials_permissions" {
			finding = candidate
			break
		}
	}
	if finding.ID == "" || finding.Level != doctor.LevelError || finding.Details["path"] != credentialPath {
		t.Fatalf("weak Claude Code login finding = %#v", finding)
	}
}
