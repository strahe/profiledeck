package grokbuild_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/grokbuild"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokcoord "github.com/strahe/profiledeck/internal/grokbuild/coordination"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/providercoord"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
)

func TestManagedProfilesSwitchExactWorkingCopiesWithoutPublicBodies(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	authA := syntheticAuth("login-a", "AUTH_BODY_A")
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authA)

	application := newApplication(t, home)
	first, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "grok-a", Name: stringPointer("Grok A"),
	})
	if err != nil {
		t.Fatalf("create first Profile: %v", err)
	}
	if first.ConfigSet.ID != "shared" {
		t.Fatalf("first Profile Config Set = %q, want shared", first.ConfigSet.ID)
	}
	assertStoredCredential(t, ctx, application, first.Summary.CredentialID, authA)
	assertStoredConfig(t, ctx, application, first.ConfigSet.ID, "")

	authB := syntheticAuth("login-b", "AUTH_BODY_B")
	configB := "# CONFIG_BODY_B\nauth_provider = \"CONFIG_SECRET_VALUE\"\n"
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authB)
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), configB)
	second, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "grok-b", NewConfigSetID: "grok-b-settings",
	})
	if err != nil {
		t.Fatalf("create second Profile: %v", err)
	}
	if !containsWarning(second.Warnings, "override the selected login") {
		t.Fatalf("create warnings do not report the behavior override: %#v", second.Warnings)
	}
	assertStoredCredential(t, ctx, application, second.Summary.CredentialID, authB)
	assertStoredConfig(t, ctx, application, second.ConfigSet.ID, configB)

	planA, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-a",
	})
	if err != nil {
		t.Fatalf("build A plan: %v", err)
	}
	assertPlanContainsNoBody(t, planA)
	if _, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-a", Confirm: true,
		ExpectedPlanFingerprint: planA.PlanFingerprint,
	}); err != nil {
		t.Fatalf("apply A: %v", err)
	}
	assertFileContent(t, filepath.Join(home, grokconfig.AuthFileName), authA)
	assertFileContent(t, filepath.Join(home, grokconfig.ConfigFileName), "")

	planB, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-b",
	})
	if err != nil {
		t.Fatalf("build B plan: %v", err)
	}
	assertPlanContainsNoBody(t, planB)
	if !containsWarning(planB.Warnings, "override the selected login") {
		t.Fatalf("plan warnings do not report the behavior override: %#v", planB.Warnings)
	}
	if _, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-b", Confirm: true,
		ExpectedPlanFingerprint: planB.PlanFingerprint,
	}); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	assertFileContent(t, filepath.Join(home, grokconfig.AuthFileName), authB)
	assertFileContent(t, filepath.Join(home, grokconfig.ConfigFileName), configB)

	if _, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
		SourceProfileID: "grok-b", ProfileID: "grok-b-shared",
		CredentialBinding: grokbuild.ForkBindingShareParent,
		ConfigBinding:     grokbuild.ForkBindingCopyNew,
		NewConfigSetID:    "grok-b-shared-settings",
	}); err != nil {
		t.Fatalf("fork Profile with shared login: %v", err)
	}

	authBCheckedIn := syntheticAuth("login-b", "AUTH_BODY_B_CHECKED_IN")
	configBCheckedIn := "# CONFIG_BODY_B_CHECKED_IN\n"
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authBCheckedIn)
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), configBCheckedIn)
	planBackToA, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-a",
	})
	if err != nil {
		t.Fatalf("build return-to-A plan: %v", err)
	}
	assertPlanContainsNoBody(t, planBackToA)
	appliedA, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-a", Confirm: true,
		ExpectedPlanFingerprint: planBackToA.PlanFingerprint,
	})
	if err != nil {
		t.Fatalf("apply return to A: %v", err)
	}
	assertStoredCredential(t, ctx, application, second.Summary.CredentialID, authBCheckedIn)
	assertStoredConfig(t, ctx, application, second.ConfigSet.ID, configBCheckedIn)
	assertOperationProfiles(
		t,
		ctx,
		application,
		appliedA.OperationID,
		[]string{"grok-a", "grok-b", "grok-b-shared"},
	)
	assertFileContent(t, filepath.Join(home, grokconfig.AuthFileName), authA)
	assertFileContent(t, filepath.Join(home, grokconfig.ConfigFileName), "")

	planBackToB, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-b",
	})
	if err != nil {
		t.Fatalf("build return-to-B plan: %v", err)
	}
	if _, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID: grokconfig.ProviderID, ProfileID: "grok-b", Confirm: true,
		ExpectedPlanFingerprint: planBackToB.PlanFingerprint,
	}); err != nil {
		t.Fatalf("apply return to B: %v", err)
	}
	assertFileContent(t, filepath.Join(home, grokconfig.AuthFileName), authBCheckedIn)
	assertFileContent(t, filepath.Join(home, grokconfig.ConfigFileName), configBCheckedIn)

	authBSaved := syntheticAuth("login-b", "AUTH_BODY_B_SAVED")
	configBSaved := "# CONFIG_BODY_B_SAVED\n"
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authBSaved)
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), configBSaved)
	saved, err := application.GrokBuild().SaveActiveProfileState(ctx)
	if err != nil {
		t.Fatalf("save current Profile: %v", err)
	}
	assertStoredCredential(t, ctx, application, saved.CredentialID, authBSaved)
	assertStoredConfig(t, ctx, application, saved.ConfigSet.ID, configBSaved)
	assertOperationProfiles(t, ctx, application, saved.OperationID, []string{"grok-b", "grok-b-shared"})

	detail, err := application.GrokBuild().GetProfile(ctx, "grok-b")
	if err != nil {
		t.Fatalf("get Profile: %v", err)
	}
	publicJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal public detail: %v", err)
	}
	for _, body := range []string{"AUTH_BODY", "CONFIG_BODY", "CONFIG_SECRET_VALUE"} {
		if strings.Contains(string(publicJSON), body) {
			t.Fatalf("public Profile detail exposed managed file content: %s", publicJSON)
		}
	}
}

