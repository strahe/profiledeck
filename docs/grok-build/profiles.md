# Grok Build Profiles

A Grok Build Profile saves a file-based login from `auth.json` and user-level `config.toml` settings. Sessions, logs, plugins, project settings, and managed configuration are not included.

## Before you start

Sign in with Grok Build and confirm that `auth.json` is present, non-empty, and valid. If `config.toml` is missing when a new Config Set is created, its settings are empty. End active Grok sessions before changing files, then start a new session afterward.

Grok Home is selected from `--grok-home`, `GROK_HOME`, then `~/.grok`. Its location is fixed after first setup. Profile creation, saving, and switching are unavailable while `GROK_AUTH` or `GROK_AUTH_PATH` selects another authentication source.

## Save Profiles

```bash
profiledeck-cli grok-build profile create work
```

The first Profile uses the `shared` Config Set, creating it from current settings if needed. Later Profiles reuse the current Profile's saved Config Set without reading the working `config.toml`. To save current settings separately:

```bash
profiledeck-cli grok-build profile create client --new-config-set client
```

To use a non-default Home, put the global option before the command: `profiledeck-cli --grok-home /path/to/grok-home grok-build profile create work`.

Forking can share or copy the login and Config Set independently; at least one must be copied. For example, share the login and copy settings:

```bash
profiledeck-cli grok-build profile fork work client \
  --credential-binding share-parent \
  --config-binding copy-new \
  --new-config-set client
```

See [Profiles and settings](../guide/concepts.md) for sharing and deletion effects.

## Save changes and switch

ProfileDeck saves valid working login and settings changes when you switch away. To save before replacing the working files, run:

```bash
profiledeck-cli grok-build profile save-current
```

An explicit save requires a valid, non-empty `auth.json` and an existing, valid `config.toml`; an empty `config.toml` is valid. If either check fails, neither saved item changes. A switch may still restore valid files from the selected Profile. Its preview hides both files' contents. An authentication override in `config.toml` may bypass the selected login; ProfileDeck warns but does not change the setting. See [Review and Switch](../operations/switching.md).

## Check credits

Desktop checks the current Profile at startup and after switching; later checks are manual. It cannot refresh an inactive Profile. A check uses the installed Grok Build app and may renew its current login. Results are temporary and separate from [local usage reports](./usage-cost.md); they are not account bills.

Checks require a supported saved login and a working Grok Build installation. `GROK_AUTH` and `GROK_AUTH_PATH` are unsupported. If Grok Build was installed with `GROK_BIN_DIR`, make that same absolute directory available to ProfileDeck.
