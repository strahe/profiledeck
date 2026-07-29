# Grok Build Profiles

A Grok Build Profile saves one file-based login and one set of reusable user settings, called a Config Set. The login and settings can be shared or copied independently when you create another Profile.

ProfileDeck manages only `auth.json` and the user-level `config.toml` in the selected Grok Home. Sessions, logs, plugins, project settings, managed configuration, and other Grok files are not included.

## Before you start

Sign in with Grok Build and confirm that its Grok Home contains a non-empty, valid `auth.json`. A missing `config.toml` is allowed and is saved as empty settings when ProfileDeck creates a Config Set from the working file.

CLI commands resolve the Grok Home in this order:

1. `--grok-home`
2. `GROK_HOME`
3. `~/.grok`

The first successful setup binds the Grok Build Provider to that absolute Home. ProfileDeck rejects a different Home later instead of silently switching locations.

Profile creation, `save-current`, and switching are unavailable while `GROK_AUTH` or `GROK_AUTH_PATH` selects another authentication source. Unset those variables and use Grok Build's `auth.json` before those operations.

End active Grok sessions before an operation that changes files, then start a new session afterward. ProfileDeck coordinates `auth.json` changes with Grok Build, but it does not treat a process or lock file as proof that a session is running.

## Save a Profile in Desktop

1. Select **Grok Build → Profiles**.
2. Choose **Save Current**.
3. Enter a permanent Profile ID and a display name.
4. For the first Profile, use the default `shared` Config Set. ProfileDeck creates it from the current settings only if it does not already exist.

The first Profile becomes current. To save another login, sign in to that account with Grok Build, return to ProfileDeck, and save another Profile. Reusing the current Config Set only binds its saved settings; it does not read or overwrite the current `config.toml`. Create a separate Config Set when the current settings should be captured independently.

## Save a Profile with the CLI

```bash
profiledeck-cli init
profiledeck-cli grok-build detect
profiledeck-cli grok-build profile create work
```

The first Profile saves the current login, uses `shared`, and becomes current. If `shared` does not exist, ProfileDeck creates it from the current settings; a pre-created `shared` Config Set is left unchanged. Later Profiles reuse the current Profile's saved Config Set by default without reading or overwriting the working `config.toml`:

```bash
grok logout
grok login
profiledeck-cli grok-build profile create personal
```

Save the current settings separately when needed:

```bash
profiledeck-cli grok-build profile create client \
  --new-config-set client \
  --config-set-name "Client"
```

To use another Grok Home, place the global option before the command:

```bash
profiledeck-cli --grok-home /path/to/grok-home grok-build detect
```

## Manage Config Sets

In Desktop, open **Config Sets** from the Grok Build Profiles page. You can create, copy, rename, or delete saved settings. A Config Set cannot be deleted while a Profile uses it.

The equivalent CLI commands show summaries without printing `config.toml`:

```bash
profiledeck-cli grok-build config-set list
profiledeck-cli grok-build config-set show shared
profiledeck-cli grok-build config-set create experimental --name "Experimental"
profiledeck-cli grok-build config-set copy shared local --name "Local"
profiledeck-cli grok-build config-set update local --description "Local settings"
profiledeck-cli grok-build config-set delete local --yes
```

Choose different saved settings for an inactive Profile with:

```bash
profiledeck-cli grok-build profile set-config work shared
```

## Fork a Profile

Forking creates another Profile from saved data. Copy the login or Config Set when the new Profile must be able to change that item without affecting the source Profile.

Desktop presents the share-or-copy choice in the Fork form. In the CLI, at least one item must use `copy-new`:

```bash
profiledeck-cli grok-build profile fork work client-login \
  --credential-binding copy-new \
  --config-binding share-parent

profiledeck-cli grok-build profile fork work client-config \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client-config
```

## Save changes and switch

Grok Build continues to use normal `auth.json` and `config.toml` files. Before switching away, ProfileDeck preserves valid changes made to the current login or settings. Use **Update from Current Grok Build** in Desktop, or run:

```bash
profiledeck-cli grok-build profile save-current
```

An explicit save requires a valid, non-empty `auth.json`; it does not partially save only the settings.

In Desktop, choose **Use Profile**, review the actions, target paths, and warnings, then confirm. In the CLI:

```bash
profiledeck-cli switch grok-build work --dry-run
profiledeck-cli switch grok-build work --yes
```

The preview never contains `auth.json` or `config.toml` content. Both files are saved byte for byte in ProfileDeck's local data, while public output shows only the action, target, path, and warnings.

If the current working copy is missing or invalid, ProfileDeck warns that it will not be saved. A confirmed switch can still restore the selected Profile's valid saved files. If `config.toml` contains an authentication override, ProfileDeck warns that Grok Build may bypass the selected saved login; it does not print the setting or change it automatically.

## Delete a Profile

Open a Profile's action menu in Desktop and choose **Delete Profile**, or run:

```bash
profiledeck-cli grok-build profile delete work --yes
```

This deletes the complete global Profile from every Agent, not only its Grok Build data. It also deletes saved logins and Config Sets used only by that Profile, while shared saved data remains. A current Profile or one with an unfinished operation cannot be deleted. Deletion does not change Grok Build's working files.

Grok Build usage, quotas, credits, and custom authentication sources are not supported by this integration.