func TestSaveActiveProfileStateRejectsChangedActiveProfile(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	originalAuth := syntheticAuth("work", "ORIGINAL_AUTH")
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), originalAuth)
	application := newApplication(t, home)
	created, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("work", "CHANGED_AUTH"))
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), "# changed\n")
	_, err = application.GrokBuild().SaveActiveProfileStateFor(ctx, "other")
	assertErrorCode(t, err, apperror.ProfileChanged)
	assertStoredCredential(t, ctx, application, created.Summary.CredentialID, originalAuth)
	assertStoredConfig(t, ctx, application, created.ConfigSet.ID, "")
}

func TestValidationErrorsDoNotExposeManagedFileBodies(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	application := newApplication(t, home)

	const authMarker = "AUTH_VALIDATION_SECRET"
	writePrivateFile(
		t,
		filepath.Join(home, grokconfig.AuthFileName),
		"{\"login\":{\"key\":\""+authMarker+"\"",
	)
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "invalid-auth",
	}); err == nil {
		t.Fatal("invalid auth.json was accepted")
	} else if strings.Contains(err.Error(), authMarker) {
		t.Fatal("auth.json validation error exposed managed file content")
	}

	const configMarker = "CONFIG_VALIDATION_SECRET"
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("login-a", "marker"))
	writePrivateFile(
		t,
		filepath.Join(home, grokconfig.ConfigFileName),
		"api_key = \""+configMarker+"\"\n[broken",
	)
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "invalid-config",
	}); err == nil {
		t.Fatal("invalid config.toml was accepted")
	} else if strings.Contains(err.Error(), configMarker) {
		t.Fatal("config.toml validation error exposed managed file content")
	}
}

