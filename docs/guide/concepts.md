# Concepts

ProfileDeck separates application state from target tool files.

## Application store

`profiledeck.db` is the SQLite source of truth for ProfileDeck-owned data:

- providers
- profiles
- active state
- profile targets
- switch and rollback operation records
- hidden Codex auth credentials
- Codex Config Sets
- imported usage events and cursors

Target tool files remain owned by their tools. ProfileDeck writes them only through switch and rollback operations.

## Provider

A provider represents an AI tool integration. The implemented adapters are:

- `codex` for Codex profile switching.
- `generic` for manually configured target files.

Providers can be enabled or disabled.

## Profile

A profile is a named desired state. A single profile can contain one or more provider targets. A Codex Profile binds one hidden auth credential and one Config Set; either resource may be shared with other Profiles.

## Target

A target maps a profile to a file path, format, strategy, and desired value. Plans are built from targets, but targets are not written directly. `switch` rebuilds the plan under a lock, verifies file hashes, creates a backup, then writes target files atomically.

## Codex hidden credential

Codex auth credentials are internal lifecycle objects, not user-managed accounts. A hidden credential stores the latest desired `auth.json` payload and may be shared by multiple profiles. Codex `tokens.account_id` is parsed only for display metadata and never used as a ProfileDeck identity or merge key.

## Codex Config Set

A Config Set stores one complete desired `$CODEX_HOME/config.toml` payload. The first Codex Profile creates `shared`; the name is editable and has no special runtime behavior. Config Sets are ProfileDeck application data and can be deleted only when unreferenced.

## Codex working copies

The active Profile's `auth.json` and `config.toml` are working copies. A switch captures valid external changes into the active bindings before materializing different target bindings. Invalid or missing copies are not captured.

## Target files

Target files are external files such as:

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`
- manually configured JSON, TOML, env, or text files

ProfileDeck never writes these files from UI or CRUD commands. Writes happen through `switch`, `rollback`, or `recover`; create, fork, rebind, and save-current operations update ProfileDeck application data only.
