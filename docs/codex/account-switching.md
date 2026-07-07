# Codex Account Switching

Full Codex account switching captures and restores the two user-level files that matter for a login:

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`

ProfileDeck does not split or move `sessions/`, logs, skills, or other Codex state. Those remain shared under the same `CODEX_HOME`.

## Requirements

Codex must use file credentials. If `$CODEX_HOME/auth.json` is missing, add this to `$CODEX_HOME/config.toml` and login again:

```toml
cli_auth_credentials_store = "file"
```

Then run:

```bash
codex login
```

## Capture the current account

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
```

By default, the local ProfileDeck account id is the profile id. Use `--account` only when you want a different local alias:

```bash
profiledeck codex profile capture work --account work-login
```

The local account id is not the same as Codex `tokens.account_id`. ProfileDeck stores `tokens.account_id` as metadata only.

## Switch to a captured profile

```bash
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` is read-only. `switch` writes `config.toml` and `auth.json` only through the transaction pipeline.

## Capture another account

1. Login with the other Codex account so `$CODEX_HOME/auth.json` represents that account.
2. Capture it under a different profile:

```bash
profiledeck codex profile capture personal
```

After both profiles are captured:

```bash
profiledeck switch codex work --yes
profiledeck switch codex personal --yes
```

## Manage stored accounts

List and inspect account metadata:

```bash
profiledeck codex account list
profiledeck codex account show work
```

These commands do not print raw tokens.

Export and import are explicit sensitive operations:

```bash
profiledeck codex account export work --output ./auth.json
profiledeck codex account import work-edited --auth-file ./auth.json
```

Export writes raw Codex auth JSON to a `0600` file when the platform supports it. Treat the exported file as a secret.
