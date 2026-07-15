package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceDatabasePublishesValidatedCandidate(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
	createSwapTestDatabase(t, databasePath, `"old"`)
	createSwapTestDatabase(t, RestoreCandidatePath(databasePath), `"new"`)

	if err := ReplaceDatabase(ctx, databasePath); err != nil {
		t.Fatalf("replace database: %v", err)
	}
	assertSwapTestValue(t, databasePath, `"new"`)
	assertSwapArtifactsAbsent(t, databasePath)
}

func TestReplaceDatabaseRestoresOriginalWhenCandidateValidationFails(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
	createSwapTestDatabase(t, databasePath, `"old"`)
	if err := os.WriteFile(RestoreCandidatePath(databasePath), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceDatabase(ctx, databasePath); err == nil {
		t.Fatal("expected invalid candidate replacement to fail")
	}
	assertSwapTestValue(t, databasePath, `"old"`)
	assertSwapArtifactsAbsent(t, databasePath)
}

func TestReplaceDatabaseCanRestoreWhenCurrentDatabaseIsMissing(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
	createSwapTestDatabase(t, RestoreCandidatePath(databasePath), `"restored"`)

	if err := ReplaceDatabase(ctx, databasePath); err != nil {
		t.Fatalf("replace missing database: %v", err)
	}
	assertSwapTestValue(t, databasePath, `"restored"`)
	assertSwapArtifactsAbsent(t, databasePath)
}

func TestReconcileDatabaseSwapInterruptionPhases(t *testing.T) {
	ctx := context.Background()

	t.Run("prepared keeps original", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		createSwapTestDatabase(t, databasePath, `"old"`)
		createSwapTestDatabase(t, RestoreCandidatePath(databasePath), `"new"`)
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapPrepared, true); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile prepared exchange: %v", err)
		}
		assertSwapTestValue(t, databasePath, `"old"`)
		assertSwapArtifactsAbsent(t, databasePath)
	})

	t.Run("original moved rolls back", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		oldPath := databasePath + ".restore-old"
		createSwapTestDatabase(t, databasePath, `"old"`)
		createSwapTestDatabase(t, RestoreCandidatePath(databasePath), `"new"`)
		if err := renameDatabaseSet(databasePath, oldPath); err != nil {
			t.Fatal(err)
		}
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapOriginalMoved, true); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile original-moved exchange: %v", err)
		}
		assertSwapTestValue(t, databasePath, `"old"`)
		assertSwapArtifactsAbsent(t, databasePath)
	})

	t.Run("candidate installed finishes commit", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		oldPath := databasePath + ".restore-old"
		candidatePath := RestoreCandidatePath(databasePath)
		createSwapTestDatabase(t, databasePath, `"old"`)
		createSwapTestDatabase(t, candidatePath, `"new"`)
		if err := renameDatabaseSet(databasePath, oldPath); err != nil {
			t.Fatal(err)
		}
		if err := renameDatabaseSet(candidatePath, databasePath); err != nil {
			t.Fatal(err)
		}
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapCandidateInstalled, true); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile candidate-installed exchange: %v", err)
		}
		assertSwapTestValue(t, databasePath, `"new"`)
		assertSwapArtifactsAbsent(t, databasePath)
	})

	t.Run("invalid installed candidate rolls back", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		oldPath := databasePath + ".restore-old"
		createSwapTestDatabase(t, databasePath, `"old"`)
		if err := renameDatabaseSet(databasePath, oldPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(databasePath, []byte("damaged candidate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapCandidateInstalled, true); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile invalid installed candidate: %v", err)
		}
		assertSwapTestValue(t, databasePath, `"old"`)
		assertSwapArtifactsAbsent(t, databasePath)
	})
}

func TestReconcileDatabaseSwapWithoutOriginalDatabase(t *testing.T) {
	ctx := context.Background()

	t.Run("original-moved phase removes partially installed candidate", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		createSwapTestDatabase(t, databasePath, `"candidate"`)
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapOriginalMoved, false); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile partial candidate: %v", err)
		}
		if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial candidate remained after rollback: %v", err)
		}
		assertSwapArtifactsAbsent(t, databasePath)
	})

	t.Run("candidate-installed phase finishes valid restore", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		createSwapTestDatabase(t, databasePath, `"candidate"`)
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapCandidateInstalled, false); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile installed candidate: %v", err)
		}
		assertSwapTestValue(t, databasePath, `"candidate"`)
		assertSwapArtifactsAbsent(t, databasePath)
	})

	t.Run("candidate-installed phase removes invalid restore", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
		if err := os.WriteFile(databasePath, []byte("damaged candidate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeDatabaseSwapState(databasePath+".restore-state", databaseSwapCandidateInstalled, false); err != nil {
			t.Fatal(err)
		}

		if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
			t.Fatalf("reconcile invalid candidate: %v", err)
		}
		if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid candidate remained after rollback: %v", err)
		}
		assertSwapArtifactsAbsent(t, databasePath)
	})
}

func TestReconcileDatabaseSwapRejectsDamagedMarkerWithoutChangingDatabase(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "profiledeck.db")
	createSwapTestDatabase(t, databasePath, `"old"`)
	createSwapTestDatabase(t, RestoreCandidatePath(databasePath), `"new"`)
	if err := os.WriteFile(databasePath+".restore-state", []byte(`{"format_version":1,"phase":"unknown","original_exists":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReconcileDatabaseSwap(ctx, databasePath); err == nil {
		t.Fatal("expected invalid restore marker to be rejected")
	}
	assertSwapTestValue(t, databasePath, `"old"`)
	if _, err := os.Stat(RestoreCandidatePath(databasePath)); err != nil {
		t.Fatalf("invalid marker unexpectedly changed candidate: %v", err)
	}
}

func createSwapTestDatabase(t *testing.T, path, value string) {
	t.Helper()
	db, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.UpsertSetting(context.Background(), UpsertSettingParams{
		Key: "swap.value", ValueJSON: value,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Checkpoint(context.Background()); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertSwapTestValue(t *testing.T, path, want string) {
	t.Helper()
	db, err := NewFactory(path).OpenHealthy(context.Background(), true)
	if err != nil {
		t.Fatalf("open database %q: %v", path, err)
	}
	defer db.Close()
	setting, err := db.GetSetting(context.Background(), "swap.value")
	if err != nil || setting.ValueJSON != want {
		t.Fatalf("database value = %#v, err = %v; want %q", setting, err, want)
	}
}

func assertSwapArtifactsAbsent(t *testing.T, databasePath string) {
	t.Helper()
	for _, path := range []string{
		databasePath + ".restore-state",
		databasePath + ".restore-old",
		RestoreCandidatePath(databasePath),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("restore artifact %q still exists: %v", path, err)
		}
	}
}
