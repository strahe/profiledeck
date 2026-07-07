package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upProviderAccountSecrets, downProviderAccountSecrets)
}

func upProviderAccountSecrets(ctx context.Context, db *bun.DB) error {
	// v1 supports only Codex file-auth payloads; new secret kinds must be added
	// through an explicit migration so validation and output redaction stay aligned.
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS provider_account_secrets (
			provider_id TEXT NOT NULL,
			account_id TEXT NOT NULL,
			secret_kind TEXT NOT NULL CHECK (secret_kind IN ('codex-auth-json')),
			payload_json TEXT NOT NULL,
			payload_sha256 TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (provider_id, account_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_account_secrets_secret_kind ON provider_account_secrets(secret_kind)`,
	})
}

func downProviderAccountSecrets(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP INDEX IF EXISTS idx_provider_account_secrets_secret_kind`,
		`DROP TABLE IF EXISTS provider_account_secrets`,
	})
}
