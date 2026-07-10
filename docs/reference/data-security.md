# Data and Security

ProfileDeck manages local files and local secrets. Treat its runtime directory as sensitive.

## Runtime data

The runtime root is:

```text
<os-user-config-dir>/profiledeck
```

It contains:

- `profiledeck.db`
- `backups/`
- `exports/`
- `logs/`
- `locks/`

`--config-dir` changes the base user config directory used to resolve this runtime root.

## SQLite database

`profiledeck.db` stores ProfileDeck application data. For Codex, it stores raw `auth.json` payloads in hidden credential records and complete `config.toml` payloads in Config Sets. These are the long-lived resource states; the files in `$CODEX_HOME` are working copies of the active Profile.

Each Config Set is limited to 16 MiB and validated against its SHA-256 hash. The database payload may contain tokens or other sensitive configuration even when its column is not named as a secret.

The database is not encrypted at rest. File permissions are tightened on POSIX systems when possible, but local filesystem access remains the security boundary.

## Backups

Switch and rollback backups may contain previous target file content. For Codex, that can include raw `auth.json` and `config.toml`.

Backup commands show metadata such as path, action, hash, and mode. They do not print raw auth content in normal output.

## Sensitive Profile exports

`profiledeck codex profile export` is an explicit sensitive export mode for local backup and database rebuilds. Its JSON bundle contains raw Codex `auth.json` and complete Config Set payloads. ProfileDeck requires an explicit output path, writes atomically, refuses symlink targets, and sets the file to `0600` on POSIX systems. It does not create or change the selected parent directory.

Import accepts only a private regular file, validates its format, version, hashes, auth JSON, TOML, references, and conflicts before writing, then applies all database changes in one transaction. It never uses `tokens.account_id` to merge credentials. Import does not set active state or write Codex working files.

Keep sensitive bundles outside the ProfileDeck runtime before deleting a development database. Do not commit or share them.

## Redaction

ProfileDeck redacts sensitive-looking values in previews and command output. Codex auth previews are always fully redacted. Config Set and Profile APIs expose metadata summaries, never raw TOML or auth payloads.

The following commands are metadata-only and do not print raw auth:

```bash
profiledeck codex profile list
profiledeck codex profile show <profile-id>
profiledeck codex config-set list
profiledeck codex config-set show <config-set-id>
profiledeck plan codex <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

Export and import command output remains metadata-only. Only the explicitly selected bundle file contains raw payloads.

## What ProfileDeck does not store

ProfileDeck usage import stores derived token and cost records, validated model labels, safe or derived session identifiers for distinct counts, stable hashed event identities, and hashed path-based cursor keys. A stable event identity is scoped by provider and source, then derived from the safe session identifier, per-session usage order, model, and token counts; it excludes paths, timestamps, prompts, and completions. ProfileDeck does not persist raw Codex JSONL events, prompts, completions, full source paths, or API keys as usage metadata or report output. Import errors expose at most a basename, hashed source key, and sanitized filesystem error.

Usage reports expose aggregates only. Local logs cannot reliably identify the Profile, hidden credential, or ChatGPT account that served a request, so ProfileDeck does not infer or publish account-level usage.

Desktop automatic-sync status contains the configured interval, timestamps, an outcome, aggregate error counts, and a redacted error code/message when needed. It does not emit session paths, cursor keys, or raw importer errors to the UI event stream.

Config Set v1 does not capture skills, plugin installation caches, project `.codex/config.toml` files, or system policy.
