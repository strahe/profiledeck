package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upProfileTargetPathLookup, downProfileTargetPathLookup)
}

func upProfileTargetPathLookup(ctx context.Context, db *bun.DB) error {
	_, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_profile_targets_path_key ON profile_targets(path_key)`)
	return err
}

func downProfileTargetPathLookup(ctx context.Context, db *bun.DB) error {
	_, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_profile_targets_path_key`)
	return err
}
