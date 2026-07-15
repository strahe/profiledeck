package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type fakeKeyringClient struct {
	value      string
	exists     bool
	getErr     error
	setErr     error
	setThenErr bool
	getCalls   int
	onGet      func()
}

func (client *fakeKeyringClient) Get(_, _ string) (string, error) {
	client.getCalls++
	if client.onGet != nil {
		client.onGet()
	}
	if client.getErr != nil {
		return "", client.getErr
	}
	if !client.exists {
		return "", keyring.ErrNotFound
	}
	return client.value, nil
}

func (client *fakeKeyringClient) Set(_, _, value string) error {
	if client.setThenErr {
		client.value = value
		client.exists = true
		client.setThenErr = false
		return errors.New("simulated post-write failure")
	}
	if client.setErr != nil {
		return client.setErr
	}
	client.value = value
	client.exists = true
	return nil
}

func (client *fakeKeyringClient) Delete(_, _ string) error {
	if !client.exists {
		return keyring.ErrNotFound
	}
	client.value = ""
	client.exists = false
	return nil
}

func TestAntigravityCreateSwitchAndCapture(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("access-a", "refresh-a"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	first, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if !first.Summary.Active {
		t.Fatalf("expected first profile active")
	}
	_, err = environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "first", ProviderID: agyconfig.ProviderID, TargetID: "auth",
		Path: t.TempDir() + "/auth.json", Format: profiletarget.FormatJSON, Strategy: profiletarget.StrategyReplaceFile,
		ValueJSON: `{"content":"not-a-binding"}`,
	})
	assertErrorCode(t, err, apperror.TargetInvalid)
	client.value = testAgyPayload("access-b", "refresh-b")
	second, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if !second.Summary.Active {
		t.Fatalf("expected second profile active")
	}
	plan, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].BackendID != switchtarget.BackendKeyring || plan.Operations[0].Action != planActionUpdate {
		t.Fatalf("unexpected keyring plan: %#v", plan.Operations)
	}
	operation := plan.Operations[0]
	if operation.Path != "" || operation.Format != "" || operation.Strategy != "" || operation.FileExists || operation.IsSymlink ||
		operation.BeforeSHA256 != "" || operation.DesiredSHA256 != "" || operation.BeforePreview.Content != "" ||
		operation.DesiredPreview.Content != "" || operation.AfterPreview.Content != "" {
		t.Fatalf("keyring plan exposed sensitive target details: %#v", plan.Operations[0])
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	for _, hidden := range []string{"gemini", "access-a", "refresh-a", switchtarget.SHA256String(testAgyPayload("access-a", "refresh-a"))} {
		if strings.Contains(string(planJSON), hidden) {
			t.Fatalf("public plan exposed %q: %s", hidden, planJSON)
		}
	}
	switchResult, err := environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("ApplySwitch: %v", err)
	}
	normalizedA, _, _ := agyauth.Normalize([]byte(testAgyPayload("access-a", "refresh-a")))
	if client.value != normalizedA {
		t.Fatalf("expected first login in keyring")
	}
	if !switchResult.RecoveryCleanupCompleted {
		t.Fatalf("successful switch did not remove its recovery point: %#v", switchResult)
	}
	client.value = testAgyPayload("access-a-refreshed", "refresh-a-refreshed")
	toSecond, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "second"})
	if err != nil {
		t.Fatalf("Build second plan: %v", err)
	}
	if len(toSecond.StateCaptures) != 1 || !toSecond.StateCaptures[0].Changed || toSecond.StateCaptures[0].StoredSHA256 != "" || toSecond.StateCaptures[0].CurrentSHA256 != "" {
		t.Fatalf("expected redacted refreshed-login capture, got %#v", toSecond.StateCaptures)
	}
	secondSwitch, err := environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "second",
		ExpectedPlanFingerprint: toSecond.PlanFingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("Switch second: %v", err)
	}
	if !secondSwitch.RecoveryCleanupCompleted {
		t.Fatalf("successful switch did not remove its recovery point: %#v", secondSwitch)
	}
	normalizedB, _, _ := agyauth.Normalize([]byte(testAgyPayload("access-b", "refresh-b")))
	if client.value != normalizedB {
		t.Fatalf("expected second login in keyring")
	}
}

