package switching

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type failSecondProviderPolicy struct {
	calls int
}

func (policy *failSecondProviderPolicy) RequireAgent(context.Context, agent.ID) error {
	return nil
}

func (policy *failSecondProviderPolicy) RequireProvider(context.Context, string) error {
	policy.calls++
	if policy.calls > 1 {
		return apperror.New(apperror.AgentDisabled, "Agent is disabled")
	}
	return nil
}

func TestApplySwitchCreateCleansRecoveryPointAndSetsActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.env")
	rawSecret := "OPENAI_API_KEY=raw-create-secret\n"
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, rawSecret),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}

	plan, err := newSwitchingTestEnvironment(t, configDir).service.BuildPlan(ctx, BuildPlanRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
	})
	if err != nil {
		t.Fatalf("expected plan to succeed, got %v", err)
	}
	if plan.PlanFingerprint == "" {
		t.Fatalf("expected plan fingerprint")
	}

	result, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID:              "provider-a",
		ProfileID:               "profile-a",
		Confirm:                 true,
		ExpectedPlanFingerprint: plan.PlanFingerprint,
	})
	if err != nil {
		t.Fatalf("expected switch apply to succeed, got %v", err)
	}
	if result.Status != store.OperationStatusApplied || result.Counts.Create != 1 || result.Counts.Update != 0 || result.Counts.Noop != 0 {
		t.Fatalf("unexpected switch result: %#v", result)
	}
	assertFileContent(t, targetPath, rawSecret)

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	operation, err := db.GetOperation(ctx, result.OperationID)
	if err != nil {
		t.Fatalf("expected operation read to succeed, got %v", err)
	}
	if operation.Status != store.OperationStatusApplied || operation.ErrorCode != "" {
		t.Fatalf("unexpected operation: %#v", operation)
	}
	if strings.Contains(operation.MetadataJSON, "raw-create-secret") {
		t.Fatalf("expected operation metadata to exclude raw target content, got %s", operation.MetadataJSON)
	}
	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(operation.MetadataJSON), &metadata); err != nil {
		t.Fatalf("expected switch metadata decode to succeed, got %v", err)
	}
	if metadata.PreviousActive == nil || metadata.PreviousActive.Exists {
		t.Fatalf("expected switch metadata to record missing previous active state, got %#v", metadata.PreviousActive)
	}
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != result.OperationID {
		t.Fatalf("unexpected active state: %#v", activeState)
	}

	assertSuccessfulSwitchRecoveryRemoved(t, configDir, result)
}

func TestApplySwitchRechecksAgentPolicyAfterAcquiringLock(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	environment := newSwitchingTestEnvironment(t, configDir)
	targetPath := filepath.Join(t.TempDir(), "settings.env")
	if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "target-a",
		Path: targetPath, Format: "text", Strategy: "replace-file",
		ValueJSON: contentValueJSON(t, "OPENAI_API_KEY=secret\n"),
	}); err != nil {
		t.Fatalf("Create target: %v", err)
	}
	policy := &failSecondProviderPolicy{}
	gated := NewService(environment.runtime.Paths(), environment.runtime.StoreFactory(), policy, environment.service.dependencies)

	_, err := gated.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	assertErrorCode(t, err, apperror.AgentDisabled)
	if policy.calls != 2 {
		t.Fatalf("Provider policy calls = %d, want 2", policy.calls)
	}
	if _, statErr := os.Stat(targetPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target was touched after Agent disable: %v", statErr)
	}
}

func TestMaintenanceRechecksAgentPolicyAfterAcquiringLock(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	environment := newSwitchingTestEnvironment(t, configDir)
	policy := &failSecondProviderPolicy{}
	service := NewService(environment.runtime.Paths(), environment.runtime.StoreFactory(), policy, environment.service.dependencies)
	if err := policy.RequireProvider(ctx, "provider-a"); err != nil {
		t.Fatalf("initial Provider policy: %v", err)
	}
	mutated := false
	err := service.RunMaintenance(ctx, maintenance.Request{
		Operation: "provider-update", ProviderID: "provider-a",
	}, func(context.Context, *store.Store, string) error {
		mutated = true
		return nil
	})
	assertErrorCode(t, err, apperror.AgentDisabled)
	if mutated {
		t.Fatal("maintenance mutation ran after Agent disable")
	}
}

