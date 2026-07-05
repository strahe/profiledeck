package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upProfileTargets, downProfileTargets)
}

func upProfileTargets(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS profile_targets (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			path TEXT NOT NULL,
			path_key TEXT NOT NULL,
			format TEXT NOT NULL CHECK (format IN ('text', 'json', 'toml', 'env')),
			strategy TEXT NOT NULL CHECK (strategy IN ('replace-file', 'json-merge', 'toml-merge', 'env-merge')),
			value_json TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, target_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_profile_id ON profile_targets(profile_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_provider_id ON profile_targets(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_enabled ON profile_targets(enabled)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_targets_unique_path ON profile_targets(profile_id, provider_id, path_key)`,
		`CREATE TRIGGER IF NOT EXISTS trg_profile_targets_path_owner_insert
		BEFORE INSERT ON profile_targets
		WHEN EXISTS (
			SELECT 1 FROM profile_targets
			WHERE path_key = NEW.path_key
				AND (provider_id <> NEW.provider_id OR target_id <> NEW.target_id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'profile target path is owned by another provider target');
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_profile_targets_path_owner_update
		BEFORE UPDATE OF path, path_key, provider_id, target_id ON profile_targets
		WHEN EXISTS (
			SELECT 1 FROM profile_targets
			WHERE path_key = NEW.path_key
				AND NOT (
					profile_id = OLD.profile_id
					AND provider_id = OLD.provider_id
					AND target_id = OLD.target_id
				)
				AND (provider_id <> NEW.provider_id OR target_id <> NEW.target_id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'profile target path is owned by another provider target');
		END`,
	})
}

func downProfileTargets(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP TRIGGER IF EXISTS trg_profile_targets_path_owner_update`,
		`DROP TRIGGER IF EXISTS trg_profile_targets_path_owner_insert`,
		`DROP INDEX IF EXISTS idx_profile_targets_unique_path`,
		`DROP INDEX IF EXISTS idx_profile_targets_enabled`,
		`DROP INDEX IF EXISTS idx_profile_targets_provider_id`,
		`DROP INDEX IF EXISTS idx_profile_targets_profile_id`,
		`DROP TABLE IF EXISTS profile_targets`,
	})
}
