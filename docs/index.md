# ProfileDeck

ProfileDeck safely switches local AI coding tool profiles. The current implementation is Codex-first: it can capture Codex user configuration and file-based authentication, switch between stored profiles, import local token usage, and recover from interrupted switch operations.

## Current capabilities

- Capture a Codex profile from `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`.
- Switch Codex `config.toml` and `auth.json` through a transaction pipeline with lock, hash guard, backup, and atomic writes.
- Manage lightweight Codex model/base URL profiles without capturing auth.
- Import Codex session JSONL usage and show estimated local cost.
- Inspect backups, diagnose runtime state, recover failed switches, and rollback applied switches.
- Manage generic providers, profiles, and target files for advanced local workflows.

## Quick start

```bash
make build

profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
profiledeck plan codex work
profiledeck switch codex work --yes
```

Codex must use file credentials for full account switching. If `$CODEX_HOME/auth.json` is missing, set `cli_auth_credentials_store = "file"` in Codex config and run `codex login` again.

## Documentation map

- [Getting Started](/guide/getting-started) covers local build, runtime initialization, and command shortcuts.
- [Account Switching](/codex/account-switching) covers the Codex full-profile workflow.
- [Switching](/operations/switching) explains plan, apply, backups, and safety checks.
- [Data and Security](/reference/data-security) describes stored secrets, backups, and redaction boundaries.
