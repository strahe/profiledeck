# ProfileDeck

ProfileDeck safely switches local AI coding tool Profiles. The current implementation is Codex-first: each Profile saves a Codex login and a reusable Config Set, so accounts and settings can be switched together.

## Current capabilities

- Create a Codex Profile from `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`.
- Share saved logins and Config Sets independently across Profiles.
- Preview every switch, preserve valid changes made in Codex, create a backup first, and stop if the reviewed files changed.
- Import active and archived Codex session JSONL, then analyze local-time trends, models, sessions, cache usage, and API-equivalent estimated cost.
- Inspect backups, find problems that block switching, recover failed switches, and undo an applied switch.
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

- [Getting Started](/guide/getting-started) covers local build, initial setup, and command shortcuts.
- [Codex Profiles](/codex/profiles) covers saved logins, Config Sets, switching, limits, and backups.
- [Codex Usage and Cost](/codex/usage-cost) covers offline imports, reports, and estimation limits.
- [Switching](/operations/switching) explains plan, apply, backups, and safety checks.
- [Data and Security](/reference/data-security) describes stored secrets, backups, and redaction boundaries.
