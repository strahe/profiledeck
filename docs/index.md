# ProfileDeck

ProfileDeck safely switches local AI coding tool profiles. The current implementation is Codex-first: a Profile combines a hidden login credential with a reusable Config Set, while the transaction pipeline switches their working copies, preserves valid local changes, and supports recovery.

## Current capabilities

- Create a Codex Profile from `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`.
- Share credentials and complete Config Sets independently across Profiles.
- Automatically capture valid working-copy changes while switching through lock, hash guard, backup, and atomic writes.
- Import active and archived Codex session JSONL, then analyze local-time trends, models, sessions, cache usage, and API-equivalent estimated cost.
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
- [Codex Profiles](/codex/profiles) covers credentials, Config Sets, and working-copy behavior.
- [Codex Usage and Cost](/codex/usage-cost) covers offline imports, reports, and estimation limits.
- [Switching](/operations/switching) explains plan, apply, backups, and safety checks.
- [Data and Security](/reference/data-security) describes stored secrets, backups, and redaction boundaries.
