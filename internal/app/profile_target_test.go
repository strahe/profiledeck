package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

func TestProfileTargetAppCRUDValidationAndRedaction(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       filepath.Join(t.TempDir(), "target.txt"),
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"ok"}`,
	})
	assertAppErrorCode(t, err, ErrorProfileNotFound)

	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
		Name:      "Profile A",
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
	_, err = CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       filepath.Join(t.TempDir(), "target.txt"),
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"ok"}`,
	})
	assertAppErrorCode(t, err, ErrorProviderNotFound)

	if _, err := DeleteProfile(ctx, DeleteProfileRequest{ConfigDir: configDir, ID: "profile-a", Confirm: true}); err != nil {
		t.Fatalf("expected setup profile delete to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	for _, tc := range []CreateProfileTargetRequest{
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-relative-path",
			Path:       "target.txt",
			Format:     "text",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":"ok"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-combo",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "text",
			Strategy:   "json-merge",
			ValueJSON:  `{"model":"x"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-env-ref",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "json",
			Strategy:   "json-merge",
			ValueJSON:  `{"api_key":{"ref_type":"env","name":"OPENAI_API_KEY"}}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-env-key",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "env",
			Strategy:   "env-merge",
			ValueJSON:  `{"1BAD":"value"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-env-value",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "env",
			Strategy:   "env-merge",
			ValueJSON:  "{\"OPENAI_API_KEY\":\"bad\\nvalue\"}",
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-toml-null",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "toml",
			Strategy:   "toml-merge",
			ValueJSON:  `{"model":null}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-json",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "text",
			Strategy:   "replace-file",
			ValueJSON:  `[]`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "bad-content",
			Path:       filepath.Join(t.TempDir(), "target.txt"),
			Format:     "text",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":123}`,
		},
		{
			ConfigDir:    configDir,
			ProfileID:    "profile-a",
			ProviderID:   "provider-a",
			TargetID:     "bad-metadata",
			Path:         filepath.Join(t.TempDir(), "target.txt"),
			Format:       "text",
			Strategy:     "replace-file",
			ValueJSON:    `{"content":"ok"}`,
			MetadataJSON: stringPtr(`{"api_key":"raw-key"}`),
		},
	} {
		_, err := CreateProfileTarget(ctx, tc)
		assertAppErrorCode(t, err, ErrorTargetInvalid)
	}

	rawMergeEnabled := false
	rawMergeTarget, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-raw-merge",
		Path:       filepath.Join(t.TempDir(), "target-raw-merge.json"),
		Format:     "json",
		Strategy:   "json-merge",
		ValueJSON:  `{"api_key":"raw-key","model":"x"}`,
		Enabled:    &rawMergeEnabled,
	})
	if err != nil {
		t.Fatalf("expected raw credential-looking target desired content to be accepted, got %v", err)
	}
	if strings.Contains(rawMergeTarget.ValuePreview.Content, "raw-key") || !strings.Contains(rawMergeTarget.ValuePreview.Content, redactedValue) {
		t.Fatalf("expected raw merge target preview to redact sensitive key, got %#v", rawMergeTarget.ValuePreview)
	}

	target, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:    configDir,
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		TargetID:     "target-a",
		Path:         filepath.Join(t.TempDir(), "target.txt"),
		Format:       "text",
		Strategy:     "replace-file",
		ValueJSON:    `{"content":"OPENAI_API_KEY=raw-key\nsafe=value"}`,
		MetadataJSON: stringPtr(`{"purpose":"test"}`),
	})
	if err != nil {
		t.Fatalf("expected profile target create to succeed, got %v", err)
	}
	if strings.Contains(target.ValuePreview.Content, "raw-key") || !strings.Contains(target.ValuePreview.Content, redactedValue) {
		t.Fatalf("expected target value preview to redact raw key, got %#v", target.ValuePreview)
	}

	largeEnabled := false
	largeContent := "OPENAI_API_KEY=raw-large-key\n" + strings.Repeat("safe=value\n", maxPreviewBytes)
	largeTarget, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-large",
		Path:       filepath.Join(t.TempDir(), "target-large.txt"),
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, largeContent),
		Enabled:    &largeEnabled,
	})
	if err != nil {
		t.Fatalf("expected large profile target create to succeed, got %v", err)
	}
	if !largeTarget.ValuePreview.Truncated {
		t.Fatalf("expected large target preview to be truncated, got %#v", largeTarget.ValuePreview)
	}
	if strings.Contains(largeTarget.ValuePreview.Content, "raw-large-key") || !strings.Contains(largeTarget.ValuePreview.Content, redactedValue) {
		t.Fatalf("expected large target preview to redact raw key, got %#v", largeTarget.ValuePreview)
	}

	targets, err := ListProfileTargets(ctx, ListProfileTargetsRequest{ConfigDir: configDir, ProfileID: "profile-a", ProviderID: "provider-a"})
	if err != nil {
		t.Fatalf("expected target list to succeed, got %v", err)
	}
	if len(targets) != 1 || targets[0].TargetID != "target-a" {
		t.Fatalf("expected default list to include only enabled target-a, got %#v", targets)
	}

	_, err = UpdateProfileTarget(ctx, UpdateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
	})
	assertAppErrorCode(t, err, ErrorTargetInvalid)
	_, err = UpdateProfileTarget(ctx, UpdateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Strategy:   stringPtr("json-merge"),
	})
	assertAppErrorCode(t, err, ErrorTargetInvalid)

	_, err = DeleteProvider(ctx, DeleteProviderRequest{ConfigDir: configDir, ID: "provider-a", Confirm: true})
	assertAppErrorCode(t, err, ErrorProviderInUse)
	_, err = DeleteProfile(ctx, DeleteProfileRequest{ConfigDir: configDir, ID: "profile-a", Confirm: true})
	assertAppErrorCode(t, err, ErrorProfileInUse)
}