func TestCreateRequiresNonEmptyValidAuthAndAllowsMissingConfig(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	application := newApplication(t, home)
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "missing-auth",
	}); err == nil {
		t.Fatal("missing auth.json was accepted")
	}
	if _, err := os.Stat(filepath.Join(home, grokconfig.AuthFileName)); !os.IsNotExist(err) {
		t.Fatalf("missing auth.json was rewritten: %v", err)
	}
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), "{}")
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "empty-auth-store",
	}); err == nil {
		t.Fatal("empty AuthStore was accepted")
	}
}

func TestFirstProfileReusesPrecreatedSharedConfigSet(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config := "# saved source bytes\n[ui]\nscreen_mode = \"minimal\"\n"
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("login-a", "marker"))
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), config)
	application := newApplication(t, home)

	if _, err := application.GrokBuild().CreateConfigSet(ctx, grokbuild.CreateConfigSetRequest{
		ConfigSetID: "shared", Name: "Existing Shared", Description: "Created before the first Profile",
	}); err != nil {
		t.Fatalf("precreate shared Config Set: %v", err)
	}
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID: "existing", Name: "Existing", MetadataJSON: "{}",
	}); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("create existing Profile: %v; close store: %v", err, closeErr)
		}
		t.Fatalf("create existing Profile: %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "existing", ProviderID: grokconfig.ProviderID,
		SlotID: grokpreset.ConfigSetSlotUserConfig, ConfigSetID: "shared",
	}); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("bind existing Profile: %v; close store: %v", err, closeErr)
		}
		t.Fatalf("bind existing Profile: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := os.Remove(filepath.Join(home, grokconfig.ConfigFileName)); err != nil {
		t.Fatalf("remove working config: %v", err)
	}
	created, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "first",
	})
	if err != nil {
		t.Fatalf("create first Profile: %v", err)
	}
	if created.ConfigSet.ID != "shared" || created.ConfigSet.Name != "Existing Shared" {
		t.Fatalf("reused Config Set = %#v", created.ConfigSet)
	}
	assertStoredConfig(t, ctx, application, "shared", config)
	assertOperationProfiles(t, ctx, application, created.OperationID, []string{"first"})
}

func TestCreateProfileReusesActiveConfigSetWithoutReadingWorkingConfig(t *testing.T) {
	tests := []struct {
		name          string
		savedConfig   string
		workingConfig *string
		wantWarning   bool
	}{
		{
			name:          "warning follows saved config",
			savedConfig:   "[models.default]\napi_key = \"synthetic\"\n",
			workingConfig: stringPointer("[broken"),
			wantWarning:   true,
		},
		{
			name:          "working override does not add warning",
			savedConfig:   "[ui]\nscreen_mode = \"minimal\"\n",
			workingConfig: stringPointer("[models.default]\nenv_key = \"SYNTHETIC\"\n"),
		},
		{
			name:        "missing working config",
			savedConfig: "# preserved\n[ui]\nscreen_mode = \"minimal\"\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			authPath := filepath.Join(home, grokconfig.AuthFileName)
			configPath := filepath.Join(home, grokconfig.ConfigFileName)
			writePrivateFile(t, authPath, syntheticAuth("login-a", "marker-a"))
			writePrivateFile(t, configPath, test.savedConfig)
			application := newApplication(t, home)
			first, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
				ProfileID: "first",
			})
			if err != nil {
				t.Fatalf("create first Profile: %v", err)
			}
			if test.workingConfig == nil {
				if err := os.Remove(configPath); err != nil {
					t.Fatalf("remove working config: %v", err)
				}
			} else {
				writePrivateFile(t, configPath, *test.workingConfig)
			}
			writePrivateFile(t, authPath, syntheticAuth("login-b", "marker-b"))

			created, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
				ProfileID: "second",
			})
			if err != nil {
				t.Fatalf("create second Profile: %v", err)
			}
			if created.ConfigSet.ID != first.ConfigSet.ID ||
				created.ConfigSet.UpdatedAtUnixMS != first.ConfigSet.UpdatedAtUnixMS {
				t.Fatalf("reused Config Set changed: first=%#v second=%#v", first.ConfigSet, created.ConfigSet)
			}
			assertStoredConfig(t, ctx, application, first.ConfigSet.ID, test.savedConfig)
			hasWarning := slices.Contains(created.Warnings, grokconfig.AuthOverrideWarning)
			if hasWarning != test.wantWarning {
				t.Fatalf("override warning = %t, want %t; warnings=%v", hasWarning, test.wantWarning, created.Warnings)
			}
		})
	}
}

