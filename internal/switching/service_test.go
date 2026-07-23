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
	"sync"
	"testing"
	"time"

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

type blockingApplyFileBackend struct {
	switchtarget.FileBackend
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (backend *blockingApplyFileBackend) Apply(
	ctx context.Context,
	spec switchtarget.Spec,
	snapshot switchtarget.Snapshot,
	desired string,
	mode os.FileMode,
	useMode bool,
) error {
	backend.once.Do(func() {
		close(backend.started)
		<-backend.release
	})
	return backend.FileBackend.Apply(ctx, spec, snapshot, desired, mode, useMode)
}

func TestRunWithSharedLockQueuesConcurrentWork(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	service := newSwitchingTestEnvironment(t, configDir).service

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)
	go func() {
		firstDone <- service.RunWithSharedLock(ctx, "first", func(context.Context) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first shared-lock operation")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- service.RunWithSharedLock(ctx, "second", func(context.Context) error {
			close(secondStarted)
			return nil
		})
	}()

	select {
	case <-secondStarted:
		t.Fatal("second shared-lock operation started before the first completed")
	case err := <-secondDone:
		t.Fatalf("second shared-lock operation returned while queued: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("expected first shared-lock operation to succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first shared-lock operation to finish")
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued shared-lock operation")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("expected queued shared-lock operation to succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued shared-lock operation to finish")
	}
}

func TestRunWithSharedLockHonorsCancellationWhileQueued(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	service := newSwitchingTestEnvironment(t, configDir).service

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)
	go func() {
		firstDone <- service.RunWithSharedLock(ctx, "first", func(context.Context) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first shared-lock operation")
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	called := false
	err := service.RunWithSharedLock(canceledCtx, "canceled", func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled queued operation, got %v", err)
	}
	if called {
		t.Fatal("expected canceled queued operation not to run")
	}

	release()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("expected first shared-lock operation to succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first shared-lock operation to finish")
	}
}

func TestApplyAllowsOnlyOneConcurrentSwitch(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatal(err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	backend := &blockingApplyFileBackend{started: make(chan struct{}), release: make(chan struct{})}
	environment := newSwitchingTestEnvironmentWithTargets(t, configDir, switchtarget.MustRegistry(backend))
	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "target-a",
		Path: targetPath, Format: "text", Strategy: "replace-file", ValueJSON: contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatal(err)
	}

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(backend.release) }) }
	t.Cleanup(release)
	type applyOutcome struct {
		result ApplySwitchResult
		err    error
	}
	firstDone := make(chan applyOutcome, 1)
	go func() {
		result, err := environment.service.Apply(ctx, ApplySwitchRequest{
			ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
		})
		firstDone <- applyOutcome{result: result, err: err}
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first switch target write")
	}

	_, err = environment.service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.OperationRecoveryRequired)
	release()
	select {
	case outcome := <-firstDone:
		if outcome.err != nil || outcome.result.Status != store.OperationStatusApplied {
			t.Fatalf("first concurrent switch result=%#v error=%v", outcome.result, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first concurrent switch")
	}
	assertFileContent(t, targetPath, "managed\n")
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if incomplete, err := db.ListIncompleteOperations(ctx); err != nil || len(incomplete) != 0 {
		t.Fatalf("concurrent switches left incomplete operations: %#v, %v", incomplete, err)
	}
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

func TestApplySwitchBlocksBeforeTargetWriteWhenRecoveryCleanupIsUnsafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is platform-specific")
	}
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatal(err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	environment := newSwitchingTestEnvironment(t, configDir)
	targetPath := filepath.Join(t.TempDir(), "settings.env")
	if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "target-a",
		Path: targetPath, Format: "text", Strategy: "replace-file",
		ValueJSON: contentValueJSON(t, "OPENAI_API_KEY=managed\n"),
	}); err != nil {
		t.Fatal(err)
	}
	paths := environment.runtime.Paths()
	if err := os.Remove(paths.Recovery); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.Recovery); err != nil {
		t.Fatal(err)
	}

	_, err = environment.service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.OperationRecoveryCleanupRequired)
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target changed while cleanup was unsafe: %v", err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside" {
		t.Fatalf("recovery symlink target changed: %q, %v", raw, err)
	}
	if count := countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusPending) +
		countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusFailed); count != 0 {
		t.Fatalf("cleanup preflight created %d incomplete operations", count)
	}
}

