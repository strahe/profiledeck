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
