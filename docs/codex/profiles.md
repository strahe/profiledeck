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

`plan` is read-only. `switch`, `rollback`, and `recover` are the only paths that write Codex target files. Invalid or missing working copies are not captured; the plan reports a warning and the backup retains the filesystem state.
