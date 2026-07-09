package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCreateCodexProfileCreatesIndependentCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"same-account","access_token":"token-1"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected first create to succeed, got %v", err)
	}
	completeCodexProfileSwitchForTest(t, ctx, configDir, "switch-active", "work")

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-mini"`+"\n", `{"tokens":{"account_id":"same-account","access_token":"token-2"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "personal"}); err != nil {
		t.Fatalf("expected second create to succeed without auth binding choice, got %v", err)
	}

	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"same-account","access_token":"live"}}`), 0o600); err != nil {
		t.Fatalf("expected live auth mutation to succeed, got %v", err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: codexconfig.ProviderID, ProfileID: "work", Confirm: true}); err != nil {
		t.Fatalf("expected work switch to succeed, got %v", err)
	}
	assertJSONFile(t, authPath, "token-1")
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: codexconfig.ProviderID, ProfileID: "personal", Confirm: true}); err != nil {
		t.Fatalf("expected personal switch to succeed, got %v", err)
	}
	assertJSONFile(t, authPath, "token-2")
}

func TestForkCodexProfileSharesOrCopiesCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"team","access_token":"parent"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "parent"}); err != nil {
		t.Fatalf("expected parent create to succeed, got %v", err)
	}
	if _, err := ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir:       configDir,
		CodexDir:        codexDir,
		SourceProfileID: "parent",
		ProfileID:       "shared-child",
		AuthBinding:     CodexForkAuthBindingShareParent,
	}); err != nil {
		t.Fatalf("expected shared fork to succeed, got %v", err)
	}
	if _, err := ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir:       configDir,
		CodexDir:        codexDir,
		SourceProfileID: "parent",
		ProfileID:       "copied-child",
		AuthBinding:     CodexForkAuthBindingCopyNew,
	}); err != nil {
		t.Fatalf("expected copied fork to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"team","access_token":"updated-parent"}}`)
	if _, err := SyncCodexProfile(ctx, SyncCodexProfileRequest{
		ConfigDir:  configDir,
		CodexDir:   codexDir,
		ProfileID:  "parent",
		AuthUpdate: CodexSyncAuthUpdateShared,
	}); err != nil {
		t.Fatalf("expected parent shared sync to succeed, got %v", err)
	}

	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"team","access_token":"live"}}`), 0o600); err != nil {
		t.Fatalf("expected live auth mutation to succeed, got %v", err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: codexconfig.ProviderID, ProfileID: "shared-child", Confirm: true}); err != nil {
		t.Fatalf("expected shared child switch to succeed, got %v", err)
	}
	assertJSONFile(t, authPath, "updated-parent")
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: codexconfig.ProviderID, ProfileID: "copied-child", Confirm: true}); err != nil {
		t.Fatalf("expected copied child switch to succeed, got %v", err)
	}
	assertJSONFile(t, authPath, "parent")
}

func TestForkCodexProfileRejectsMissingSourceCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"team","access_token":"parent"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "parent"}); err != nil {
		t.Fatalf("expected parent create to succeed, got %v", err)
	}

	valueJSON, err := codexpreset.CredentialBindingValueJSON("cred_missing")
	if err != nil {
		t.Fatalf("expected missing credential binding setup to encode, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected healthy store to open, got %v", err)
	}
	if _, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:  "parent",
		ProviderID: codexconfig.ProviderID,
		TargetID:   codexconfig.AuthTargetID,
		ValueJSON:  &valueJSON,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected auth target mutation setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	_, err = ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir:       configDir,
		CodexDir:        codexDir,
		SourceProfileID: "parent",
		ProfileID:       "shared-child",
		AuthBinding:     CodexForkAuthBindingShareParent,
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestSyncCodexProfileRequiresChoiceForChangedSharedCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"team","access_token":"token-1"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "parent"}); err != nil {
		t.Fatalf("expected parent create to succeed, got %v", err)
	}
	if _, err := ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir:       configDir,
		CodexDir:        codexDir,
		SourceProfileID: "parent",
		ProfileID:       "shared-child",
		AuthBinding:     CodexForkAuthBindingShareParent,
	}); err != nil {
		t.Fatalf("expected shared fork to succeed, got %v", err)
	}

	writeCodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"team","access_token":"token-2"}}`)
	_, err := SyncCodexProfile(ctx, SyncCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "parent"})
	assertAppErrorCode(t, err, ErrorCodexInvalid)

	result, err := SyncCodexProfile(ctx, SyncCodexProfileRequest{
		ConfigDir:  configDir,
		CodexDir:   codexDir,
		ProfileID:  "parent",
		AuthUpdate: CodexSyncAuthUpdateShared,
	})
	if err != nil {
		t.Fatalf("expected explicit shared sync to succeed, got %v", err)
	}
	if !hasWarning(result.Warnings, codexSharedCredentialWarning) {
		t.Fatalf("expected shared credential warning, got %#v", result.Warnings)
	}
}