func TestApplySwitchRejectsUnknownSystemStateBeforeCreatingOperation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatal(err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	environment := newSwitchingTestEnvironment(t, configDir)
	targetPath := filepath.Join(t.TempDir(), "settings.env")
	if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "target-a",
		Path: targetPath, Format: "text", Strategy: "replace-file",
		ValueJSON: contentValueJSON(t, "OPENAI_API_KEY=managed\n"),
	}); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := rawDB.ExecContext(ctx, `
		INSERT INTO system_state (key, value_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('future.safety_state', 'true', 1, 1)
	`)
	closeErr := rawDB.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatal(err)
	}

	_, err = environment.service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.StoreSchemaInvalid)
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target changed with unknown system state: %v", err)
	}
	if count := countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusPending) +
		countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusFailed); count != 0 {
		t.Fatalf("unknown system state created %d incomplete operations", count)
	}
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
	incompleteBefore, err := db.ListIncompleteOperations(ctx)
	if err != nil || len(incompleteBefore) != 1 {
		t.Fatalf("incomplete operations before retry = %#v, %v", incompleteBefore, err)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.OperationRecoveryRequired)
	incompleteAfter, err := db.ListIncompleteOperations(ctx)
	if err != nil || len(incompleteAfter) != 1 || incompleteAfter[0].ID != incompleteBefore[0].ID {
		t.Fatalf("blocked retry changed recovery source: before=%#v after=%#v err=%v", incompleteBefore, incompleteAfter, err)
	}
	assertFileContent(t, firstPath, "first\n")
}

func TestApplySwitchKeepsFailedOperationRecoverableWhenCleanupRegistrationFails(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatal(err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	environment := newSwitchingTestEnvironment(t, configDir)
	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: "profile-a", ProviderID: "provider-a", TargetID: "target-a",
		Path: targetPath, Format: "text", Strategy: "replace-file", ValueJSON: contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, triggerErr := rawDB.ExecContext(ctx, `
		CREATE TRIGGER fail_cleanup_registration
		BEFORE INSERT ON system_state
		BEGIN
			SELECT RAISE(ABORT, 'injected cleanup registration failure');
		END
	`)
	closeErr := rawDB.Close()
	if err := errors.Join(triggerErr, closeErr); err != nil {
		t.Fatal(err)
	}

	_, err = environment.service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.OperationUpdateFailed)
	assertFileContent(t, targetPath, "managed\n")
	failedSwitchID := singleOperationIDByTypeStatus(
		t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed,
	)
	paths := environment.runtime.Paths()
	if info, err := os.Stat(filepath.Join(paths.Recovery, failedSwitchID)); err != nil || !info.IsDir() {
		t.Fatalf("recovery point after registration failure = %#v, %v", info, err)
	}
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active state committed after cleanup registration failure: %v", err)
	}
	operation := mustOperation(t, ctx, db, failedSwitchID)
	if operation.Status != store.OperationStatusFailed || !strings.Contains(operation.MetadataJSON, `"checkpoint":"recovery_created"`) {
		t.Fatalf("failed switch is not recoverable: %#v", operation)
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
	if len(appErr.Details) != 0 || strings.Contains(err.Error(), "database") {
		t.Fatalf("expected cleanup failure diagnostics to remain private, got %#v: %v", appErr.Details, err)
	}
	if !errors.Is(err, originalErr) {
		t.Fatal("expected original failure to remain on the error chain")
	}
}

func TestErrorCodeAndMessageNormalizesUnknownFailure(t *testing.T) {
	private := errors.New("write /private/SECRET_TARGET: permission denied")
	code, message := errorCodeAndMessage(private)
	if code != apperror.CommandFailed || strings.Contains(message, "SECRET_TARGET") {
		t.Fatalf("unexpected persisted failure: %s %q", code, message)
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

func TestPublicSensitiveFileOperationRedactsHashes(t *testing.T) {
	private := PlanOperation{
		BackendID: targetBackendFile, Path: "/safe/display/path", BeforeSHA256: "before-secret-hash",
		DesiredSHA256: "desired-secret-hash", BeforePreview: TextPreview{Content: "[REDACTED]"}, sensitive: true,
	}
	public := publicPlanOperation(private)
	if public.BeforeSHA256 != "" || public.DesiredSHA256 != "" {
		t.Fatalf("public sensitive file operation exposed hashes: %#v", public)
	}
	if public.Path != private.Path || public.BeforePreview.Content != private.BeforePreview.Content {
		t.Fatalf("public sensitive file operation lost safe presentation fields: %#v", public)
	}
	captures := publicStateCaptures([]StateCapture{{StoredSHA256: "stored", CurrentSHA256: "current"}}, []applyPlanOperation{{PlanOperation: private}})
	if captures[0].StoredSHA256 != "" || captures[0].CurrentSHA256 != "" {
		t.Fatalf("public sensitive file capture exposed hashes: %#v", captures[0])
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