func TestForkProfileReusesGlobalProfileWithoutGrokBuildBindings(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("source", "SOURCE_AUTH"))
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), "[ui]\nscreen_mode = \"minimal\"\n")
	application := newApplication(t, home)

	source, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "source", Name: stringPointer("Source"),
	})
	if err != nil {
		t.Fatalf("create source Profile: %v", err)
	}
	if _, err := application.Providers().Create(ctx, provider.CreateRequest{
		ID: "other-agent", Name: "Other Agent", AdapterID: "generic",
	}); err != nil {
		t.Fatalf("create unrelated Provider: %v", err)
	}
	if _, err := application.Profiles().Create(ctx, profile.CreateRequest{
		ID: "existing", Name: "Existing Profile", Description: "Keep this description",
	}); err != nil {
		t.Fatalf("create existing global Profile: %v", err)
	}
	otherTargetPath := filepath.Join(t.TempDir(), "other-agent.json")
	if _, err := application.Targets().Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "existing", ProviderID: "other-agent", TargetID: "settings",
		Path: otherTargetPath, Format: profiletarget.FormatJSON, Strategy: profiletarget.StrategyReplaceFile,
		ValueJSON: `{"content":"other-agent-state"}`,
	}); err != nil {
		t.Fatalf("create unrelated Profile target: %v", err)
	}

	reused, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
		SourceProfileID: "source", ProfileID: "existing",
		CredentialBinding: grokbuild.ForkBindingCopyNew,
		ConfigBinding:     grokbuild.ForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("fork into existing global Profile: %v", err)
	}
	if reused.Profile.Name != "Existing Profile" || reused.Profile.Description != "Keep this description" {
		t.Fatalf("existing Profile metadata changed: %#v", reused.Profile)
	}
	if reused.ConfigSet.ID != source.ConfigSet.ID {
		t.Fatalf("reused Config Set = %q, want %q", reused.ConfigSet.ID, source.ConfigSet.ID)
	}
	if _, err := application.Targets().Get(ctx, profiletarget.GetProfileTargetRequest{
		ProfileID: "existing", ProviderID: "other-agent", TargetID: "settings",
	}); err != nil {
		t.Fatalf("unrelated Profile target was not preserved: %v", err)
	}
	if _, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
		SourceProfileID: "source", ProfileID: "existing",
		CredentialBinding: grokbuild.ForkBindingCopyNew,
		ConfigBinding:     grokbuild.ForkBindingShareParent,
	}); err == nil {
		t.Fatal("fork reused a Profile that already has Grok Build bindings")
	} else {
		assertErrorCode(t, err, apperror.ProfileAlreadyExists)
	}

	if _, err := application.Profiles().Create(ctx, profile.CreateRequest{
		ID: "existing-updated", Name: "Old Name", Description: "Old description",
	}); err != nil {
		t.Fatalf("create global Profile for metadata update: %v", err)
	}
	updatedName := "Updated Name"
	updatedDescription := "Updated description"
	updated, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
		SourceProfileID: "source", ProfileID: "existing-updated",
		CredentialBinding: grokbuild.ForkBindingCopyNew,
		ConfigBinding:     grokbuild.ForkBindingShareParent,
		Name:              &updatedName,
		Description:       &updatedDescription,
	})
	if err != nil {
		t.Fatalf("fork with explicit metadata: %v", err)
	}
	if updated.Profile.Name != updatedName || updated.Profile.Description != updatedDescription {
		t.Fatalf("explicit Profile metadata was not applied: %#v", updated.Profile)
	}

	if _, err := application.Profiles().Create(ctx, profile.CreateRequest{
		ID: "partial", Name: "Partial",
	}); err != nil {
		t.Fatalf("create partial Profile: %v", err)
	}
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store for partial binding: %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: "partial", ProviderID: grokconfig.ProviderID,
		SlotID: grokpreset.ConfigSetSlotUserConfig, ConfigSetID: source.ConfigSet.ID,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create partial Grok Build binding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store after partial binding: %v", err)
	}
	if _, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
		SourceProfileID: "source", ProfileID: "partial",
		CredentialBinding: grokbuild.ForkBindingCopyNew,
		ConfigBinding:     grokbuild.ForkBindingShareParent,
	}); err == nil {
		t.Fatal("fork reused a Profile with a partial Grok Build binding")
	} else {
		assertErrorCode(t, err, apperror.ProfileAlreadyExists)
	}
}

