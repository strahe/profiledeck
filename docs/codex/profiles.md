# Codex Profiles

A Codex Profile saves one login and one set of reusable Codex settings, called a Config Set. The login and settings can be shared or copied independently when you create another Profile.

Each Config Set contains only the user-level `config.toml`. Sessions, logs, skills, plugin caches, project `.codex/config.toml` files, and system policy are not included.

## Before you start

Codex must store its login in `auth.json`. If that file is missing, add this setting to `$CODEX_HOME/config.toml`, then sign in again:

```toml
cli_auth_credentials_store = "file"
```

```bash
codex login
```

ProfileDeck also requires a valid `config.toml`. CLI commands resolve the Codex home in this order:

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

## Save a Profile in Desktop

1. Select **Codex → Profiles**.
2. Choose **Save Current**.
3. Enter a permanent Profile ID and a display name.
4. For the first Profile, save the current Codex settings in the default `shared` Config Set.

The first Profile becomes current. To save another login, run `codex login` for that account, return to ProfileDeck, and save another Profile. Reuse the current Config Set when both accounts should use the same settings, or save a new Config Set when the settings must change independently.

## Save a Profile with the CLI

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

The first Profile saves the current login and settings, creates the `shared` Config Set, and becomes current. Later Profiles reuse the current Config Set by default:

```bash
codex login
profiledeck codex profile create personal
```

Save the current settings separately when needed:

```bash
profiledeck codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## Manage Config Sets

In Desktop, open **Config Sets** from the Codex Profiles page. You can create, copy, rename, or delete saved settings. A Config Set cannot be deleted while a Profile uses it.

The equivalent CLI commands show summaries without printing the complete settings:

```bash
profiledeck codex config-set list
profiledeck codex config-set show shared
profiledeck codex config-set create experimental --name "Experimental"
profiledeck codex config-set copy shared local --name "Local"
profiledeck codex config-set update local --description "Local models"
profiledeck codex config-set delete local --yes
```

Choose different saved settings for an inactive Profile with:

```bash
profiledeck codex profile set-config work shared
```

## Fork a Profile

Forking creates another Profile from saved data. Copy the login or Config Set when the new Profile must be able to change that item without affecting the source Profile.

Desktop presents the share-or-copy choice in the Fork form. In the CLI, at least one item must use `copy-new`:

```bash
profiledeck codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## Save changes and switch

Codex continues to use normal `auth.json` and `config.toml` files. Before switching away, ProfileDeck preserves valid changes made to the current login or settings.

Use **Update from Current Codex** in Desktop, or run the following command, before signing in to a different account or replacing the current files when you want to save explicitly:

```bash
profiledeck codex profile save-current
```

In Desktop, choose **Use Profile**, review the hidden-value preview, and confirm. In the CLI:

```bash
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` is read-only. To require the switch to match an earlier preview, pass its fingerprint:

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

If the current `auth.json` or `config.toml` is missing or invalid, the preview warns that it will not be saved; a confirmed switch can recreate it from the selected Profile. ProfileDeck stops before writing when the current state is unsupported, cannot be checked safely, or changes after review. Open Diagnostics or run `profiledeck doctor` before retrying.

## Check limits and keep a login active

Desktop can check the current ChatGPT Codex limits for one saved Profile. ProfileDeck checks the current Profile once at startup; use **Refresh limits** when you need a later result. A check can renew a supported Codex sign-in and save the refreshed login. Inactive Profiles are not checked automatically unless you enable an interval for them.

Set automatic limit refresh to Off, 5, 10, 30, or 60 minutes on the Profile detail page or under **Codex → Settings**. Managed ChatGPT logins can also enable **Renew sign-in automatically**. Both options are off by default and run only while ProfileDeck is open or hidden in the menu bar.

Limit information is temporary and is not saved to disk. It is not a billing balance and does not connect local sessions to a Profile or account. Some external sign-in methods can show limits but cannot be renewed automatically.

## Back up and restore Profiles

Save current changes before exporting, and keep the backup outside any ProfileDeck data directory you plan to remove:

```bash
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
```

Without Profile IDs, the command exports every Codex Profile and Config Set. To export selected Profiles and the data they need:

```bash
profiledeck codex profile export work personal \
  --output ./selected-codex-profiles.json
```

The JSON file contains complete Codex sign-in data and settings. Where the operating system supports private file permissions, ProfileDeck restricts the export to your user account. It does not print the sensitive contents. Anyone with the file may be able to access your account.

Inspect a backup before importing it into an initialized ProfileDeck setup:

```bash
profiledeck codex profile import inspect ./profiledeck-codex-profiles.json
profiledeck codex profile import apply ./profiledeck-codex-profiles.json \
  --plan-fingerprint <reviewed-fingerprint> \
  --yes
```

Import adds missing data, skips identical data, and stops without changes when saved Codex data conflicts. Imported Profiles do not become current and do not write `auth.json` or `config.toml`. Review and apply a normal switch when you are ready to use one.