func TestAntigravityPlanDoesNotCaptureTargetLoginIntoOutgoingProfile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	loginA := testAgyPayload("access-a", "refresh-a")
	loginB := testAgyPayload("access-b", "refresh-b")
	client := &fakeKeyringClient{value: loginA, exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	client.value = loginB
	second, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	secondBefore, err := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	_ = db.Close()
	if err != nil {
		t.Fatalf("read second login: %v", err)
	}

	// The working copy already contains the target login even though the active
	// state still points to the second Profile. Switching must not check the
	// target login into the outgoing second Profile.
	client.value = loginA
	plan, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.StateCaptures) != 0 || len(plan.Operations) != 1 || plan.Operations[0].Action != planActionNoop {
		t.Fatalf("expected target-matching login to switch without capture, got %#v", plan)
	}
	if _, err := environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatalf("ApplySwitch: %v", err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	secondAfter, err := db.GetProviderCredential(ctx, second.Summary.CredentialID)
	if err != nil {
		t.Fatalf("read second login after switch: %v", err)
	}
	if secondAfter.PayloadJSON != secondBefore.PayloadJSON {
		t.Fatalf("expected outgoing Profile login to remain unchanged")
	}
	firstBefore, err := db.GetProviderCredential(ctx, firstCredentialID(t, ctx, db, "first"))
	if err != nil {
		t.Fatalf("read first login: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// The active Profile is now first, but an external switch has placed the
	// already-known second login in Keyring. Re-selecting first must restore its
	// stored login instead of capturing second into first.
	client.value = loginB
	plan, err = environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan after external switch: %v", err)
	}
	if len(plan.StateCaptures) != 0 || len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate {
		t.Fatalf("expected known other login to be restored without capture, got %#v", plan)
	}
	if _, err := environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatalf("ApplySwitch after external switch: %v", err)
	}
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("reopen store after external switch: %v", err)
	}
	defer db.Close()
	firstAfter, err := db.GetProviderCredential(ctx, firstBefore.ID)
	if err != nil {
		t.Fatalf("read first login after external switch: %v", err)
	}
	secondAfter, err = db.GetProviderCredential(ctx, second.Summary.CredentialID)
	if err != nil {
		t.Fatalf("read second login after external switch: %v", err)
	}
	if firstAfter.PayloadJSON != firstBefore.PayloadJSON || secondAfter.PayloadJSON != secondBefore.PayloadJSON {
		t.Fatalf("expected known Profile logins to remain distinct")
	}
}

func TestAntigravityNoopPreservesRawKeyringState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	canonical := testAgyPayload("access", "refresh")
	client := &fakeKeyringClient{value: canonical, exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}

	// agy and other compatible writers may serialize the same payload with
	// different whitespace or field order. A no-op switch must fingerprint the
	// exact value that remains in Keyring so later safety checks use the same state.
	client.value = "{\n  \"auth_method\": \"consumer\",\n  \"token\": {\n    \"expiry\": \"2026-07-12T04:00:00.000000Z\",\n    \"refresh_token\": \"refresh\",\n    \"token_type\": \"Bearer\",\n    \"access_token\": \"access\"\n  }\n}"
	plan, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionNoop {
		t.Fatalf("expected semantic no-op plan, got %#v", plan.Operations)
	}
	result, err := environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "work",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("ApplySwitch: %v", err)
	}
	if !result.RecoveryCleanupCompleted {
		t.Fatalf("successful no-op switch did not clean recovery state: %#v", result)
	}
}

func firstCredentialID(t *testing.T, ctx context.Context, db *store.Store, profileID string) string {
	t.Helper()
	binding, err := db.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
	if err != nil {
		t.Fatalf("read %s login binding: %v", profileID, err)
	}
	return binding.CredentialID
}

