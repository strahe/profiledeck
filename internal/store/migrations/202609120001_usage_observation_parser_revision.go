package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upUsageObservationParserRevision, downUsageObservationParserRevision)
}

func upUsageObservationParserRevision(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return addUsageColumnIfMissing(
			ctx,
			tx,
			"usage_import_observations",
			"parser_revision",
			"INTEGER NOT NULL DEFAULT 0 CHECK (parser_revision >= 0)",
		)
	})
}

func downUsageObservationParserRevision(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return dropUsageColumnIfExists(ctx, tx, "usage_import_observations", "parser_revision")
	})
}