func TestProfileTargetPathOwnershipAllowsSharedLogicalTarget(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-b",
		Name:      "Profile B",
	}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	if _, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-b",
		Name:      "Provider B",
		AdapterID: "generic",
	}); err != nil {
		t.Fatalf("expected second provider create to succeed, got %v", err)
	}

	sharedPath := filepath.Join(t.TempDir(), "settings.json")
	for _, profileID := range []string{"profile-a", "profile-b"} {
		if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
			ConfigDir:  configDir,
			ProfileID:  profileID,
			ProviderID: "provider-a",
			TargetID:   "settings",
			Path:       sharedPath,
			Format:     "json",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":"{\"model\":\"x\"}"}`,
		}); err != nil {
			t.Fatalf("expected shared logical target create for %s to succeed, got %v", profileID, err)
		}
	}

	_, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-b",
		ProviderID: "provider-a",
		TargetID:   "other-settings",
		Path:       sharedPath,
		Format:     "json",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"{}"}`,
	})
	assertAppErrorCode(t, err, ErrorTargetAlreadyExists)

	_, err = CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-b",
		ProviderID: "provider-b",
		TargetID:   "settings",
		Path:       sharedPath,
		Format:     "json",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"{}"}`,
	})
	assertAppErrorCode(t, err, ErrorTargetAlreadyExists)

	otherPath := filepath.Join(t.TempDir(), "other-settings.json")
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-b",
		ProviderID: "provider-a",
		TargetID:   "other-settings",
		Path:       otherPath,
		Format:     "json",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"{}"}`,
	}); err != nil {
		t.Fatalf("expected non-conflicting target create to succeed, got %v", err)
	}
	_, err = UpdateProfileTarget(ctx, UpdateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-b",
		ProviderID: "provider-a",
		TargetID:   "other-settings",
		Path:       &sharedPath,
	})
	assertAppErrorCode(t, err, ErrorTargetAlreadyExists)
}

func TestProfileTargetPathNormalizationPreventsOwnershipBypass(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetDir := t.TempDir()
	cleanPath := filepath.Join(targetDir, "settings.json")
	dirtyPath := targetDir + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "settings.json"

	target, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "settings",
		Path:       dirtyPath,
		Format:     "json",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"{}"}`,
	})
	if err != nil {
		t.Fatalf("expected dirty target path create to succeed, got %v", err)
	}
	if target.Path != cleanPath {
		t.Fatalf("expected target path to be normalized to %q, got %q", cleanPath, target.Path)
	}

	_, err = CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "other-settings",
		Path:       cleanPath,
		Format:     "json",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"{}"}`,
	})
	assertAppErrorCode(t, err, ErrorTargetAlreadyExists)
}

func TestTargetPathOwnershipKeyForOS(t *testing.T) {
	path := filepath.Join(string(os.PathSeparator), "tmp", "ProfileDeck", "SETTINGS.JSON")
	lowerPath := strings.ToLower(path)

	for _, tc := range []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: lowerPath},
		{goos: "windows", want: lowerPath},
		{goos: "linux", want: path},
	} {
		if got := targetPathOwnershipKeyForOS(path, tc.goos); got != tc.want {
			t.Fatalf("expected ownership key for %s to be %q, got %q", tc.goos, tc.want, got)
		}
	}
}

