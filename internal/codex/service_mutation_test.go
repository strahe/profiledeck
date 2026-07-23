package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCreateCodexProfileCreatesSharedConfigSetAndActivates(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token-1"}}`)
	result, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
	if !result.Summary.Active || result.ConfigSet.ID != codexSharedConfigSetID || result.ConfigSet.Name != codexSharedConfigSetName || result.ConfigSet.ReferenceCount != 1 {
		t.Fatalf("unexpected create result: %#v", result)
	}
	if result.OperationID == "" {
		t.Fatalf("expected maintenance operation id")
	}

	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	defer db.Close()
	configBinding, err := db.GetProfileConfigSetBinding(ctx, "work", codexconfig.ProviderID, codexpreset.ConfigSetSlotUserConfig)
	if err != nil {
		t.Fatalf("expected config binding, got %v", err)
	}
	if configBinding.ConfigSetID != codexSharedConfigSetID {
		t.Fatalf("unexpected config binding: %#v", configBinding)
	}
	if targetCount, err := db.CountProfileTargetReferences(ctx, "work"); err != nil || targetCount != 0 {
		t.Fatalf("expected Codex resource IDs not to be stored in generic targets, count=%d err=%v", targetCount, err)
	}
	publicTargets, err := newCodexTestEnvironment(t, configDir, "").targets.List(ctx, profiletarget.ListProfileTargetsRequest{
		ProfileID: "work", ProviderID: codexconfig.ProviderID, IncludeDisabled: true,
	})
	if err != nil || len(publicTargets) != 0 {
		t.Fatalf("expected typed Codex bindings to stay out of generic targets, got %#v err=%v", publicTargets, err)
	}
	_, err = newCodexTestEnvironment(t, configDir, "").targets.Get(ctx, profiletarget.GetProfileTargetRequest{
		ProfileID: "work", ProviderID: codexconfig.ProviderID, TargetID: codexconfig.AuthTargetID,
	})
	assertErrorCode(t, err, apperror.TargetInvalid)
	metadataJSON := `{}`
	_, err = newCodexTestEnvironment(t, configDir, "").providers.Update(ctx, provider.UpdateRequest{
		ID: codexconfig.ProviderID, MetadataJSON: &metadataJSON,
	})
	assertErrorCode(t, err, apperror.ProviderInvalid)
	adapterID := "generic"
	_, err = newCodexTestEnvironment(t, configDir, "").providers.Update(ctx, provider.UpdateRequest{
		ID: codexconfig.ProviderID, AdapterID: &adapterID,
	})
	assertErrorCode(t, err, apperror.ProviderInvalid)
}

func TestCreateFirstCodexProfileRejectsCustomConfigSet(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	_, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{
		ProfileID: "work", NewConfigSetID: "custom",
	})
	assertErrorCode(t, err, apperror.CodexInvalid)
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	configSets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil || len(configSets) != 0 {
		t.Fatalf("expected rejected first create to leave no Config Sets, got %#v err=%v", configSets, err)
	}
}

func TestListCodexConfigSetsSurvivesInvalidActiveBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	if err := db.DeleteProfileConfigSetBinding(ctx, "work", codexconfig.ProviderID, codexpreset.ConfigSetSlotUserConfig); err != nil {
		_ = db.Close()
		t.Fatalf("expected active binding removal setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	list, err := newCodexTestEnvironment(t, configDir, "").codex.ListConfigSets(ctx)
	if err != nil {
		t.Fatalf("expected Config Set listing to survive an invalid active binding, got %v", err)
	}
	if len(list.ConfigSets) != 1 || list.ConfigSets[0].Active {
		t.Fatalf("expected Config Sets without an active marker, got %#v", list.ConfigSets)
	}
}

func TestCreateCodexProfileDoesNotExposeInvalidConfigContent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	secret := "config-parser-secret"
	invalidConfig := "api_key = \"" + secret + "\"\n["
	_, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{
		ProfileID: "work", ConfigContent: &invalidConfig,
	})
	assertErrorCode(t, err, apperror.CodexInvalid)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("expected invalid TOML error not to expose config content, got %v", err)
	}
}

