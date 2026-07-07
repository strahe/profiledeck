# Concepts

ProfileDeck separates application state from target tool files.

## Application store

`profiledeck.db` is the SQLite source of truth for ProfileDeck-owned data:

- providers
- profiles
- active state
- profile targets
- switch and rollback operation records
- Codex account secrets
- imported usage events and cursors

Target tool files remain owned by their tools. ProfileDeck writes them only through switch and rollback operations.

## Provider

A provider represents an AI tool integration. The implemented adapters are:

- `codex` for Codex preset profiles.
- `generic` for manually configured target files.

Providers can be enabled or disabled.

## Profile

A profile is a named desired state. A single profile can contain one or more provider targets. For Codex, common profile ids are local account aliases such as `work` or `team-zhu`.

## Target

A target maps a profile to a file path, format, strategy, and desired value. Plans are built from targets, but targets are not written directly. `switch` rebuilds the plan under a lock, verifies file hashes, creates a backup, then writes target files atomically.

## Codex account alias

ProfileDeck account ids are local aliases for stored Codex `auth.json` payloads. Codex `tokens.account_id` is stored as metadata only because it is not guaranteed to uniquely identify every local login.

## Target files

Target files are external files such as:

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`
- manually configured JSON, TOML, env, or text files

ProfileDeck never writes these files from UI or CRUD commands. Writes happen through `switch`, `rollback`, or `recover`.