func TestMapTargetStoreErrorDistinguishesPathOwnership(t *testing.T) {
	err := mapTargetStoreError(store.ErrPathOwned)
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected path ownership store error to map to AppError, got %T: %v", err, err)
	}
	if appErr.Code != ErrorTargetAlreadyExists || appErr.Message != "target path is already owned by another profile target" {
		t.Fatalf("unexpected path ownership app error: %#v", appErr)
	}
}

func TestBuildPlanUsesBoundedPreviewForLargeTargets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "large.env")
	beforeContent := "OPENAI_API_KEY=old-large-key\n" + strings.Repeat("safe=value\n", maxPreviewBytes) + "tail=value"
	desiredContent := "OPENAI_API_KEY=new-large-key\n" + strings.Repeat("safe=value\n", maxPreviewBytes) + "tail=value"
	if err := os.WriteFile(targetPath, []byte(beforeContent), 0o600); err != nil {
		t.Fatalf("expected large file setup to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-large",
		Path:       targetPath,
		Format:     "env",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, desiredContent),
	}); err != nil {
		t.Fatalf("expected large target create to succeed, got %v", err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("expected large plan to succeed, got %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected one large target operation, got %#v", plan.Operations)
	}
	op := plan.Operations[0]
	if op.Action != planActionUpdate || op.BeforeSHA256 != sha256Hex([]byte(beforeContent)) || op.DesiredSHA256 != sha256Hex([]byte(desiredContent)) {
		t.Fatalf("unexpected large target operation: %#v", op)
	}
	if !op.BeforePreview.Truncated || !op.DesiredPreview.Truncated {
		t.Fatalf("expected large target previews to be truncated, got before=%#v desired=%#v", op.BeforePreview, op.DesiredPreview)
	}
	for _, leaked := range []string{"old-large-key", "new-large-key", "tail=value"} {
		if strings.Contains(op.BeforePreview.Content, leaked) || strings.Contains(op.DesiredPreview.Content, leaked) {
			t.Fatalf("expected bounded redacted previews, leaked %q in %#v", leaked, op)
		}
	}
}

func TestBuildPlanRejectsOversizedMergeTargets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "oversized.env")
	if err := os.WriteFile(targetPath, []byte(strings.Repeat("x", maxTargetContentBytes+1)), 0o600); err != nil {
		t.Fatalf("expected oversized file setup to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-oversized",
		Path:       targetPath,
		Format:     "env",
		Strategy:   "env-merge",
		ValueJSON:  `{"A":"1"}`,
	}); err != nil {
		t.Fatalf("expected oversized target create to succeed, got %v", err)
	}

	_, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorTargetReadFailed)
}

