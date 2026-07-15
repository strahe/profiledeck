package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
)

func TestCodexSwitchSharedConfigWritesOnlyAuthAndCapturesRefresh(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, false)
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	refreshed := `{"tokens":{"account_id":"second","access_token":"second-refreshed"}}`
	if err := os.WriteFile(authPath, []byte(refreshed), 0o600); err != nil {
		t.Fatalf("expected auth refresh setup, got %v", err)
	}

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected switch plan, got %v", err)
	}
	if len(plan.StateCaptures) != 1 || plan.StateCaptures[0].ResourceKind != codexCaptureKindCredential {
		t.Fatalf("expected refreshed auth capture, got %#v", plan.StateCaptures)
	}
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionUpdate)
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionNoop)
	configBefore := readFileString(t, configPath)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first", Confirm: true}); err != nil {
		t.Fatalf("expected switch to first, got %v", err)
	}
	if got := readFileString(t, configPath); got != configBefore {
		t.Fatalf("expected shared config working copy not to be rewritten, got %q", got)
	}
	assertJSONFile(t, authPath, "first-token")
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "second", Confirm: true}); err != nil {
		t.Fatalf("expected switch back to second, got %v", err)
	}
	assertJSONFile(t, authPath, "second-refreshed")
}

func TestCodexSwitchSharedCredentialWritesOnlyConfig(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"first\"\n", `{"tokens":{"account_id":"shared","access_token":"shared-token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("expected first profile, got %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "first", ProfileID: "second",
		CredentialBinding: CodexForkBindingShareParent, ConfigBinding: CodexForkBindingCopyNew, NewConfigSetID: "second-config",
	}); err != nil {
		t.Fatalf("expected fork, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	createTestCodexConfigSet(t, ctx, db, "second-config", "model = \"second\"\n")
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "second"})
	if err != nil {
		t.Fatalf("expected plan, got %v", err)
	}
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionNoop)
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionUpdate)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "second", Confirm: true}); err != nil {
		t.Fatalf("expected switch, got %v", err)
	}
	if got := readFileString(t, filepath.Join(codexDir, codexconfig.ConfigFileName)); got != "model = \"second\"\n" {
		t.Fatalf("expected second config, got %q", got)
	}
	assertJSONFile(t, filepath.Join(codexDir, codexconfig.AuthFileName), "shared-token")
}

func TestCodexSwitchCapturesBothChangedWorkingCopies(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	refreshedConfig := "model = \"second-refreshed\"\n"
	refreshedAuth := `{"tokens":{"account_id":"second","access_token":"second-refreshed"}}`
	writeCodexProfileFixture(t, codexDir, refreshedConfig, refreshedAuth)

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected switch plan, got %v", err)
	}
	if len(plan.StateCaptures) != 2 {
		t.Fatalf("expected both working copies to be captured, got %#v", plan.StateCaptures)
	}
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionUpdate)
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionUpdate)
	refreshedAuth = `{"tokens":{"account_id":"second","access_token":"second-refreshed-again"}}`
	if err := os.WriteFile(authPath, []byte(refreshedAuth), 0o600); err != nil {
		t.Fatalf("expected second auth refresh setup, got %v", err)
	}
	updatedPlan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected updated switch plan, got %v", err)
	}
	if updatedPlan.PlanFingerprint == plan.PlanFingerprint {
		t.Fatalf("expected pending capture hash to change plan fingerprint")
	}
	plan = updatedPlan
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first", ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true}); err != nil {
		t.Fatalf("expected switch to first, got %v", err)
	}
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "second", Confirm: true}); err != nil {
		t.Fatalf("expected switch back to second, got %v", err)
	}
	if got := readFileString(t, configPath); got != refreshedConfig {
		t.Fatalf("expected captured config, got %q", got)
	}
	assertJSONFile(t, authPath, "second-refreshed-again")
}

func TestCodexSwitchAlreadyMatchingTargetDoesNotPolluteOutgoingBindings(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	first, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "first")
	if err != nil {
		t.Fatalf("expected first detail, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	firstConfigSet, err := db.GetProviderConfigSet(ctx, first.Summary.ConfigSetID)
	if err != nil {
		t.Fatalf("expected first config set, got %v", err)
	}
	second, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "second")
	if err != nil {
		t.Fatalf("expected second detail, got %v", err)
	}
	secondCredentialBefore, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	secondConfigBefore, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	_ = db.Close()
	formattedTargetAuth := "{\n  \"tokens\": {\n    \"account_id\": \"first\",\n    \"access_token\": \"first-token\"\n  }\n}\n"
	writeCodexProfileFixture(t, codexDir, firstConfigSet.PayloadText, formattedTargetAuth)

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected plan, got %v", err)
	}
	if len(plan.StateCaptures) != 0 {
		t.Fatalf("expected target-matching files not to be captured into outgoing resources, got %#v", plan.StateCaptures)
	}
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionNoop)
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionNoop)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first", Confirm: true}); err != nil {
		t.Fatalf("expected switch, got %v", err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	defer db.Close()
	secondCredentialAfter, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	secondConfigAfter, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	if secondCredentialAfter.PayloadSHA256 != secondCredentialBefore.PayloadSHA256 || secondConfigAfter.PayloadSHA256 != secondConfigBefore.PayloadSHA256 {
		t.Fatalf("expected outgoing bindings to remain unchanged")
	}
}

func TestCodexSwitchDoesNotCaptureKnownOtherProfileIntoActiveProfile(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: codexconfig.ProviderID, ProfileID: "first", Confirm: true,
	}); err != nil {
		t.Fatalf("expected first Profile to become active, got %v", err)
	}
	first, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "first")
	if err != nil {
		t.Fatalf("expected first detail, got %v", err)
	}
	second, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "second")
	if err != nil {
		t.Fatalf("expected second detail, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	firstCredentialBefore, _ := db.GetProviderCredential(ctx, first.Summary.CredentialID)
	firstConfigBefore, _ := db.GetProviderConfigSet(ctx, first.Summary.ConfigSetID)
	secondCredentialBefore, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	secondConfigBefore, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	_ = db.Close()
	writeCodexProfileFixture(t, codexDir, secondConfigBefore.PayloadText, secondCredentialBefore.PayloadJSON)

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: codexconfig.ProviderID, ProfileID: "first",
	})
	if err != nil {
		t.Fatalf("expected restore plan, got %v", err)
	}
	if len(plan.StateCaptures) != 0 {
		t.Fatalf("expected known other working copies not to be captured, got %#v", plan.StateCaptures)
	}
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionUpdate)
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionUpdate)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: codexconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatalf("expected first Profile restore, got %v", err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	defer db.Close()
	firstCredentialAfter, _ := db.GetProviderCredential(ctx, first.Summary.CredentialID)
	firstConfigAfter, _ := db.GetProviderConfigSet(ctx, first.Summary.ConfigSetID)
	secondCredentialAfter, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	secondConfigAfter, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	if firstCredentialAfter.PayloadSHA256 != firstCredentialBefore.PayloadSHA256 || firstConfigAfter.PayloadSHA256 != firstConfigBefore.PayloadSHA256 ||
		secondCredentialAfter.PayloadSHA256 != secondCredentialBefore.PayloadSHA256 || secondConfigAfter.PayloadSHA256 != secondConfigBefore.PayloadSHA256 {
		t.Fatalf("expected Codex Profile resources to remain distinct")
	}
}

func TestCodexSwitchCanLeaveActiveProfileWithUnsupportedBindings(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	second, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "second")
	if err != nil {
		t.Fatalf("expected second detail, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	credentialBefore, err := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	if err != nil {
		t.Fatalf("expected second credential, got %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "second", ProviderID: codexconfig.ProviderID,
		SlotID: "unsupported", CredentialID: second.Summary.CredentialID,
	}); err != nil {
		t.Fatalf("expected unsupported binding setup, got %v", err)
	}
	_ = db.Close()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"second","access_token":"unknown-refresh"}}`), 0o600); err != nil {
		t.Fatalf("expected refreshed auth setup, got %v", err)
	}

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected switch-away plan despite unsupported active binding, got %v", err)
	}
	if !hasWarning(plan.Warnings, "bindings are unsupported") {
		t.Fatalf("expected unsupported active binding warning, got %#v", plan.Warnings)
	}
	for _, capture := range plan.StateCaptures {
		if capture.ResourceKind == codexCaptureKindCredential {
			t.Fatalf("expected ambiguous active login not to be captured, got %#v", plan.StateCaptures)
		}
	}
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: codexconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatalf("expected switch away from invalid active Profile, got %v", err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	defer db.Close()
	credentialAfter, err := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	if err != nil || credentialAfter.PayloadSHA256 != credentialBefore.PayloadSHA256 {
		t.Fatalf("expected ambiguous active credential to remain unchanged, got %#v err=%v", credentialAfter, err)
	}
}

