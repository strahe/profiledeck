# Codex Profiles

A Codex Profile saves one Codex login and one Config Set. This lets you switch accounts and settings together while still editing the active settings in Codex itself.

A Config Set covers only the user-level `config.toml`. Sessions, logs, skills, plugin caches, project `.codex/config.toml` files, and system policy are not included.

## Prerequisite

Codex must use file credentials. If `$CODEX_HOME/auth.json` is missing, add this to `$CODEX_HOME/config.toml` and sign in again:

```toml
cli_auth_credentials_store = "file"
```

```bash
codex login
```

## Create a Profile

In Desktop, choose **Save Current Codex as a New Profile**. The Profile ID is used in CLI commands and links and cannot be changed later. The name is what ProfileDeck displays throughout the app.

The first Profile saves the current Codex login and settings, creates a Config Set named `shared`, and becomes current:

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

Later Profiles reuse the current Config Set by default. To add another login without copying the settings, sign in to Codex with that account and create another Profile:

```bash
codex login
profiledeck codex profile create personal
```

To save the current settings as a separate Config Set, provide a new ID:

```bash
profiledeck codex profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

## Manage Config Sets

Config Set commands show names and summaries without printing complete Codex settings:

```bash
profiledeck codex config-set list
profiledeck codex config-set show shared
profiledeck codex config-set create experimental --name "Experimental"
profiledeck codex config-set copy shared local --name "Local"
profiledeck codex config-set update local --description "Local models"
profiledeck codex config-set delete local --yes
```

`create` saves the current `config.toml`. Any Config Set, including `shared`, can be renamed. A Config Set can be deleted only when no Profile uses it.

Choose a different Config Set for an inactive Profile with:

```bash
profiledeck codex profile set-config work shared
```

## Fork a Profile

Forking creates a new Profile and lets you share or copy its login and Config Set. At least one item must be copied so the new Profile can change independently:

```bash
profiledeck codex profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck codex profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## Save and switch

Before switching, ProfileDeck preserves valid changes made to the current Codex login or settings. Use `save-current` before signing in to a different account or replacing the current Codex files when you want to save explicitly:

```bash
profiledeck codex profile save-current
profiledeck plan codex work
profiledeck switch codex work --yes
```

In Desktop, use **Update from Current Codex** on the current Profile detail page.

`plan` is read-only. It shows the files that would change with sensitive values hidden. A switch creates a backup first and stops without writing if a required file is missing, invalid, unsupported, or changed after review.

## Check usage limits

The Desktop Profiles page can check current ChatGPT Codex limits for a saved login. At startup, ProfileDeck checks the current Profile once. It does not check inactive Profiles or repeat the request when you reopen the page.

Use **Refresh limits** on one Profile row or detail page for a later check. There is no refresh-all action. The list shows the remaining percentage and reset time for each period; the detail page shows any additional limit information provided by Codex.

When the saved sign-in method supports renewal, checking limits may also renew the Codex login. Some external sign-in methods can provide limits but cannot be renewed automatically. If automatic Codex updates are unavailable, a manual limit check may still work without changing the saved login.

Set automatic limit refresh to Off, 5, 10, 30, or 60 minutes on the Profile detail page or under **Codex Settings**. The setting is off by default and changes in either place appear in the other.

Managed ChatGPT logins can also enable **Renew sign-in automatically**. Automatic limit refresh already includes sign-in renewal, so this option is mainly useful when automatic limit refresh is off.

Automatic updates run only while ProfileDeck is open or hidden in the tray. They stop when ProfileDeck exits and cannot keep a login active after the service revokes it.

Limit information is temporary, is not written to `profiledeck.db`, and is separate from the local Usage report. It is not a billing balance and does not connect local sessions to a Profile or account.

## Back up and restore Profiles

Save current changes before exporting, and keep the backup outside any ProfileDeck data directory you plan to delete:

```bash
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
```

Without Profile IDs, export includes all Codex Profiles and Config Sets. Provide one or more Profile IDs to export only those Profiles and the logins and Config Sets they need:

```bash
profiledeck codex profile export work personal \
  --output ./selected-codex-profiles.json
```

The JSON backup contains complete Codex sign-in data and settings. ProfileDeck writes it with `0600` permissions on POSIX systems and does not print the sensitive contents in command output. Anyone with the file may be able to access your account, so keep it private.

After initializing a new database, inspect the backup before importing it:

```bash
profiledeck init
profiledeck codex profile import inspect ./profiledeck-codex-profiles.json
profiledeck codex profile import apply ./profiledeck-codex-profiles.json \
  --plan-fingerprint <reviewed-fingerprint> \
  --yes
```

Import adds missing data, skips identical data, and makes no changes when existing Codex data conflicts. If a global Profile with the same ID has no Codex bindings yet, import attaches them and keeps that Profile's existing name and description. It does not make a Profile current, restore automatic-update settings, or write `auth.json` or `config.toml`. Review and apply a normal switch when you are ready to use an imported Profile.
