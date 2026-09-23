# Codex Profiles

A Codex Profile saves one login and one reusable set of user-level `config.toml` settings, called a Config Set. Sessions, logs, skills, plugins, project settings, and system policy are not included.

## Before you start

Codex must save its login in `auth.json` and have a valid `config.toml`. If `auth.json` is missing, add this setting to `$CODEX_HOME/config.toml` and sign in again:

```toml
cli_auth_credentials_store = "file"
```

Use the login command for the account type you need:

```bash
codex login
printenv OPENAI_API_KEY | codex login --with-api-key
printf '%s' "$CODEX_ACCESS_TOKEN" | codex login --with-access-token
```

Passing an API key or access token only as an environment variable does not create `auth.json`. CLI commands look for Codex files in `--codex-dir`, `CODEX_HOME`, then `~/.codex`.

## Save Profiles

```bash
profiledeck-cli codex profile create work
```

The first Profile saves the current login and settings in a `shared` Config Set. After signing in to another account, create another Profile. It reuses the current Config Set unless you request a separate one:

```bash
profiledeck-cli codex profile create personal
profiledeck-cli codex profile create client --new-config-set client
```

Shared Config Set changes affect every Profile using it. To give an existing inactive Profile different saved settings, use `profiledeck-cli codex profile set-config <profile-id> <config-set-id>`. See [Profiles and Config Sets](../guide/concepts.md) for sharing and deletion effects.

## Fork a Profile

Forking adds Codex data to a new Profile or to an existing Profile without Codex data. Choose whether the login and settings should be shared or copied; at least one must be copied. For example, to share the login and copy the settings:

```bash
profiledeck-cli codex profile fork work client \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client
```

## Save changes and switch

Codex keeps using its normal `auth.json` and `config.toml`. ProfileDeck saves valid changes from the current Profile when you switch away. To save them before signing in to another account or replacing those files, run:

```bash
profiledeck-cli codex profile save-current
```

If either working file is missing or invalid, ProfileDeck warns that it will not save that file. A switch can still restore valid files from the selected Profile. See [Review and Switch](../operations/switching.md) for the CLI command and recovery behavior.

## Check limits and keep a login active

Desktop can check limits for ChatGPT Codex logins and compatible API Key services. Current-Profile checks run at startup and after a switch. Automatic ChatGPT limit refresh and sign-in renewal are optional and off by default; otherwise later checks are manual. A ChatGPT check may renew and save the login.

For an API Key Profile with a custom absolute HTTP or HTTPS Base URL, a limit check sends the saved key to that URL's `/v1/usage`. API Key checks run only at startup, after switching, or manually. HTTP does not encrypt the key or response in transit. Codex access-token Profiles do not have automatic limit or login refresh.

Limit snapshots are temporary and separate from [local usage reports](./usage-cost.md). They do not identify which Profile produced earlier activity.