func TestSaveCurrentRejectsMissingConfigWithoutChangingSavedState(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "non-empty shared Config Set", config: "[ui]\nscreen_mode = \"minimal\"\n"},
		{name: "empty Config Set", config: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			authPath := filepath.Join(home, grokconfig.AuthFileName)
			configPath := filepath.Join(home, grokconfig.ConfigFileName)
			originalAuth := syntheticAuth("original", "ORIGINAL_AUTH_SECRET")
			writePrivateFile(t, authPath, originalAuth)
			writePrivateFile(t, configPath, test.config)
			application := newApplication(t, home)

			first, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
				ProfileID: "first",
			})
			if err != nil {
				t.Fatalf("create first Profile: %v", err)
			}
			second, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
				ProfileID: "second",
			})
			if err != nil {
				t.Fatalf("create second Profile: %v", err)
			}
			firstBefore, err := application.GrokBuild().GetProfile(ctx, first.Profile.ID)
			if err != nil {
				t.Fatalf("show first Profile before save: %v", err)
			}
			secondBefore, err := application.GrokBuild().GetProfile(ctx, second.Profile.ID)
			if err != nil {
				t.Fatalf("show second Profile before save: %v", err)
			}
			activeProfileID := grokBuildActiveProfileID(t, ctx, application)
			before, err := application.GrokBuild().GetConfigSet(ctx, first.ConfigSet.ID)
			if err != nil {
				t.Fatalf("get Config Set before save: %v", err)
			}
			if before.ReferenceCount != 2 {
				t.Fatalf("Config Set reference count = %d, want 2", before.ReferenceCount)
			}
			operationsBefore := operationCount(t, ctx, application)

			const changedAuthMarker = "CHANGED_AUTH_SECRET"
			writePrivateFile(t, authPath, syntheticAuth("changed", changedAuthMarker))
			if err := os.Remove(configPath); err != nil {
				t.Fatalf("remove working config: %v", err)
			}
			_, err = application.GrokBuild().SaveActiveProfileState(ctx)
			if err == nil {
				t.Fatal("save-current accepted a missing config.toml")
			}
			assertErrorCode(t, err, apperror.GrokBuildInvalid)
			if strings.Contains(err.Error(), changedAuthMarker) || (test.config != "" && strings.Contains(err.Error(), test.config)) {
				t.Fatalf("save-current error exposed managed content: %v", err)
			}

			assertStoredCredential(t, ctx, application, first.Summary.CredentialID, originalAuth)
			assertStoredCredential(t, ctx, application, second.Summary.CredentialID, originalAuth)
			assertStoredConfig(t, ctx, application, first.ConfigSet.ID, test.config)
			firstAfter, err := application.GrokBuild().GetProfile(ctx, first.Profile.ID)
			if err != nil {
				t.Fatalf("show first Profile after save: %v", err)
			}
			secondAfter, err := application.GrokBuild().GetProfile(ctx, second.Profile.ID)
			if err != nil {
				t.Fatalf("show second Profile after save: %v", err)
			}
			if firstAfter.Summary.CredentialReferenceCount != firstBefore.Summary.CredentialReferenceCount ||
				secondAfter.Summary.CredentialReferenceCount != secondBefore.Summary.CredentialReferenceCount {
				t.Fatal("rejected save changed credential reference counts")
			}
			after, err := application.GrokBuild().GetConfigSet(ctx, first.ConfigSet.ID)
			if err != nil {
				t.Fatalf("get Config Set after rejected save: %v", err)
			}
			if after.ReferenceCount != before.ReferenceCount {
				t.Fatalf("Config Set reference count changed: before=%d after=%d", before.ReferenceCount, after.ReferenceCount)
			}
			if afterActiveProfileID := grokBuildActiveProfileID(t, ctx, application); afterActiveProfileID != activeProfileID {
				t.Fatal("rejected save changed the active Profile")
			}
			db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, true)
			if err != nil {
				t.Fatalf("open store after rejected save: %v", err)
			}
			incomplete, listErr := db.ListIncompleteOperations(ctx)
			closeErr := db.Close()
			if listErr != nil {
				t.Fatalf("list incomplete operations: %v", listErr)
			}
			if closeErr != nil {
				t.Fatalf("close store after rejected save: %v", closeErr)
			}
			if len(incomplete) != 0 {
				t.Fatalf("rejected save left incomplete operations: %#v", incomplete)
			}
			if operationsAfter := operationCount(t, ctx, application); operationsAfter != operationsBefore {
				t.Fatalf("operation count changed: before=%d after=%d", operationsBefore, operationsAfter)
			}
		})
	}
}

