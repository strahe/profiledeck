package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upPricingCatalog, downPricingCatalog)
}

func upPricingCatalog(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "pricing_catalog_version",
			"INTEGER CHECK (pricing_catalog_version IS NULL OR pricing_catalog_version > 0)"); err != nil {
			return err
		}
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "cache_write_input_tokens",
			"INTEGER CHECK (cache_write_input_tokens IS NULL OR cache_write_input_tokens >= 0)"); err != nil {
			return err
		}
		return addUsageColumnIfMissing(ctx, tx, "usage_facts", "cache_creation_input_tokens",
			"INTEGER CHECK (cache_creation_input_tokens IS NULL OR cache_creation_input_tokens >= 0)")
	})
}

func downPricingCatalog(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "cache_creation_input_tokens"); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "cache_write_input_tokens"); err != nil {
			return err
		}
		return dropUsageColumnIfExists(ctx, tx, "usage_facts", "pricing_catalog_version")
	})
}