func TestBuildPlanRejectsOversizedReplaceFileTargets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "oversized.env")
	beforeContent := strings.Repeat("x", maxTargetContentBytes+1)
	if err := os.WriteFile(targetPath, []byte(beforeContent), 0o600); err != nil {
		t.Fatalf("expected oversized file setup to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-oversized-replace",
		Path:       targetPath,
		Format:     "env",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"ok"}`,
	}); err != nil {
		t.Fatalf("expected oversized replace target create to succeed, got %v", err)
	}

	_, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorTargetReadFailed)
}

func TestBuildPlanRejectsOversizedDesiredContent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       filepath.Join(t.TempDir(), "oversized-desired.txt"),
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, strings.Repeat("x", maxTargetContentBytes+1)),
	}); err != nil {
		t.Fatalf("expected oversized desired target create to succeed, got %v", err)
	}

	_, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorTargetInvalid)
}

func TestBuildPlanReadOnlyOperationsAndRedaction(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetDir := t.TempDir()
	missingPath := filepath.Join(targetDir, "missing.env")
	samePath := filepath.Join(targetDir, "same.env")
	differentPath := filepath.Join(targetDir, "different.env")
	settingsPath := filepath.Join(targetDir, "settings.json")
	if err := os.WriteFile(samePath, []byte("OPENAI_API_KEY=same\nsafe=value"), 0o600); err != nil {
		t.Fatalf("expected same file setup to succeed, got %v", err)
	}
	if err := os.WriteFile(differentPath, []byte("OPENAI_API_KEY=old\nsafe=value"), 0o600); err != nil {
		t.Fatalf("expected different file setup to succeed, got %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"safe":"value","api_key":"stored-secret"}`), 0o600); err != nil {
		t.Fatalf("expected settings file setup to succeed, got %v", err)
	}

	for _, req := range []CreateProfileTargetRequest{
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-create",
			Path:       missingPath,
			Format:     "env",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":"safe=value OPENAI_API_KEY=new"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-noop",
			Path:       samePath,
			Format:     "env",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":"OPENAI_API_KEY=same\nsafe=value"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-update",
			Path:       differentPath,
			Format:     "env",
			Strategy:   "replace-file",
			ValueJSON:  `{"content":"OPENAI_API_KEY=new\nsafe=value"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-json-update",
			Path:       settingsPath,
			Format:     "json",
			Strategy:   "json-merge",
			ValueJSON:  `{"api_key":"new","model":"x"}`,
		},
		{
			ConfigDir:  configDir,
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-json-create",
			Path:       filepath.Join(targetDir, "settings-2.json"),
			Format:     "json",
			Strategy:   "json-merge",
			ValueJSON:  `{"model":"y"}`,
		},
	} {
		if _, err := CreateProfileTarget(ctx, req); err != nil {
			t.Fatalf("expected target %s create to succeed, got %v", req.TargetID, err)
		}
	}

	beforeTargetCount := countTableRows(t, initResult.DatabasePath, "profile_targets")
	beforeOperationCount := countTableRows(t, initResult.DatabasePath, "operations")
	plan, err := BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
	})
	if err != nil {
		t.Fatalf("expected build plan to succeed, got %v", err)
	}
	if !plan.ReadOnly || len(plan.Operations) != 5 {
		t.Fatalf("unexpected plan summary: %#v", plan)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("expected no top-level warnings for JSON merge plan, got %#v", plan.Warnings)
	}
	if countTableRows(t, initResult.DatabasePath, "profile_targets") != beforeTargetCount {
		t.Fatalf("expected build plan not to mutate profile_targets")
	}
	if countTableRows(t, initResult.DatabasePath, "operations") != beforeOperationCount {
		t.Fatalf("expected build plan not to create operations")
	}
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("expected build plan not to create missing target file, stat error: %v", err)
	}
	if raw, err := os.ReadFile(differentPath); err != nil {
		t.Fatalf("expected different target read to succeed, got %v", err)
	} else if string(raw) != "OPENAI_API_KEY=old\nsafe=value" {
		t.Fatalf("expected build plan not to write target file, got %q", string(raw))
	}

	actions := map[string]string{}
	reasons := map[string]string{}
	for _, op := range plan.Operations {
		actions[op.TargetID] = op.Action
		reasons[op.TargetID] = op.StatusReason
		if strings.Contains(op.BeforePreview.Content, "old") || strings.Contains(op.BeforePreview.Content, "stored-secret") || strings.Contains(op.DesiredPreview.Content, "new") || strings.Contains(op.AfterPreview.Content, "new") {
			t.Fatalf("expected previews to redact raw secret values, got %#v", op)
		}
	}
	if actions["target-create"] != planActionCreate || reasons["target-create"] != planReasonTargetMissing {
		t.Fatalf("unexpected create operation: actions=%#v reasons=%#v", actions, reasons)
	}
	if actions["target-noop"] != planActionNoop || reasons["target-noop"] != planReasonTargetSameContent {
		t.Fatalf("unexpected noop operation: actions=%#v reasons=%#v", actions, reasons)
	}
	if actions["target-update"] != planActionUpdate || reasons["target-update"] != planReasonTargetDifferentContent {
		t.Fatalf("unexpected update operation: actions=%#v reasons=%#v", actions, reasons)
	}
	if actions["target-json-update"] != planActionUpdate || reasons["target-json-update"] != planReasonTargetDifferentContent {
		t.Fatalf("unexpected json update operation: actions=%#v reasons=%#v", actions, reasons)
	}
	if actions["target-json-create"] != planActionCreate || reasons["target-json-create"] != planReasonTargetMissing {
		t.Fatalf("unexpected json create operation: actions=%#v reasons=%#v", actions, reasons)
	}
	for _, op := range plan.Operations {
		if op.TargetID == "target-json-update" {
			if op.DesiredSHA256 == "" || op.AfterPreview.Content == "" {
				t.Fatalf("expected merge to include desired hash and after preview, got %#v", op)
			}
			if op.BeforeSHA256 == "" || op.BeforePreview.Content == "" {
				t.Fatalf("expected merge to include existing target preview, got %#v", op)
			}
		}
	}
}

func TestRedactSensitiveTextHandlesEscapedQuotedValues(t *testing.T) {
	for _, raw := range []string{
		`"api_key": "raw \"quoted\" secret",`,
		`{"model":"x","api_key":"raw-json-secret","nested":{"authorization":"Bearer raw-token"}}`,
		`{"model":"x","api_key":"raw-json-secret`,
		`safe=value OPENAI_API_KEY=raw-env-secret ANTHROPIC_AUTH_TOKEN="raw quoted env secret"`,
	} {
		redacted := redactSensitiveText(raw)

		for _, leaked := range []string{"raw", "quoted", "secret", "Bearer"} {
			if strings.Contains(redacted, leaked) {
				t.Fatalf("expected sensitive text to be redacted for %q, got %q", raw, redacted)
			}
		}
		if !strings.Contains(redacted, redactedValue) {
			t.Fatalf("expected redacted marker for %q, got %q", raw, redacted)
		}
	}
}