func TestConfigSetManagementSurvivesInvalidActiveBinding(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("login-a", "marker"))
	application := newApplication(t, home)
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "active",
	}); err != nil {
		t.Fatalf("create active Profile: %v", err)
	}

	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.DeleteProfileConfigSetBinding(
		ctx,
		"active",
		grokconfig.ProviderID,
		grokpreset.ConfigSetSlotUserConfig,
	); err != nil {
		_ = db.Close()
		t.Fatalf("remove active Config Set binding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	listed, err := application.GrokBuild().ListConfigSets(ctx)
	if err != nil {
		t.Fatalf("list Config Sets with invalid active binding: %v", err)
	}
	if len(listed.ConfigSets) != 1 || listed.ConfigSets[0].Active {
		t.Fatalf("Config Set list = %#v", listed.ConfigSets)
	}
	created, err := application.GrokBuild().CreateConfigSet(ctx, grokbuild.CreateConfigSetRequest{
		ConfigSetID: "repair", Name: "Repair",
	})
	if err != nil {
		t.Fatalf("create Config Set with invalid active binding: %v", err)
	}
	if created.ID != "repair" || created.Active {
		t.Fatalf("created Config Set = %#v", created)
	}

	db, err = application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	invalidMetadata := "{}"
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID: grokconfig.ProviderID, MetadataJSON: &invalidMetadata,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("corrupt Provider metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corrupted store: %v", err)
	}
	if _, err := application.GrokBuild().ListConfigSets(ctx); err == nil {
		t.Fatal("Config Set list hid invalid Provider metadata")
	}
}

