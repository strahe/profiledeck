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

`profiledeck.db` stores ProfileDeck application data. For Codex, it also stores raw `auth.json` payloads in a dedicated account secret table so ProfileDeck can restore accounts during switches.

The database is not encrypted at rest. File permissions are tightened on POSIX systems when possible, but local filesystem access remains the security boundary.

## Backups

Switch and rollback backups may contain previous target file content. For Codex, that can include raw `auth.json`.

Backup commands show metadata such as path, action, hash, and mode. They do not print raw auth content in normal output.

## Redaction

ProfileDeck redacts sensitive-looking values in previews and command output. Codex auth previews are always fully redacted.

The following commands are metadata-only and do not print raw auth:

```bash
profiledeck codex account list
profiledeck codex account show <account-id>
profiledeck plan codex <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

## Explicit auth export

This command intentionally writes raw auth JSON:

```bash
profiledeck codex account export <account-id> --output ./auth.json
```

The exported file is a secret. Review, edit, move, and delete it with the same care as Codex's original `auth.json`.

## What ProfileDeck does not store

ProfileDeck usage import stores derived token and cost records. It does not persist raw Codex JSONL events, prompts, completions, or API keys as usage metadata.
