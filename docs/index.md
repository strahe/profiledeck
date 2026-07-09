# ProfileDeck

ProfileDeck safely switches local AI coding tool profiles. The current implementation is Codex-first: it can create full-file Codex profiles from user configuration and file-based authentication, switch between stored profiles, import local token usage, and recover from interrupted switch operations.

## Current capabilities

- Create a Codex profile from `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`.
- Switch Codex `config.toml` and `auth.json` through a transaction pipeline with lock, hash guard, backup, and atomic writes.
- Share or fork hidden Codex auth credentials between full-file profiles.
- Import Codex session JSONL usage and show estimated local cost.
- Inspect backups, diagnose runtime state, recover failed switches, and rollback applied switches.
- Manage generic providers, profiles, and target files for advanced local workflows.

## Quick start

```bash
make build

profiledeck init
profiledeck codex detect
profiledeck codex profile create work
profiledeck plan codex work
profiledeck switch codex work --yes
```

Codex must use file credentials for profile switching. If `$CODEX_HOME/auth.json` is missing, set `cli_auth_credentials_store = "file"` in Codex config and run `codex login` again.

## Documentation map

- [Getting Started](/guide/getting-started) covers local build, runtime initialization, and command shortcuts.
- [Codex Profiles](/codex/profiles) covers the Codex full-profile workflow.
- [Switching](/operations/switching) explains plan, apply, backups, and safety checks.
- [Data and Security](/reference/data-security) describes stored secrets, backups, and redaction boundaries.