func TestApplySwitchUpdatePreservesPOSIXModeAndCleansRecoveryPoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode preservation is not a Windows ACL guarantee")
	}

	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.env")
	oldContent := "OPENAI_API_KEY=raw-old-secret\n"
	newContent := "OPENAI_API_KEY=raw-new-secret\n"
	if err := os.WriteFile(targetPath, []byte(oldContent), 0o640); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, newContent),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}

	result, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch apply to succeed, got %v", err)
	}
	if result.Counts.Update != 1 {
		t.Fatalf("expected one update, got %#v", result.Counts)
	}
	assertFileContent(t, targetPath, newContent)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(targetPath)
		if err != nil {
			t.Fatalf("expected target stat to succeed, got %v", err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("expected target mode to be preserved, got %#o", info.Mode().Perm())
		}
	}

	assertSuccessfulSwitchRecoveryRemoved(t, configDir, result)
}

func TestApplySwitchNoopRecordsOperationAndActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	content := "already-current\n"
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, content),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}

	result, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected noop switch apply to succeed, got %v", err)
	}
	if result.Counts.Noop != 1 || result.Counts.Create != 0 || result.Counts.Update != 0 {
		t.Fatalf("unexpected noop counts: %#v", result.Counts)
	}
	assertSuccessfulSwitchRecoveryRemoved(t, configDir, result)

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != result.OperationID {
		t.Fatalf("unexpected noop active state: %#v", activeState)
	}
}

func TestApplySwitchRejectsStalePlanFingerprintBeforeWriting(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	plan, err := newSwitchingTestEnvironment(t, configDir).service.BuildPlan(ctx, BuildPlanRequest{ProviderID: "provider-a", ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("expected plan to succeed, got %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("external\n"), 0o600); err != nil {
		t.Fatalf("expected external write to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID:              "provider-a",
		ProfileID:               "profile-a",
		Confirm:                 true,
		ExpectedPlanFingerprint: plan.PlanFingerprint,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
	assertFileContent(t, targetPath, "external\n")
}

func TestApplySwitchRejectsUnsupportedSymlinkBeforeBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is platform-specific")
	}

	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.txt")
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(realPath, []byte("raw\n"), 0o600); err != nil {
		t.Fatalf("expected real target setup to succeed, got %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("expected symlink setup to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       linkPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	assertErrorCode(t, err, apperror.SwitchPlanUnsupported)
	assertFileContent(t, realPath, "raw\n")
	if recoveryCount := countRecoveryDirs(t, configDir); recoveryCount != 0 {
		t.Fatalf("expected no recovery directory for unsupported switch, got %d", recoveryCount)
	}
	if failed := countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusFailed); failed != 1 {
		t.Fatalf("expected one failed operation, got %d", failed)
	}
}

func TestApplySwitchFailsPartialMultiTargetWriteWithoutActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	for _, target := range []struct {
		id      string
		path    string
		content string
	}{
		{id: "target-a", path: firstPath, content: "first\n"},
		{id: "target-b", path: secondPath, content: "second\n"},
	} {
		if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   target.id,
			Path:       target.path,
			Format:     "text",
			Strategy:   "replace-file",
			ValueJSON:  contentValueJSON(t, target.content),
		}); err != nil {
			t.Fatalf("expected target %s create to succeed, got %v", target.id, err)
		}
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	assertFileContent(t, firstPath, "first\n")
	if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected second target not to exist, got %v", err)
	}
	if failed := countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusFailed); failed != 1 {
		t.Fatalf("expected one failed operation, got %d", failed)
	}

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected no active state after partial failed write, got %v", err)
	}
}

func TestApplySwitchFailsWhenLockExistsAndKeepsLockFile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "external-lock")
	if err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	defer lock.Release()
	lockContent := readFileString(t, paths.Lock)

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	assertErrorCode(t, err, apperror.LockAcquireFailed)
	if got := readFileString(t, paths.Lock); got != lockContent {
		t.Fatalf("expected existing lock to remain unchanged, got %q", got)
	}
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected target not to be written, got %v", err)
	}
}

