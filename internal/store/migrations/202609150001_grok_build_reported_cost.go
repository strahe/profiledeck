package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upGrokBuildReportedCost, downGrokBuildReportedCost)
}

func upGrokBuildReportedCost(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := addUsageColumnIfMissing(
			ctx,
			tx,
			"usage_facts",
			"reported_cost_usd_ticks",
			"INTEGER CHECK (reported_cost_usd_ticks IS NULL OR reported_cost_usd_ticks > 0)",
		); err != nil {
			return err
		}
		return addUsageColumnIfMissing(
			ctx,
			tx,
			"usage_facts",
			"reported_cost_status",
			`INTEGER NOT NULL DEFAULT 0
				CHECK (reported_cost_status IN (0, 1, 2))
				CHECK (
					(reported_cost_status IN (1, 2) AND reported_cost_usd_ticks IS NOT NULL)
					OR (reported_cost_status = 0 AND reported_cost_usd_ticks IS NULL)
				)`,
		)
	})
}

func downGrokBuildReportedCost(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "reported_cost_status"); err != nil {
			return err
		}
		return dropUsageColumnIfExists(ctx, tx, "usage_facts", "reported_cost_usd_ticks")
	})
}