func TestApplicationFreezesResolvedGrokHome(t *testing.T) {
	ctx := context.Background()
	initialHome := t.TempDir()
	t.Setenv("GROK_HOME", initialHome)
	application, err := app.New(app.Config{
		ConfigDir: t.TempDir(), CodexDir: t.TempDir(),
		AgentAccess: agent.AccessUnrestricted,
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	t.Cleanup(application.Close)
	if _, err := application.Initialize(ctx); err != nil {
		t.Fatalf("initialize application: %v", err)
	}

	t.Setenv("GROK_HOME", t.TempDir())
	detected, err := application.GrokBuild().Detect(ctx)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if detected.GrokHome != initialHome {
		t.Fatal("Grok Build Home changed after application construction")
	}
}

func TestCustomAuthEnvironmentBlocksAuthMutationsButAllowsConfigCapture(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), syntheticAuth("login-a", "marker"))
	application := newApplication(t, home)
	t.Setenv("GROK_AUTH", "synthetic-command")
	detected, err := application.GrokBuild().Detect(ctx)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if detected.FileAuthSupported {
		t.Fatal("custom authentication environment was reported as supported")
	}
	if _, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
		ProfileID: "blocked",
	}); err == nil {
		t.Fatal("custom authentication environment allowed a file-backed mutation")
	}
	if _, err := application.GrokBuild().CreateConfigSet(ctx, grokbuild.CreateConfigSetRequest{
		ConfigSetID: "settings-only",
		Name:        "Settings only",
	}); err != nil {
		t.Fatalf("custom authentication environment blocked config-only capture: %v", err)
	}
}

func TestCreateAndSaveCurrentReadWorkingCopyAfterGrokGuard(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	authBeforeCreate := syntheticAuth("login-a", "AUTH_BEFORE_CREATE")
	authAfterCreateRefresh := syntheticAuth("login-a", "AUTH_AFTER_CREATE_REFRESH")
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authBeforeCreate)
	application := newApplication(t, home)

	resolvedHome, err := grokconfig.ResolveHome(home)
	if err != nil {
		t.Fatalf("resolve Home: %v", err)
	}
	refreshCoordinator := grokcoord.NewCoordinator(resolvedHome)
	refreshGuard, err := refreshCoordinator.Acquire(ctx, providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("acquire simulated refresh guard: %v", err)
	}
	defer refreshGuard.Release()
	var created grokbuild.ProfileSaveResult
	createDone := make(chan error, 1)
	go func() {
		result, callErr := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
			ProfileID: "guarded",
		})
		created = result
		createDone <- callErr
	}()
	assertStillWaitingForGuard(t, createDone, "create")
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authAfterCreateRefresh)
	refreshGuard.Release()
	if err := awaitGuardedMutation(t, createDone, "create"); err != nil {
		t.Fatalf("create Profile after refresh: %v", err)
	}
	assertStoredCredential(t, ctx, application, created.Summary.CredentialID, authAfterCreateRefresh)

	authAfterSaveRefresh := syntheticAuth("login-a", "AUTH_AFTER_SAVE_REFRESH")
	writePrivateFile(t, filepath.Join(home, grokconfig.ConfigFileName), "")
	refreshGuard, err = refreshCoordinator.Acquire(ctx, providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("reacquire simulated refresh guard: %v", err)
	}
	defer refreshGuard.Release()
	var saved grokbuild.ProfileStateSaveResult
	saveDone := make(chan error, 1)
	go func() {
		result, callErr := application.GrokBuild().SaveActiveProfileState(ctx)
		saved = result
		saveDone <- callErr
	}()
	assertStillWaitingForGuard(t, saveDone, "save-current")
	writePrivateFile(t, filepath.Join(home, grokconfig.AuthFileName), authAfterSaveRefresh)
	refreshGuard.Release()
	if err := awaitGuardedMutation(t, saveDone, "save-current"); err != nil {
		t.Fatalf("save current Profile after refresh: %v", err)
	}
	assertStoredCredential(t, ctx, application, saved.CredentialID, authAfterSaveRefresh)
}