func TestAntigravityPlanRejectsUnsupportedConfigBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("access", "refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	payload := "unsupported = true\n"
	if _, err := db.UpsertProviderConfigSet(ctx, store.UpsertProviderConfigSetParams{
		ID: "unsupported", ProviderID: agyconfig.ProviderID, ConfigKind: "unsupported",
		Name: "Unsupported", PayloadText: payload, PayloadSHA256: switchtarget.SHA256String(payload), MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create unsupported Config Set: %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "work", ProviderID: agyconfig.ProviderID, SlotID: "unsupported", ConfigSetID: "unsupported",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create unsupported binding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	_, err = environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	assertErrorCode(t, err, apperror.AntigravityInvalid)
	doctor, err := environment.doctor.Run(ctx)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if !hasDoctorFinding(doctor.Findings, "antigravity_login_binding_invalid") {
		t.Fatalf("expected unsupported binding finding, got %#v", doctor.Findings)
	}
}

func TestAntigravityPlanRejectsCredentialPayloadHashMismatch(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initialized, err := initAntigravityTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("access", "refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	created, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("Create Profile: %v", err)
	}

	// Preserve the stored digest while changing the exact payload bytes to a
	// semantically equivalent serialization. Integrity validation must still fail.
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	reformatted := "{\n  \"auth_method\": \"consumer\",\n  \"token\": {\"access_token\": \"access\", \"token_type\": \"Bearer\", \"refresh_token\": \"refresh\", \"expiry\": \"2026-07-12T04:00:00.000000Z\"}\n}"
	if _, err := rawDB.ExecContext(ctx, `UPDATE provider_credentials SET payload_json = ? WHERE id = ?`, reformatted, created.Summary.CredentialID); err != nil {
		_ = rawDB.Close()
		t.Fatalf("corrupt credential payload: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	_, err = environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	assertErrorCode(t, err, apperror.AntigravityInvalid)
}

func TestAntigravityCreateCanAttachToExistingGlobalProfile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("shared", "shared-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	name := "Shared Profile"
	description := "Used by more than one Agent"
	if _, err := environment.profiles.Create(ctx, profile.CreateRequest{
		ID: "shared", Name: name, Description: description,
	}); err != nil {
		t.Fatalf("Create global Profile: %v", err)
	}
	result, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{
		ProfileID: "shared",
	})
	if err != nil {
		t.Fatalf("Attach Antigravity Profile: %v", err)
	}
	if result.Summary.Profile.Name != name || result.Summary.Profile.Description != description {
		t.Fatalf("expected existing global Profile metadata to remain unchanged, got %#v", result.Summary.Profile)
	}
	_, err = environment.antigravity.UpdateProfile(ctx, UpdateAntigravityProfileRequest{ProfileID: "shared"})
	assertErrorCode(t, err, apperror.ProfileInvalid)
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	bindings, err := db.ListProfileCredentialBindings(ctx, "shared", agyconfig.ProviderID)
	if err != nil || len(bindings) != 1 || bindings[0].SlotID != agyconfig.CredentialSlot {
		t.Fatalf("expected one Antigravity binding, got %#v err=%v", bindings, err)
	}
	_, err = environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{
		ProfileID: "shared",
	})
	assertErrorCode(t, err, apperror.ProfileAlreadyExists)
}

func TestAntigravityCreateAndSaveRequireCurrentConsumerOAuthLogin(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	_, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"})
	assertErrorCode(t, err, apperror.AntigravityInvalid)

	client.value = testAgyPayload("access", "refresh")
	client.exists = true
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}
	client.value = ""
	client.exists = false
	_, err = environment.antigravity.SaveActiveProfile(ctx)
	assertErrorCode(t, err, apperror.AntigravityInvalid)
}

func TestAntigravityCreatePreservesDisabledProvider(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("first", "first-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	disabled := false
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: agyconfig.ProviderID, Enabled: &disabled}); err != nil {
		_ = db.Close()
		t.Fatalf("disable provider: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	client.value = testAgyPayload("second", "second-refresh")
	getCalls := client.getCalls
	_, err = environment.antigravity.Detect(ctx)
	assertErrorCode(t, err, apperror.ProviderDisabled)
	if client.getCalls != getCalls {
		t.Fatalf("disabled Provider detection read the external Keyring")
	}
	_, err = environment.antigravity.GetProfile(ctx, GetAntigravityProfileRequest{ProfileID: "first"})
	assertErrorCode(t, err, apperror.ProviderDisabled)
	_, err = environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"})
	assertErrorCode(t, err, apperror.ProviderDisabled)
	db, err = openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db.Close()
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if err != nil || provider.Enabled {
		t.Fatalf("expected compatible Antigravity provider to remain disabled, provider=%#v err=%v", provider, err)
	}
}

func TestAntigravitySaveCurrentWarnsWhenLoginIsShared(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("first", "first-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	first, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	client.value = testAgyPayload("second", "second-refresh")
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "second", ProviderID: agyconfig.ProviderID,
		SlotID: agyconfig.CredentialSlot, CredentialID: first.Summary.CredentialID,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("share login binding: %v", err)
	}
	_ = db.Close()

	result, err := environment.antigravity.SaveActiveProfile(ctx)
	if err != nil {
		t.Fatalf("Save current: %v", err)
	}
	if result.Summary.CredentialReferenceCount != 2 || !hasWarning(result.Warnings, "shared Antigravity login") {
		t.Fatalf("expected shared login warning, got %#v", result)
	}
}

func TestAntigravityPlanRejectsManagedProviderAdapterChange(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("first", "first-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}
	adapterID := "generic"
	_, err := environment.providers.Update(ctx, provider.UpdateRequest{ID: agyconfig.ProviderID, AdapterID: &adapterID})
	assertErrorCode(t, err, apperror.ProviderInvalid)
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: agyconfig.ProviderID, AdapterID: &adapterID}); err != nil {
		_ = db.Close()
		t.Fatalf("change adapter: %v", err)
	}
	_ = db.Close()
	_, err = environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	assertErrorCode(t, err, apperror.AntigravityInvalid)
	_, err = environment.antigravity.UpdateProfile(ctx, UpdateAntigravityProfileRequest{ProfileID: "first", Name: stringPtr("Updated")})
	assertErrorCode(t, err, apperror.AntigravityInvalid)
	_, err = environment.antigravity.SaveActiveProfile(ctx)
	assertErrorCode(t, err, apperror.AntigravityInvalid)
}

func TestAntigravityFailedWriteCanRecoverKeyring(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("a", "ra"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	client.value = testAgyPayload("b", "rb")
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	plan, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	client.setThenErr = true
	_, err = environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err == nil {
		t.Fatalf("expected simulated switch failure")
	}
	db, openErr := openHealthyStore(ctx, configDir, true)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	incomplete, listErr := db.ListIncompleteOperations(ctx)
	_ = db.Close()
	if listErr != nil || len(incomplete) != 1 {
		t.Fatalf("expected one failed operation, got %#v err=%v", incomplete, listErr)
	}
	if _, err := environment.switching.RecoverOperation(ctx, switching.RecoverOperationParams{OperationID: incomplete[0].ID, Confirm: true}); err != nil {
		t.Fatalf("RecoverOperation: %v", err)
	}
	normalizedB, _, _ := agyauth.Normalize([]byte(testAgyPayload("b", "rb")))
	if client.value != normalizedB {
		t.Fatalf("expected recovery to restore previous keyring value")
	}
}

func TestAntigravityPlanHandlesMissingNoopInvalidAndConcurrentKeyring(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("a", "ra"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}

	noop, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	if err != nil || len(noop.Operations) != 1 || noop.Operations[0].Action != planActionNoop {
		t.Fatalf("expected no-op plan, got %#v err=%v", noop.Operations, err)
	}
	client.exists = false
	client.value = ""
	create, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	if err != nil || len(create.Operations) != 1 || create.Operations[0].Action != planActionCreate {
		t.Fatalf("expected create plan for missing Keyring value, got %#v err=%v", create.Operations, err)
	}
	if create.Operations[0].Path != "" || create.Operations[0].BeforeSHA256 != "" || create.Operations[0].DesiredSHA256 != "" {
		t.Fatalf("expected missing Keyring plan to remain redacted, got %#v", create.Operations[0])
	}

	client.exists = true
	client.value = "{invalid"
	_, err = environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	assertErrorCode(t, err, apperror.AntigravityInvalid)

	client.value = testAgyPayload("other", "other-refresh")
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "other"}); err != nil {
		t.Fatalf("Create second Profile: %v", err)
	}
	concurrent, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "work"})
	if err != nil || concurrent.Operations[0].Action != planActionUpdate {
		t.Fatalf("expected update plan, got %#v err=%v", concurrent.Operations, err)
	}
	client.value = testAgyPayload("changed-after-plan", "changed-refresh")
	_, err = environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "work",
		ExpectedPlanFingerprint: concurrent.PlanFingerprint, Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
}

