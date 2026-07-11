# Codex Profiles

A Codex Profile combines two independently shareable resources:

- a hidden credential containing the desired `$CODEX_HOME/auth.json` payload;
- a Config Set containing the complete desired `$CODEX_HOME/config.toml` payload.

The files on disk are working copies of the active Profile. ProfileDeck stores long-lived state in `profiledeck.db`, checks valid working-copy changes back into the active bindings during a switch, and writes only the resources whose bindings change.

Config Sets cover only the user-level `config.toml`. Sessions, logs, skills, plugin caches, project `.codex/config.toml` files, and system policy remain outside this model. Codex `tokens.account_id` is display metadata only and never determines identity or binding behavior.

## Requirements

Codex must use file credentials. If `$CODEX_HOME/auth.json` is missing, add this to `$CODEX_HOME/config.toml` and log in again:

```toml
cli_auth_credentials_store = "file"
```

```bash
codex login
```

## Create Profiles

In Desktop, use **Save Current Codex as a New Profile**. Profile ID is the stable, immutable CLI and route key; Name is the user-facing label. ProfileDeck enables this action only when both current Codex files are valid, while existing saved Profiles remain available when the source files are missing or invalid.

The first Profile captures the current files, creates a Config Set named `shared`, creates a hidden credential, and becomes active:

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

Later Profiles reuse the active Config Set by default. Log in with another Codex account, then create another Profile to capture a separate credential without duplicating configuration:

```bash
codex login
profiledeck codex profile create personal
```

To preserve the current config as an independent Config Set, create the Profile with a new Config Set ID:

```bash
profiledeck codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## Manage Config Sets

Config Set commands expose summaries and metadata, never raw TOML:

```bash
profiledeck codex config-set list
profiledeck codex config-set show shared
profiledeck codex config-set create experimental --name "Experimental"
profiledeck codex config-set copy shared local --name "Local"
profiledeck codex config-set update local --description "Local models"
profiledeck codex config-set delete local --yes
```

`create` captures the current `config.toml`. A Config Set can be renamed, including `shared`, and can be deleted only when no Profile references it. Rebind an inactive Profile with:

```bash
profiledeck codex profile set-config work shared
```

## Fork a Profile

Forking requires explicit choices for both resources. At least one resource must be copied so the result is not only an alias of the source:

```bash
profiledeck codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## Save and Switch

Switching automatically captures valid external changes to the active credential and Config Set. `save-current` is an explicit safety action before logging in again or replacing a working copy:

```bash
profiledeck codex profile save-current
profiledeck plan codex work
profiledeck switch codex work --yes
```

In Desktop, the corresponding action is **Update from Current Codex** on the active Profile detail page. It updates the active hidden credential and Config Set from the current working copies after a fresh source validation.

`plan` is read-only. `switch`, `rollback`, and `recover` are the only paths that write Codex target files. Invalid or missing working copies are not captured; the plan reports a warning and the backup retains the filesystem state.

## Read Usage Limits

The Desktop Profiles page can read the current ChatGPT Codex rate limits for saved login states. At Desktop startup, ProfileDeck reads the active Profile once after active state is available; it does not read inactive Profiles or repeat the request when the page is reopened. Use **Refresh limits** on one Profile row or on its detail page for later reads. There is no refresh-all action.

The list shows each returned window's remaining percentage and reset time. The detail page also shows consumed percentage, plan, limit state, credits, spend controls, earned resets, and additional metered limits when the service returns them. `used_percent` is consumed capacity, so the remaining value is `100 - used_percent`.

Manual refresh normally starts the installed `codex app-server` and calls its native account rate-limit method. Codex may refresh a managed OAuth login according to its own token rules. ProfileDeck captures a changed active `auth.json` back into the credential currently bound by `credential_id`; inactive credentials run in a private temporary Codex home and update through a payload-hash compare-and-swap. `tokens.account_id` remains display metadata and is never used for this ownership decision.

If app-server is missing or its protocol is incompatible, manual refresh falls back to the fixed, read-only ChatGPT Codex quota endpoint. The fallback does not refresh or write tokens. Profile-controlled model-provider URLs never receive the saved ChatGPT token.

On a Profile detail page or under **Codex Settings**, automatic limit refresh can be set to Off, 5, 10, 30, or 60 minutes. Both entry points edit the same persisted setting and update immediately. It is off by default. ProfileDeck runs one credential request at a time, spaces different credentials, and deduplicates shared credentials using the shortest enabled interval. The first automatic run is spread across a full interval and later runs include timing jitter.

Managed ChatGPT logins can also enable **Keep login available**. When automatic limit refresh is off, ProfileDeck asks Codex to refresh near the access-token expiry time, or eight days after the last recorded refresh when the token expiry cannot be read. External `chatgptAuthTokens` logins can query limits but cannot use native keepalive. Expired, reused, or revoked refresh tokens pause automatic work until the credential changes; transient failures use increasing retry delays.

Automatic tasks run only while ProfileDeck is open or hidden in the tray. They do not run after the application exits, and they cannot preserve a login after the service revokes its refresh token. The serial native call pattern reduces simultaneous multi-credential requests but does not guarantee that a service cannot associate accounts.

Limit snapshots stay in process memory. They are separate from the offline session analysis on the Usage page, are not billing balances, and do not attribute local sessions to a Profile or account.

## Back Up and Restore Profiles

Update the active Profile from valid current working-copy changes before export, then write the bundle outside any runtime directory you plan to delete:

```bash
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
```

The default export includes every Codex Profile, referenced hidden credential, and Config Set, including unreferenced Config Sets. Pass one or more Profile IDs to export only those Profiles and their dependencies:

```bash
profiledeck codex profile export work personal \
  --output ./selected-codex-profiles.json
```

The JSON bundle contains raw `auth.json` and complete `config.toml` payloads. ProfileDeck writes it with `0600` permissions on POSIX systems and does not print payloads in command output. Keep it private.

After initializing a new database, inspect the import before applying it:

```bash
profiledeck init
profiledeck codex profile import inspect ./profiledeck-codex-profiles.json
profiledeck codex profile import apply ./profiledeck-codex-profiles.json \
  --plan-fingerprint <reviewed-fingerprint> \
  --yes
```

Missing resources are created, identical resources are skipped, and any same-ID difference blocks the whole import. Import uses the current `CODEX_HOME` to rebuild Profile targets in one database transaction. It does not restore active state, automation settings, or write `auth.json` or `config.toml`; imported Profiles start with automatic limit refresh and keepalive disabled. Use the normal plan and switch flow after import.