func newApplication(t *testing.T, home string) *app.Application {
	t.Helper()
	application, err := app.New(app.Config{
		ConfigDir: t.TempDir(), CodexDir: t.TempDir(), GrokHome: home,
		AgentAccess: agent.AccessUnrestricted,
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	t.Cleanup(application.Close)
	if _, err := application.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize application: %v", err)
	}
	return application
}

func syntheticAuth(scope, marker string) string {
	return "{\n  \"" + scope + "\": {\n" +
		"    \"key\": \"" + marker + "\",\n" +
		"    \"auth_mode\": \"web_login\",\n" +
		"    \"create_time\": \"2026-01-01T00:00:00Z\",\n" +
		"    \"user_id\": \"synthetic-user\",\n" +
		"    \"email\": null,\n" +
		"    \"unknown_field\": {\"preserved\": true}\n" +
		"  }\n}\n"
}

func writePrivateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write synthetic fixture: %v", err)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(raw) != expected {
		t.Fatalf("target content changed: got %q want %q", string(raw), expected)
	}
}

func assertStoredCredential(t *testing.T, ctx context.Context, application *app.Application, id, expected string) {
	t.Helper()
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	value, err := db.GetProviderCredential(ctx, id)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if value.PayloadJSON != expected {
		t.Fatalf("stored auth bytes changed")
	}
}

func assertStoredConfig(t *testing.T, ctx context.Context, application *app.Application, id, expected string) {
	t.Helper()
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	value, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, id)
	if err != nil {
		t.Fatalf("get Config Set: %v", err)
	}
	if value.PayloadText != expected {
		t.Fatalf("stored config bytes changed")
	}
}

func assertErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func grokBuildActiveProfileID(t *testing.T, ctx context.Context, application *app.Application) string {
	t.Helper()
	states, err := application.Providers().ListActiveStates(ctx)
	if err != nil {
		t.Fatalf("list active states: %v", err)
	}
	for _, state := range states {
		if state.ProviderID == grokconfig.ProviderID {
			return state.ProfileID
		}
	}
	t.Fatal("Grok Build active Profile was not found")
	return ""
}

func operationCount(t *testing.T, ctx context.Context, application *app.Application) int {
	t.Helper()
	db, err := sql.Open("sqlite", application.Runtime().StoreFactory().DatabasePath())
	if err != nil {
		t.Fatalf("open database to count operations: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM operations").Scan(&count); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	return count
}

func assertOperationProfiles(
	t *testing.T,
	ctx context.Context,
	application *app.Application,
	operationID string,
	expected []string,
) {
	t.Helper()
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	actual, err := db.ListOperationProfileIDs(ctx, operationID)
	if err != nil {
		t.Fatalf("list operation Profiles: %v", err)
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("operation Profiles = %v, want %v", actual, expected)
	}
}

func assertPlanContainsNoBody(t *testing.T, plan switching.SwitchPlan) {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	for _, body := range []string{"AUTH_BODY", "CONFIG_BODY", "CONFIG_SECRET_VALUE"} {
		if strings.Contains(string(raw), body) {
			t.Fatalf("public plan exposed managed file content: %s", raw)
		}
	}
	for _, operation := range plan.Operations {
		if operation.BeforePreview.Content != "" ||
			operation.DesiredPreview.Content != "" ||
			operation.AfterPreview.Content != "" ||
			operation.BeforeSHA256 != "" ||
			operation.DesiredSHA256 != "" {
			t.Fatalf("sensitive operation exposed preview or hash: %#v", operation)
		}
	}
	for _, capture := range plan.StateCaptures {
		if capture.StoredSHA256 != "" || capture.CurrentSHA256 != "" {
			t.Fatalf("sensitive state capture exposed a hash: %#v", capture)
		}
	}
}

func containsWarning(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }

func assertStillWaitingForGuard(t *testing.T, done <-chan error, operation string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s completed before the Grok guard was released: %v", operation, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func awaitGuardedMutation(t *testing.T, done <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not complete after the Grok guard was released", operation)
		return nil
	}
}