func TestCreateCodexProfileReusesCurrentConfigSetWithIndependentCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"same","access_token":"token-1"}}`)
	first, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected first create to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-mini\"\n", `{"tokens":{"account_id":"same","access_token":"token-2"}}`)
	second, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "second"})
	if err != nil {
		t.Fatalf("expected second create to succeed, got %v", err)
	}
	if first.Summary.ConfigSetID != second.Summary.ConfigSetID || second.ConfigSet.ReferenceCount != 2 {
		t.Fatalf("expected profiles to share config set: first=%#v second=%#v", first.Summary, second.Summary)
	}
	if first.Summary.CredentialID == second.Summary.CredentialID {
		t.Fatalf("expected each created profile to own an independent credential")
	}
	firstDetail, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "first")
	if err != nil {
		t.Fatalf("expected first profile detail, got %v", err)
	}
	if firstDetail.Summary.Model != "gpt-5-mini" {
		t.Fatalf("expected current config to be checked into shared set, got %#v", firstDetail.Summary)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	operationProfileIDs, err := db.ListOperationProfileIDs(ctx, second.OperationID)
	if err != nil || strings.Join(operationProfileIDs, ",") != "first,second" {
		t.Fatalf("profile create operation omitted affected Profiles: ids=%v err=%v", operationProfileIDs, err)
	}
}

func TestForkCodexProfileRequiresAtLeastOneCopiedBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "parent"}); err != nil {
		t.Fatalf("expected parent create to succeed, got %v", err)
	}
	_, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "parent", ProfileID: "alias",
		CredentialBinding: CodexForkBindingShareParent, ConfigBinding: CodexForkBindingShareParent,
	})
	assertErrorCode(t, err, apperror.CodexInvalid)
	if _, err := newCodexTestEnvironment(t, configDir, "").profiles.Create(ctx, profile.CreateRequest{
		ID: "child", Name: "Existing Child", Description: "Shared globally",
	}); err != nil {
		t.Fatalf("expected global child Profile, got %v", err)
	}

	child, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "parent", ProfileID: "child",
		CredentialBinding: CodexForkBindingShareParent, ConfigBinding: CodexForkBindingCopyNew,
		NewConfigSetID: "child-config",
	})
	if err != nil {
		t.Fatalf("expected fork with copied config to succeed, got %v", err)
	}
	if child.Summary.CredentialID == "" || child.ConfigSet.ID != "child-config" {
		t.Fatalf("unexpected fork result: %#v", child)
	}
	if child.Profile.Name != "Existing Child" || child.Profile.Description != "Shared globally" {
		t.Fatalf("expected fork to preserve existing global Profile metadata, got %#v", child.Profile)
	}
	parent, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "parent")
	if err != nil {
		t.Fatalf("expected parent detail, got %v", err)
	}
	if child.Summary.CredentialID != parent.Summary.CredentialID {
		t.Fatalf("expected fork to share parent credential")
	}
	loginChild, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "parent", ProfileID: "login-child",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("expected fork with copied credential to succeed, got %v", err)
	}
	if loginChild.Summary.CredentialID == parent.Summary.CredentialID || loginChild.Summary.ConfigSetID != parent.Summary.ConfigSetID {
		t.Fatalf("expected independent credential with shared config, got %#v", loginChild.Summary)
	}
}

func TestUpdateCodexProfileConfigSetRejectsActiveProfile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "parent"}); err != nil {
		t.Fatalf("expected parent create to succeed, got %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, "").codex.CopyConfigSet(ctx, CopyCodexConfigSetRequest{
		SourceConfigSetID: codexSharedConfigSetID, ConfigSetID: "other", Name: "Other",
	}); err != nil {
		t.Fatalf("expected config set copy to succeed, got %v", err)
	}
	_, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateProfileConfigSet(ctx, UpdateCodexProfileConfigSetRequest{ProfileID: "parent", ConfigSetID: "other"})
	assertErrorCode(t, err, apperror.CodexInvalid)
	child, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "parent", ProfileID: "child",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("expected inactive child create, got %v", err)
	}
	updated, err := newCodexTestEnvironment(t, configDir, "").codex.UpdateProfileConfigSet(ctx, UpdateCodexProfileConfigSetRequest{ProfileID: child.Profile.ID, ConfigSetID: "other"})
	if err != nil || updated.Summary.ConfigSetID != "other" {
		t.Fatalf("expected inactive child config rebind, got %#v err=%v", updated, err)
	}
}

func TestSaveActiveCodexProfileStateUpdatesSharedResources(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token-1"}}`)
	created, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work", ProfileID: "shared-login",
		CredentialBinding: CodexForkBindingShareParent, ConfigBinding: CodexForkBindingCopyNew, NewConfigSetID: "shared-login-config",
	}); err != nil {
		t.Fatalf("expected shared-login fork to succeed, got %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work", ProfileID: "shared-config",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	}); err != nil {
		t.Fatalf("expected shared-config fork to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-mini\"\n", `{"tokens":{"account_id":"work","access_token":"token-2"}}`)
	result, err := newCodexTestEnvironment(t, configDir, codexDir).codex.SaveActiveProfileState(ctx)
	if err != nil {
		t.Fatalf("expected save current to succeed, got %v", err)
	}
	if result.ProfileID != "work" || result.ConfigSet.Model != "gpt-5-mini" || result.CredentialID != created.Summary.CredentialID {
		t.Fatalf("unexpected save result: %#v", result)
	}
	if result.CredentialReferenceCount != 2 || result.ConfigSet.ReferenceCount != 2 ||
		!hasWarning(result.Warnings, "shared Codex login") || !hasWarning(result.Warnings, "shared Codex config") {
		t.Fatalf("expected shared resource warnings, got %#v", result)
	}

	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, result.CredentialID)
	if err != nil || credential.PayloadJSON != `{"tokens":{"account_id":"work","access_token":"token-2"}}` {
		t.Fatalf("expected saved credential, got %#v err=%v", credential, err)
	}
	if credential.CredentialKind != codexpreset.CredentialKindAuthJSON || credential.ProviderID != codexconfig.ProviderID {
		t.Fatalf("unexpected credential metadata: %#v", credential)
	}
	operationProfileIDs, err := db.ListOperationProfileIDs(ctx, result.OperationID)
	if err != nil || strings.Join(operationProfileIDs, ",") != "shared-config,shared-login,work" {
		t.Fatalf("shared Codex save operation omitted affected Profiles: ids=%v err=%v", operationProfileIDs, err)
	}
}

func TestSaveActiveCodexProfileStateRejectsDifferentCodexHome(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	activeCodexDir := t.TempDir()
	otherCodexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, activeCodexDir, "model = \"active\"\n", `{"tokens":{"account_id":"active","access_token":"active-token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, activeCodexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "active"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, otherCodexDir, "model = \"other\"\n", `{"tokens":{"account_id":"other","access_token":"other-token"}}`)

	_, err := newCodexTestEnvironment(t, configDir, otherCodexDir).codex.SaveActiveProfileState(ctx)
	assertErrorCode(t, err, apperror.CodexInvalid)
}

func createTestCodexConfigSet(t *testing.T, ctx context.Context, db *store.Store, id, content string) {
	t.Helper()
	if _, err := upsertCodexConfigSet(ctx, db, id, id, "", content); err != nil {
		t.Fatalf("expected test config set create to succeed, got %v", err)
	}
}