func TestCodexAuthPayloadsEqualPreservesLargeIntegers(t *testing.T) {
	left := `{"tokens":{"account_id":9007199254740992}}`
	right := `{"tokens":{"account_id":9007199254740993}}`
	if codexAuthPayloadsEqual(left, right) {
		t.Fatal("expected distinct large integers not to compare equal")
	}
	formatted := "{\n  \"tokens\": {\"account_id\": 9007199254740992}\n}\n"
	if !codexAuthPayloadsEqual(left, formatted) {
		t.Fatal("expected formatting-only differences to compare equal")
	}
	if codexAuthPayloadsEqual(left, left+` {}`) {
		t.Fatal("expected multiple JSON values to be rejected")
	}
}

func TestCodexSwitchDoesNotCaptureInvalidOrMissingWorkingCopies(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	second, err := newCodexTestEnvironment(t, configDir, "").codex.GetProfile(ctx, "second")
	if err != nil {
		t.Fatalf("expected second detail, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	credentialBefore, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	configBefore, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	_ = db.Close()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte("[invalid"), 0o600); err != nil {
		t.Fatalf("expected invalid config setup, got %v", err)
	}
	if err := os.Remove(filepath.Join(codexDir, codexconfig.AuthFileName)); err != nil {
		t.Fatalf("expected missing auth setup, got %v", err)
	}

	plan, err := newCodexTestEnvironment(t, configDir, "").switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("expected repair plan, got %v", err)
	}
	if len(plan.StateCaptures) != 0 || !hasWarning(plan.Warnings, "working copy is invalid") || !hasWarning(plan.Warnings, "working copy is missing") {
		t.Fatalf("expected invalid and missing copies to be skipped with warnings, got %#v", plan)
	}
	assertCodexPlanAction(t, plan, codexconfig.TargetID, planActionUpdate)
	assertCodexPlanAction(t, plan, codexconfig.AuthTargetID, planActionCreate)
	if _, err := newCodexTestEnvironment(t, configDir, "").switching.Apply(ctx, switching.ApplySwitchRequest{ProviderID: codexconfig.ProviderID, ProfileID: "first", Confirm: true}); err != nil {
		t.Fatalf("expected switch repair, got %v", err)
	}

	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store reopen, got %v", err)
	}
	defer db.Close()
	credentialAfter, _ := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	configAfter, _ := db.GetProviderConfigSet(ctx, second.Summary.ConfigSetID)
	if credentialAfter.PayloadSHA256 != credentialBefore.PayloadSHA256 || configAfter.PayloadSHA256 != configBefore.PayloadSHA256 {
		t.Fatalf("expected invalid and missing working copies not to update outgoing resources")
	}
}

func setupCodexSwitchProfiles(t *testing.T, independentConfig bool) (context.Context, string, string) {
	t.Helper()
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"first\"\n", `{"tokens":{"account_id":"first","access_token":"first-token"}}`)
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("expected first create, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"second\"\n", `{"tokens":{"account_id":"second","access_token":"second-token"}}`)
	request := CreateCodexProfileRequest{ProfileID: "second"}
	if independentConfig {
		request.NewConfigSetID = "second-config"
	}
	if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, request); err != nil {
		t.Fatalf("expected second create, got %v", err)
	}
	return ctx, configDir, codexDir
}

func assertCodexPlanAction(t *testing.T, plan switching.SwitchPlan, targetID, action string) {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.TargetID == targetID {
			if operation.Action != action {
				t.Fatalf("expected %s action %s, got %#v", targetID, action, operation)
			}
			return
		}
	}
	t.Fatalf("missing plan operation %s", targetID)
}