func TestSwitchHashGuardDetectsChangedTarget(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "after\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	defer db.Close()
	environment := newSwitchingTestEnvironment(t, configDir)
	plan, err := environment.service.buildApplyPlan(ctx, db, "provider-a", "profile-a")
	if err != nil {
		t.Fatalf("expected internal plan to succeed, got %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("expected target change to succeed, got %v", err)
	}
	assertErrorCode(t, environment.service.verifySwitchPlanHashes(ctx, plan.Operations), apperror.TargetChanged)
}

func TestFailSwitchOperationUsesCleanupContextAfterCancellation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	db, err := store.Open(ctx, initResult.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected writable store open to succeed, got %v", err)
	}
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-canceled",
		ProfileID:    "profile-a",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected pending operation setup to succeed, got %v", err)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	originalErr := apperror.New(apperror.TargetChanged, "target changed")
	err = failSwitchOperation(canceledCtx, db, "switch-canceled", `{}`, originalErr)
	assertErrorCode(t, err, apperror.TargetChanged)

	operation, err := db.GetOperation(ctx, "switch-canceled")
	if err != nil {
		t.Fatalf("expected operation read to succeed, got %v", err)
	}
	if operation.Status != store.OperationStatusFailed || operation.ErrorCode != string(apperror.TargetChanged) {
		t.Fatalf("expected canceled cleanup to mark operation failed, got %#v", operation)
	}
}

func TestFailSwitchOperationPreservesOriginalErrorWhenFailureMarkFails(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	db, err := store.Open(ctx, initResult.DatabasePath, false)
	if err != nil {
		t.Fatalf("expected writable store open to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	originalErr := apperror.New(apperror.TargetChanged, "target changed")
	err = failSwitchOperation(ctx, db, "switch-closed", `{}`, originalErr)
	assertErrorCode(t, err, apperror.TargetChanged)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T: %v", err, err)
	}
	if appErr.Details["operation_update_error"] == "" {
		t.Fatalf("expected cleanup failure detail to be preserved, got %#v", appErr.Details)
	}
}

func TestPublicStateCapturesRedactsSensitiveNonFileHashes(t *testing.T) {
	private := []StateCapture{{
		ResourceKind: "credential", ResourceID: "credential-a",
		StoredSHA256: "stored-secret-hash", CurrentSHA256: "current-secret-hash",
	}}
	public := publicStateCaptures(private, []applyPlanOperation{{PlanOperation: PlanOperation{
		BackendID: switchtarget.BackendKeyring,
		sensitive: true,
	}}})
	if public[0].StoredSHA256 != "" || public[0].CurrentSHA256 != "" {
		t.Fatalf("expected public secret-derived hashes to be redacted, got %#v", public[0])
	}
	if private[0].StoredSHA256 == "" || private[0].CurrentSHA256 == "" {
		t.Fatalf("expected internal capture hashes to remain available, got %#v", private[0])
	}
}

func openAppTestStore(t *testing.T, ctx context.Context, databasePath string) *store.Store {
	t.Helper()

	db, err := store.Open(ctx, databasePath, true)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	return db
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()

	if got := readFileString(t, path); got != expected {
		t.Fatalf("expected file %s content %q, got %q", path, expected, got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file %s read to succeed, got %v", path, err)
	}
	return string(raw)
}

func countOperationsByStatus(t *testing.T, databasePath, status string) int {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(1) FROM operations WHERE status = ?", status).Scan(&count); err != nil {
		t.Fatalf("expected operation count query to succeed, got %v", err)
	}
	return count
}

func countRecoveryDirs(t *testing.T, configDir string) int {
	t.Helper()

	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	entries, err := os.ReadDir(paths.Recovery)
	if err != nil {
		t.Fatalf("expected recovery directory read to succeed, got %v", err)
	}
	return len(entries)
}

func assertSuccessfulSwitchRecoveryRemoved(t *testing.T, configDir string, result ApplySwitchResult) {
	t.Helper()
	if !result.RecoveryCleanupCompleted {
		t.Fatalf("successful switch did not clean its recovery point: %#v", result)
	}
	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Recovery, result.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful switch recovery point still exists: %v", err)
	}
}
