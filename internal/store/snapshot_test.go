package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/store"
)

func TestCreateSnapshotIsConsistentAndPrivateDuringWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	factory := store.NewFactory(filepath.Join(dir, "source.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatalf("open source database: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate source database: %v", err)
	}
	for _, key := range []string{"snapshot.sequence.a", "snapshot.sequence.b"} {
		if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: key, ValueJSON: "0"}); err != nil {
			t.Fatalf("seed source database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}

	writer, err := factory.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	writerStarted := make(chan struct{})
	allowCommit := make(chan struct{})
	writerResult := make(chan error, 1)
	go func() {
		writerResult <- writer.WithTransaction(ctx, func(txStore *store.Store) error {
			if _, err := txStore.UpsertSetting(ctx, store.UpsertSettingParams{
				Key: "snapshot.sequence.a", ValueJSON: "1",
			}); err != nil {
				return err
			}
			close(writerStarted)
			select {
			case <-allowCommit:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := txStore.UpsertSetting(ctx, store.UpsertSettingParams{
				Key: "snapshot.sequence.b", ValueJSON: "1",
			})
			return err
		})
	}()
	select {
	case <-writerStarted:
	case err := <-writerResult:
		t.Fatalf("start concurrent writer: %v", err)
	case <-ctx.Done():
		t.Fatalf("writer did not start: %v", ctx.Err())
	}

	destination := filepath.Join(dir, "snapshots", "snapshot.db")
	snapshotErr := factory.CreateSnapshot(ctx, destination)
	close(allowCommit)
	writerErr := <-writerResult
	if snapshotErr != nil {
		t.Fatalf("create online snapshot: %v", snapshotErr)
	}
	if writerErr != nil {
		t.Fatalf("commit concurrent writer: %v", writerErr)
	}

	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", got)
	}

	snapshot, err := store.NewFactory(destination).OpenHealthy(context.Background(), true)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapshot.Close()
	for _, key := range []string{"snapshot.sequence.a", "snapshot.sequence.b"} {
		setting, err := snapshot.GetSetting(context.Background(), key)
		if err != nil {
			t.Fatalf("read snapshot setting %q: %v", key, err)
		}
		if setting.ValueJSON != "0" {
			t.Fatalf("snapshot included uncommitted value for %q: %q", key, setting.ValueJSON)
		}
	}
}

func TestCreateSnapshotRefusesToReplaceExistingFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	factory := store.NewFactory(filepath.Join(dir, "source.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	destination := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := factory.CreateSnapshot(ctx, destination); err == nil {
		t.Fatal("expected existing destination to be rejected")
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing destination changed: contents=%q err=%v", contents, err)
	}
}
