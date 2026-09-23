# Codex Profiles

A Codex Profile saves one login and one set of reusable Codex settings, called a Config Set. The login and settings can be shared or copied independently when you fork them to a destination Profile.

Each Config Set contains only the user-level `config.toml`. Sessions, logs, skills, plugin caches, project `.codex/config.toml` files, and system policy are not included.

## Before you start

Codex must store its login in `auth.json`. If that file is missing, add this setting to `$CODEX_HOME/config.toml`, then sign in again:

```toml
cli_auth_credentials_store = "file"
```

Use the Codex login command for the sign-in method you want to save:

```bash
# ChatGPT
codex login

# OpenAI API key
printenv OPENAI_API_KEY | codex login --with-api-key

# Codex access token
printf '%s' "$CODEX_ACCESS_TOKEN" | codex login --with-access-token
```

ProfileDeck can save and switch these file-backed sign-ins. Supplying `OPENAI_API_KEY` or `CODEX_ACCESS_TOKEN` to a Codex process without running the matching login command does not create `auth.json`, so there is no login for ProfileDeck to save.

ProfileDeck also requires a valid `config.toml`. CLI commands resolve the Codex home in this order:

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

## Save a Profile in Desktop

1. Select **Codex → Profiles**.
2. Choose **New Profile**.
3. Enter a permanent Profile ID and a display name.
4. For the first Profile, save the current Codex settings in the default `shared` Config Set.

The first Profile becomes current. To save another login, run the appropriate Codex login command, return to ProfileDeck, and save another Profile. Reuse the current Config Set when both logins should use the same settings, or save a new Config Set when the settings must change independently.

## Save a Profile with the CLI

```bash
profiledeck-cli init
profiledeck-cli codex detect
profiledeck-cli codex profile create work
```

The first Profile saves the current login and settings, creates the `shared` Config Set, and becomes current. Later Profiles reuse the current Config Set by default:

```bash
# Run the appropriate Codex login command first.
profiledeck-cli codex profile create personal
```

Save the current settings separately when needed:

```bash
profiledeck-cli codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## Manage Config Sets

In Desktop, open **Config Sets** from the Codex Profiles page. You can create, copy, rename, or delete saved settings. A Config Set cannot be deleted while a Profile uses it.

The equivalent CLI commands show summaries without printing the complete settings:

```bash
profiledeck-cli codex config-set list
profiledeck-cli codex config-set show shared
profiledeck-cli codex config-set create experimental --name "Experimental"
profiledeck-cli codex config-set copy shared local --name "Local"
profiledeck-cli codex config-set update local --description "Local models"
profiledeck-cli codex config-set delete local --yes
```

Choose different saved settings for an inactive Profile with:

```bash
profiledeck-cli codex profile set-config work shared
```

## Fork a Profile

Forking adds saved Codex data to a destination Profile. The destination can be new, or it can be an existing Profile that does not already contain Codex data. Any data for other Agents remains unchanged. Copy the login or Config Set when the destination Profile must be able to change that item without affecting the source Profile.

Desktop presents the share-or-copy choice in the Fork form. In the CLI, at least one item must use `copy-new`:

```bash
profiledeck-cli codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck-cli codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## Save changes and switch

Codex continues to use normal `auth.json` and `config.toml` files. Before switching away, ProfileDeck preserves valid changes made to the current login or settings.

On the current Profile, open **More actions** and choose **Save Current Login and Settings** in Desktop, or run the following command, before signing in to a different account or replacing the current files when you want to save explicitly:

```bash
profiledeck-cli codex profile save-current
```

In Desktop, choose **Use Profile**, review the hidden-value preview, and confirm. In the CLI:

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

The `--dry-run` preview is optional and read-only. To require the switch to match an earlier preview, pass its fingerprint:

```bash
profiledeck-cli switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

If the current `auth.json` or `config.toml` is missing or invalid, the preview warns that it will not be saved; a confirmed switch can recreate it from the selected Profile. ProfileDeck stops before writing when the current state is unsupported, cannot be checked safely, or changes after review. Open Diagnostics or run `profiledeck-cli doctor` before retrying.

## Delete a Profile

Open a Profile's action menu in Desktop and choose **Delete Profile**, or run:

```bash
profiledeck-cli codex profile delete work --yes
```

This deletes the complete global Profile from every Agent, not only its Codex data. It also deletes saved logins and Config Sets used only by that Profile, while shared saved data remains. A current Profile or one with an unfinished operation cannot be deleted. Deletion does not change Codex `auth.json`, `config.toml`, or any other tool-owned working state.

## Check limits and keep a login active

Desktop can check limits for ChatGPT Codex logins and compatible API Key services. ProfileDeck checks the current Profile once at startup and after a successful switch; use **Refresh limits** when you need a later result. A ChatGPT check can renew a supported Codex sign-in and save the refreshed login.

Set automatic limit refresh to Off, 5, 10, 30, or 60 minutes on the Profile detail page or under **Codex → Settings**. Managed ChatGPT logins can also enable **Renew sign-in automatically**. Both options are off by default and run only while ProfileDeck is open or hidden in the menu bar.

For an API Key Profile with an absolute custom HTTP or HTTPS Base URL, ProfileDeck makes one compatibility request to `/v1/usage` using the saved API Key. A compatible response can show the remaining quota or wallet balance, plan, expiry, and limit windows. API Key limits are checked only at startup, after switching, or when you refresh manually; they never use the automatic interval. An HTTP Base URL sends the API Key and response without transport encryption.

Limit information is temporary and is not saved to disk or added to usage reports. API service responses are used only for the current snapshot; ProfileDeck does not import their historical usage. Codex access-token Profiles can be saved and switched, but their limits and sign-ins are not refreshed automatically.