func TestBuildPlanErrorsAndSymlinkHandling(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-missing", ProfileID: "profile-missing"})
	assertAppErrorCode(t, err, ErrorProviderNotFound)

	createGenericProviderAndProfile(t, ctx, configDir, false)

	_, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-missing"})
	assertAppErrorCode(t, err, ErrorProfileNotFound)

	_, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorProviderDisabled)

	enabled := true
	if _, err := UpdateProvider(ctx, UpdateProviderRequest{ConfigDir: configDir, ID: "provider-a", Enabled: &enabled}); err != nil {
		t.Fatalf("expected provider enable to succeed, got %v", err)
	}
	if _, err := UpdateProvider(ctx, UpdateProviderRequest{ConfigDir: configDir, ID: "provider-a", AdapterID: stringPtr("unknown")}); err != nil {
		t.Fatalf("expected provider adapter update to succeed, got %v", err)
	}
	_, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorAdapterNotFound)

	if _, err := UpdateProvider(ctx, UpdateProviderRequest{ConfigDir: configDir, ID: "provider-a", AdapterID: stringPtr("generic")}); err != nil {
		t.Fatalf("expected provider adapter restore to succeed, got %v", err)
	}
	targetDir := t.TempDir()
	directoryPath := filepath.Join(targetDir, "directory")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatalf("expected directory setup to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-directory",
		Path:       directoryPath,
		Format:     "json",
		Strategy:   "json-merge",
		ValueJSON:  `{"model":"x"}`,
	}); err != nil {
		t.Fatalf("expected directory target create to succeed, got %v", err)
	}
	_, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	assertAppErrorCode(t, err, ErrorTargetReadFailed)

	if _, err := DeleteProfileTarget(ctx, DeleteProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-directory",
		Confirm:    true,
	}); err != nil {
		t.Fatalf("expected directory target delete to succeed, got %v", err)
	}
	realPath := filepath.Join(targetDir, "real.txt")
	linkPath := filepath.Join(targetDir, "link.txt")
	if err := os.WriteFile(realPath, []byte("OPENAI_API_KEY=raw"), 0o600); err != nil {
		t.Fatalf("expected symlink target setup to succeed, got %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-symlink",
		Path:       linkPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  `{"content":"ok"}`,
	}); err != nil {
		t.Fatalf("expected symlink target create to succeed, got %v", err)
	}
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("expected symlink plan to succeed, got %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected one symlink operation, got %#v", plan.Operations)
	}
	op := plan.Operations[0]
	if op.Action != planActionUnsupported || op.StatusReason != planReasonTargetIsSymlink || !op.IsSymlink {
		t.Fatalf("expected symlink unsupported operation, got %#v", op)
	}
	if strings.Contains(op.BeforePreview.Content, "raw") {
		t.Fatalf("expected symlink target not to be read, got %#v", op)
	}
}

func createGenericProviderAndProfile(t *testing.T, ctx context.Context, configDir string, enabled bool) {
	t.Helper()

	if _, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-a",
		Name:      "Provider A",
		AdapterID: "generic",
		Enabled:   &enabled,
	}); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
		Name:      "Profile A",
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
}

func countTableRows(t *testing.T, databasePath string, table string) int {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	query := ""
	switch table {
	case "profile_targets":
		query = "SELECT COUNT(1) FROM profile_targets"
	case "operations":
		query = "SELECT COUNT(1) FROM operations"
	default:
		t.Fatalf("unexpected table count target %q", table)
	}

	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("expected table count for %s to succeed, got %v", table, err)
	}
	return count
}

func contentValueJSON(t *testing.T, content string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		t.Fatalf("expected content value JSON marshal to succeed, got %v", err)
	}
	return string(raw)
}
