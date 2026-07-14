# ProfileDeck

ProfileDeck safely switches local AI coding tool Profiles. Codex Profiles save a login and reusable Config Set. Claude Code Profiles save official subscription logins. Antigravity Profiles save the consumer OAuth login used by Antigravity agy v2.

## Current capabilities

- Create a Codex Profile from `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`.
- Save and switch multiple official Claude Code subscription logins without changing Claude Code settings.
- Save and switch Antigravity agy v2 logins through the system credential store.
- Share saved logins and Config Sets independently across Profiles.
- Preview every switch, preserve supported valid changes in the current tool state, create a backup first, and stop if a reviewed external target changed.
- Import active and archived Codex session JSONL, then analyze local-time trends, models, sessions, cache usage, and API-equivalent estimated cost.
- Inspect backups, find problems that block switching, recover failed switches, and undo an applied switch.
- Keep the macOS app up to date, view download progress, and choose when to restart.
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
- [Desktop Updates](/guide/updates) explains how to check for updates, install one, and open the macOS app for the first time.
- [Codex Profiles](/codex/profiles) covers saved logins, Config Sets, switching, limits, and backups.
- [Claude Code Profiles](/claude-code/profiles) covers official subscription login capture, switching, and platform credential locations.
- [Antigravity Profiles](/antigravity/profiles) covers agy v2 login capture, switching, and limitations.
- [Codex Usage and Cost](/codex/usage-cost) covers offline imports, reports, and estimation limits.
- [Switching](/operations/switching) explains plan, apply, backups, and safety checks.
- [Data and Security](/reference/data-security) describes stored secrets, backups, and redaction boundaries.
