package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

func TestCreateSnapshotIsConsistentAndPrivateDuringWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
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
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "snapshot.sequence", ValueJSON: "0"}); err != nil {
		t.Fatalf("seed source database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}

	writer, err := factory.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	started := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		close(started)
		for sequence := 1; ; sequence++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			value, _ := json.Marshal(sequence)
			_, _ = writer.UpsertSetting(ctx, store.UpsertSettingParams{
				Key: "snapshot.sequence", ValueJSON: string(value),
			})
		}
	}()
	<-started

	destination := filepath.Join(dir, "snapshots", "snapshot.db")
	if err := factory.CreateSnapshot(ctx, destination); err != nil {
		t.Fatalf("create online snapshot: %v", err)
	}
	cancel()
	writerWG.Wait()

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
	setting, err := snapshot.GetSetting(context.Background(), "snapshot.sequence")
	if err != nil {
		t.Fatalf("read snapshot setting: %v", err)
	}
	var sequence int
	if err := json.Unmarshal([]byte(setting.ValueJSON), &sequence); err != nil || sequence < 0 {
		t.Fatalf("snapshot contains an inconsistent value %q: %v", setting.ValueJSON, err)
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