func TestAntigravityKeyringErrorsDoNotExposeDriverText(t *testing.T) {
	backend := switchtarget.NewKeyringBackend(&fakeKeyringClient{getErr: errors.New("driver leaked credential material")})
	_, err := backend.Inspect(context.Background(), antigravityTargetSpec())
	if err == nil || strings.Contains(err.Error(), "credential material") {
		t.Fatalf("expected redacted Keyring read error, got %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("before", "before-refresh"), exists: true, setErr: errors.New("driver leaked desired secret")}
	backend = switchtarget.NewKeyringBackend(client)
	snapshot, err := backend.Inspect(context.Background(), antigravityTargetSpec())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	err = backend.Apply(context.Background(), antigravityTargetSpec(), snapshot, testAgyPayload("desired", "desired-refresh"), 0, false)
	if err == nil || strings.Contains(err.Error(), "desired secret") {
		t.Fatalf("expected redacted Keyring write error, got %v", err)
	}
}

func TestAntigravityKeyringWriteRejectsChangedSnapshot(t *testing.T) {
	client := &fakeKeyringClient{value: testAgyPayload("before", "before-refresh"), exists: true}
	backend := switchtarget.NewKeyringBackend(client)
	snapshot, err := backend.Inspect(context.Background(), antigravityTargetSpec())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	changed := testAgyPayload("changed", "changed-refresh")
	client.value = changed
	err = backend.Apply(context.Background(), antigravityTargetSpec(), snapshot, testAgyPayload("desired", "desired-refresh"), 0, false)
	assertErrorCode(t, err, apperror.TargetChanged)
	if client.value != changed {
		t.Fatalf("expected concurrent Keyring value to remain unchanged")
	}
}

func TestAntigravityDatabaseCommitFailureCanRecoverKeyring(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("first", "first-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	client.value = testAgyPayload("second", "second-refresh")
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	plan, err := environment.switching.BuildPlan(ctx, switching.BuildPlanRequest{ProviderID: agyconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	faultDB, err := sql.Open("sqlite", environment.runtime.Paths().Database)
	if err != nil {
		t.Fatalf("open fault-injection database: %v", err)
	}
	defer faultDB.Close()
	if _, err := faultDB.ExecContext(ctx, `
		CREATE TRIGGER fail_switch_complete
		BEFORE UPDATE OF status ON operations
		WHEN NEW.status = 'applied' AND OLD.operation_type = 'switch'
		BEGIN
			SELECT RAISE(FAIL, 'simulated database commit failure');
		END
	`); err != nil {
		t.Fatalf("install database commit fault: %v", err)
	}
	_, err = environment.switching.Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: agyconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err == nil {
		t.Fatalf("expected database commit failure")
	}
	normalizedFirst, _, _ := agyauth.Normalize([]byte(testAgyPayload("first", "first-refresh")))
	if client.value != normalizedFirst {
		t.Fatalf("expected external Keyring write before database failure")
	}
	db, openErr := openHealthyStore(ctx, configDir, true)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	incomplete, listErr := db.ListIncompleteOperations(ctx)
	_ = db.Close()
	if listErr != nil || len(incomplete) != 1 {
		t.Fatalf("expected failed switch operation, got %#v err=%v", incomplete, listErr)
	}
	if _, err := environment.switching.RecoverOperation(ctx, switching.RecoverOperationParams{OperationID: incomplete[0].ID, Confirm: true}); err != nil {
		t.Fatalf("RecoverOperation: %v", err)
	}
	normalizedSecond, _, _ := agyauth.Normalize([]byte(testAgyPayload("second", "second-refresh")))
	if client.value != normalizedSecond {
		t.Fatalf("expected recovery to restore pre-switch Keyring value")
	}
}

func TestDoctorReportsAntigravityStateWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayload("doctor-secret", "doctor-refresh"), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("Create Profile: %v", err)
	}
	client.value = `{"token":{"access_token":"doctor-leak","token_type":"Bearer"}}`
	result, err := environment.doctor.Run(ctx)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if !hasDoctorFinding(result.Findings, "antigravity_login_invalid") {
		t.Fatalf("expected invalid Antigravity login finding, got %#v", result.Findings)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal Doctor result: %v", err)
	}
	for _, secret := range []string{"doctor-secret", "doctor-refresh", "doctor-leak", "access_token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("Doctor result exposed %q: %s", secret, raw)
		}
	}
}

func testAgyPayload(access, refresh string) string {
	return `{"token":{"access_token":"` + access + `","token_type":"Bearer","refresh_token":"` + refresh + `","expiry":"2026-07-12T04:00:00.000000Z"},"auth_method":"consumer"}`
}

func hasDoctorFinding(findings []doctor.Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

func hasWarning(warnings []string, substring string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substring) {
			return true
		}
	}
	return false
}

func stringPtr(value string) *string {
	return &value
}
