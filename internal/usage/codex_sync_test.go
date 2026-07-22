package usage

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/store"
)

func TestBeginCodexUsageSyncRejectsCursorIdentityWithoutAdvancingGeneration(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	environment := newUsageTestEnvironment(t, configDir, "")
	initialized, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	defer db.Close()
	source, err := beginCodexUsageSync(ctx, db)
	if err != nil {
		t.Fatalf("begin initial sync: %v", err)
	}
	if _, err := db.CommitCodexUsageImport(ctx, store.CommitCodexUsageImportParams{
		Generation: source.SyncGeneration,
		File: store.CodexUsageImportFile{
			SourceID: source.ID, FileKey: usageTestEventKey("identity-file"),
			ParserRevision: CodexUsageParserRevision, IdentityRevision: CodexUsageIdentityRevision,
			EventDigest: usageTestEventKey("identity-digest"),
		},
	}); err != nil {
		t.Fatalf("commit cursor: %v", err)
	}
	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE codex_usage_import_files SET identity_revision = ?`, CodexUsageIdentityRevision-1); err != nil {
		_ = rawDB.Close()
		t.Fatalf("change cursor identity fixture: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	if _, err := beginCodexUsageSync(ctx, db); !errors.Is(err, store.ErrUsageIdentityRevision) {
		t.Fatalf("cursor identity mismatch error = %v", err)
	}
	unchanged, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
	if err != nil || unchanged.SyncGeneration != source.SyncGeneration {
		t.Fatalf("cursor identity mismatch advanced generation: source=%#v err=%v", unchanged, err)
	}
}
