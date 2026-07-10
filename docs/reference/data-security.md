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

## What ProfileDeck does not store

ProfileDeck usage import stores derived token and cost records. It does not persist raw Codex JSONL events, prompts, completions, or API keys as usage metadata.

Config Set v1 does not capture skills, plugin installation caches, project `.codex/config.toml` files, or system policy.
