# ProfileDeck
Safe profile switching for AI agents and coding tools.

## Codex CLI MVP

Capture the current Codex user config and file auth, then switch back to it later:

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
profiledeck plan codex work
profiledeck switch codex work --yes
```

`codex profile capture` reads `$CODEX_HOME/config.toml` and `$CODEX_HOME/auth.json`. Codex must be using file credentials; if `auth.json` is missing, set `cli_auth_credentials_store = "file"` in Codex config and run `codex login` again.

By default, the local ProfileDeck account id is the profile id. Use `--account ACCOUNT_ID` to store the captured auth under a different local alias. ProfileDeck records Codex `tokens.account_id` as metadata only because it is not guaranteed to uniquely identify a local login.

Captured auth is stored locally in `profiledeck.db`; switch backups may also contain previous `auth.json` content. Treat the ProfileDeck runtime directory as sensitive local data.

Stored auth can be reviewed or edited through explicit export/import:

```bash
profiledeck codex account list
profiledeck codex account export <account-id-from-list> --output ./auth.json
profiledeck codex account import <local-account-id> --auth-file ./auth.json
```

For lightweight model/base URL switching without capturing auth:

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile set work --model gpt-5.3-codex
profiledeck plan codex work
profiledeck switch codex work --yes
```

Use `--codex-dir PATH` when Codex uses a non-default home. Without it, ProfileDeck checks `CODEX_HOME` and then `~/.codex`.

`codex profile set` stores the full desired state for ProfileDeck-managed Codex keys. Omitting `--openai-base-url` removes the managed `openai_base_url` from `config.toml` on switch. Pass `--account ACCOUNT_ID` to bind an existing captured/imported local Codex account.
