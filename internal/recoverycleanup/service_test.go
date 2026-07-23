package recoverycleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestInspectAndReconcileCreateMissingRecoveryRoot(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()

	inspection, err := service.Inspect(ctx, db)
	if err != nil || inspection.State != StateRequired {
		t.Fatalf("Inspect() = %#v, %v, want required", inspection, err)
	}
	result, err := service.ReconcileLocked(ctx, db)
	if err != nil || !result.RecoveryCleanupCompleted {
		t.Fatalf("ReconcileLocked() = %#v, %v", result, err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || required {
		t.Fatalf("RecoveryCleanupRequired() = %t, %v", required, err)
	}
	info, err := os.Lstat(paths.Recovery)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("recovery root = %#v, %v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("recovery root mode = %#o, want 0700", info.Mode().Perm())
	}
}

func TestMissingRecoveryRootCreateSurvivesParentSyncFailure(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	service.syncDirectory = func(path string) error {
		if path == filepath.Dir(paths.Recovery) {
			return errors.New("injected parent sync failure")
		}
		return targetfs.SyncDirectory(path)
	}
	if _, err := service.ReconcileLocked(ctx, db); errorCode(err) != apperror.OperationRecoveryCleanupRequired {
		t.Fatalf("ReconcileLocked() error = %v", err)
	}
	if info, err := os.Lstat(paths.Recovery); err != nil || !info.IsDir() {
		t.Fatalf("created recovery root = %#v, %v", info, err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("cleanup requirement = %t, %v, want true", required, err)
	}

	service.syncDirectory = targetfs.SyncDirectory
	result, err := service.ReconcileLocked(ctx, db)
	if err != nil || !result.RecoveryCleanupCompleted {
		t.Fatalf("retry = %#v, %v", result, err)
	}
}

func TestReconcilePreservesUnresolvedSwitchAndRemovesOtherEntryTypes(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "switch-pending", ProviderID: "generic", ProfileIDs: []string{"profile-a"},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(paths.Recovery, "switch-pending")
	staleDir := filepath.Join(paths.Recovery, "switch-applied")
	staleFile := filepath.Join(paths.Recovery, "orphan")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "snapshot"), []byte("kept outside tests"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleFile, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.Recovery, "link")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}

	result, err := service.ReconcileLocked(ctx, db)
	if err != nil || !result.RecoveryCleanupCompleted {
		t.Fatalf("ReconcileLocked() = %#v, %v", result, err)
	}
	if info, err := os.Stat(keep); err != nil || !info.IsDir() {
		t.Fatalf("unresolved recovery point was not preserved: %#v, %v", info, err)
	}
	for _, removed := range []string{staleDir, staleFile, link} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale entry %q remains: %v", filepath.Base(removed), err)
		}
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "outside" {
		t.Fatalf("symlink target changed: %q, %v", raw, err)
	}
	inspection, err := service.Inspect(ctx, db)
	if err != nil || inspection.State != StateClean {
		t.Fatalf("Inspect() after cleanup = %#v, %v", inspection, err)
	}
}

func TestReconcileKeepsDurableRequirementUntilDirectorySyncSucceeds(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	if err := os.Mkdir(paths.Recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(paths.Recovery, "stale")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.syncDirectory = func(path string) error {
		if path == paths.Recovery {
			return errors.New("sync failed")
		}
		return targetfs.SyncDirectory(path)
	}

	if _, err := service.ReconcileLocked(ctx, db); errorCode(err) != apperror.OperationRecoveryCleanupRequired {
		t.Fatalf("ReconcileLocked() error = %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale entry was not removed before sync failure: %v", err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("cleanup requirement = %t, %v, want true", required, err)
	}

	service.syncDirectory = targetfs.SyncDirectory
	result, err := service.ReconcileLocked(ctx, db)
	if err != nil || !result.RecoveryCleanupCompleted {
		t.Fatalf("retry = %#v, %v", result, err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || required {
		t.Fatalf("cleanup requirement after retry = %t, %v", required, err)
	}
}

func TestReconcileContinuesAfterOneEntryFailsAndRetriesIdempotently(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	if err := os.Mkdir(paths.Recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	failed := filepath.Join(paths.Recovery, "stale-a")
	removed := filepath.Join(paths.Recovery, "stale-b")
	for _, path := range []string{failed, removed} {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service.remove = func(path string) error {
		if path == failed {
			return errors.New("injected remove failure")
		}
		return os.Remove(path)
	}

	if _, err := service.ReconcileLocked(ctx, db); errorCode(err) != apperror.OperationRecoveryCleanupRequired {
		t.Fatalf("ReconcileLocked() error = %v", err)
	}
	if _, err := os.Stat(failed); err != nil {
		t.Fatalf("failed entry was not preserved: %v", err)
	}
	if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later stale entry was not removed: %v", err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("cleanup requirement = %t, %v, want true", required, err)
	}

	service.remove = os.Remove
	result, err := service.ReconcileLocked(ctx, db)
	if err != nil || !result.RecoveryCleanupCompleted {
		t.Fatalf("retry = %#v, %v", result, err)
	}
	if _, err := os.Stat(failed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed entry remained after retry: %v", err)
	}
}

func TestReconcileRejectsUnsafeOperationIDBeforeChangingRecoveryEntries(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "../escape", ProviderID: "generic", ProfileIDs: []string{"profile-a"},
		MetadataSchemaVersion: store.OperationMetadataSchemaVersion, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(paths.Recovery, "stale")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	inspection, err := service.Inspect(ctx, db)
	if err != nil || inspection.State != StateUnknown {
		t.Fatalf("Inspect() = %#v, %v, want unknown", inspection, err)
	}
	if _, err := service.ReconcileLocked(ctx, db); errorCode(err) != apperror.OperationRecoveryCleanupRequired {
		t.Fatalf("ReconcileLocked() error = %v", err)
	}
	if raw, err := os.ReadFile(stale); err != nil || string(raw) != "stale" {
		t.Fatalf("unsafe cleanup changed stale entry: %q, %v", raw, err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || required {
		t.Fatalf("invalid operation id unexpectedly changed key: %t, %v", required, err)
	}
}

func TestRecoveryRootSymlinkIsNeverFollowed(t *testing.T) {
	ctx := context.Background()
	service, db, paths := newTestService(t, ctx)
	defer db.Close()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.Recovery); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}

	inspection, err := service.Inspect(ctx, db)
	if err != nil || inspection.State != StateUnknown {
		t.Fatalf("Inspect() = %#v, %v, want unknown", inspection, err)
	}
	if _, err := service.ReconcileLocked(ctx, db); errorCode(err) != apperror.OperationRecoveryCleanupRequired {
		t.Fatalf("ReconcileLocked() error = %v", err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside" {
		t.Fatalf("symlink target changed: %q, %v", raw, err)
	}
}

func newTestService(t *testing.T, ctx context.Context) (*Service, *store.Store, profilesruntime.Paths) {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeService.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	db, err := runtimeService.StoreFactory().Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "generic", Name: "Generic", AdapterID: "generic", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID: "profile-a", Name: "Profile A", MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return NewService(runtimeService.Paths()), db, runtimeService.Paths()
}

func errorCode(err error) apperror.Code {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}
