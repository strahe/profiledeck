package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

func TestExecStatementsRollsBackWholeMigrationCallback(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	defer db.Close()

	err = execStatements(ctx, db, []string{
		`CREATE TABLE partial_migration (id TEXT PRIMARY KEY)`,
		`INSERT INTO partial_migration (id) VALUES ('must-rollback')`,
		`INSERT INTO missing_migration_table (id) VALUES ('fail')`,
	})
	if err == nil {
		t.Fatal("migration callback unexpectedly succeeded")
	}
	var count int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'partial_migration'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration left %d partial tables", count)
	}
}

func TestUsageIncrementalCheckpointRollsBackPartialUpgrade(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	defer db.Close()
	if err := upStableBaseline(ctx, db); err != nil {
		t.Fatalf("create stable baseline: %v", err)
	}
	if err := upGrokBuildUsageImport(ctx, db); err != nil {
		t.Fatalf("create Grok Build baseline: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `DROP TABLE grok_build_usage_import_files`); err != nil {
		t.Fatalf("damage migration prerequisite: %v", err)
	}

	if err := upUsageIncrementalCheckpoint(ctx, db); err == nil {
		t.Fatal("incremental checkpoint migration unexpectedly succeeded")
	}
	for table, column := range map[string]string{
		"usage_sources":            "completed_generation",
		"codex_usage_import_files": "checkpoint_revision",
	} {
		exists, err := usageColumnExists(ctx, db, table, column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", table, column, err)
		}
		if exists {
			t.Fatalf("failed migration retained %s.%s", table, column)
		}
	}
	var observations int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM sqlite_master
		WHERE type = 'table' AND name = 'usage_import_observations'
	`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 0 {
		t.Fatal("failed migration retained usage_import_observations")
	}
}

func TestUsageIncrementalCheckpointPreservesCompletedLegacyProgress(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	defer db.Close()
	if err := upStableBaseline(ctx, db); err != nil {
		t.Fatalf("create stable baseline: %v", err)
	}
	if err := upGrokBuildUsageImport(ctx, db); err != nil {
		t.Fatalf("create Grok Build baseline: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO providers (id, name, adapter_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('codex', 'Codex', 'codex', 1, 1)
	`); err != nil {
		t.Fatalf("create legacy Provider: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO usage_sources (
			provider_id, source_key, identity_revision, sync_generation,
			last_completed_at_unix_ms
		) VALUES ('codex', 'codex-session-jsonl', 2, 7, 100)
	`); err != nil {
		t.Fatalf("create legacy source: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO codex_usage_import_files (
			source_id, file_key, parser_revision, identity_revision,
			event_digest, updated_at_unix_ms
		) VALUES (
			1,
			X'0101010101010101010101010101010101010101010101010101010101010101',
			1,
			2,
			X'0202020202020202020202020202020202020202020202020202020202020202',
			100
		)
	`); err != nil {
		t.Fatalf("create legacy cursor: %v", err)
	}

	if err := upUsageIncrementalCheckpoint(ctx, db); err != nil {
		t.Fatalf("migrate incremental checkpoints: %v", err)
	}
	var completedGeneration, checkpointRevision, processedBytes int64
	var parserState string
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT s.completed_generation, f.checkpoint_revision,
			f.processed_bytes, f.parser_state_json
		FROM usage_sources AS s
		JOIN codex_usage_import_files AS f ON f.source_id = s.id
	`).Scan(&completedGeneration, &checkpointRevision, &processedBytes, &parserState); err != nil {
		t.Fatalf("read migrated progress: %v", err)
	}
	if completedGeneration != 7 || checkpointRevision != 0 || processedBytes != 0 || parserState != "{}" {
		t.Fatalf(
			"migrated progress = completed %d checkpoint %d bytes %d state %q",
			completedGeneration,
			checkpointRevision,
			processedBytes,
			parserState,
		)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE usage_sources
		SET sync_generation = 8, completed_generation = 7
	`); err != nil {
		t.Fatalf("create interrupted current generation: %v", err)
	}
	if err := upUsageIncrementalCheckpoint(ctx, db); err != nil {
		t.Fatalf("replay incremental checkpoint migration: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT completed_generation FROM usage_sources
	`).Scan(&completedGeneration); err != nil {
		t.Fatalf("read replayed progress: %v", err)
	}
	if completedGeneration != 7 {
		t.Fatalf("migration replay completed interrupted generation: got %d", completedGeneration)
	}
}
