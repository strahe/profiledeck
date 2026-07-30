# CLI Reference

Use this page for command names and common options. Run `profiledeck-cli --help` or `profiledeck-cli <command> --help` for the exact help included with your installed version; installed help takes precedence if it differs from this page.

Angle brackets mark required values. Square brackets mark optional arguments.

## Global options

Every command accepts:

```text
--config-dir string  Use a custom ProfileDeck config directory
--grok-home string   Use a custom Grok Build Home
```

`--config-dir` is the parent config directory. ProfileDeck creates or uses its `profiledeck` folder below it.

`--grok-home` overrides `GROK_HOME` and the default `~/.grok` location. Put a global option before the command name.

## Commands

| Command | Use it to |
| --- | --- |
| `antigravity` | Save and manage Antigravity Profiles. |
| `backup` | Create, export, restore, and manage encrypted application backups. |
| `claude-code` | Save and manage Claude Code account-login Profiles. |
| `codex` | Manage Codex Profiles and saved settings (Config Sets). |
| `doctor` | Diagnose local-data, permission, and interrupted-operation problems. |
| `grok-build` | Manage Grok Build Profiles and saved settings (Config Sets). |
| `init` | Create ProfileDeck's local data. |
| `provider` | Configure another AI tool for advanced file switching. |
| `profile` | Manage Profiles and advanced file targets. |
| `recover` | Resolve an interrupted or failed switch. |
| `status` | Check whether ProfileDeck is initialized. |
| `switch` | Preview or apply a Profile switch. |
| `usage` | Import and report local Codex usage. |
| `version` | Print version information. |

## Setup and status

```bash
profiledeck-cli init [--json]
profiledeck-cli status [--json]
profiledeck-cli version
```

## Codex

```bash
profiledeck-cli codex detect [--codex-dir PATH] [--json]
profiledeck-cli codex profile list [--json]
profiledeck-cli codex profile show <profile-id> [--json]
profiledeck-cli codex profile create <profile-id> [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex profile fork <source-profile-id> <destination-profile-id> --credential-binding share-parent|copy-new --config-binding share-parent|copy-new [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex profile save-current [--codex-dir PATH] [--json]
profiledeck-cli codex profile set-config <profile-id> <config-set-id> [--json]
profiledeck-cli codex profile delete <profile-id> --yes [--json]

profiledeck-cli codex config-set list [--json]
profiledeck-cli codex config-set show <config-set-id> [--json]
profiledeck-cli codex config-set create <config-set-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set copy <source-id> <new-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set update <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli codex config-set delete <config-set-id> --yes [--json]
```

The first `profile create` saves the current Codex login and settings and creates the `shared` Config Set. Later creates reuse the current Config Set unless you pass `--new-config-set`.

`fork` requires choices for both the login and Config Set, and at least one choice must be `copy-new`. The destination can be a new Profile or an existing Profile that has no Codex data. When reusing a Profile, omitted `--name` and `--description` values leave its details unchanged. Copying settings also requires `--new-config-set`. `save-current` saves the login and settings currently used by Codex. `set-config` changes only a Profile that is not current.

`config-set create` saves the current `config.toml`. List and show commands return safe summaries. You can delete only a Config Set that no Profile uses.

See [Codex Profiles](../codex/profiles.md) for task-based examples and safety guidance.

## Grok Build

```bash
profiledeck-cli grok-build detect [--json]
profiledeck-cli grok-build profile list [--json]
profiledeck-cli grok-build profile show <profile-id> [--json]
profiledeck-cli grok-build profile create <profile-id> [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--name NAME] [--description TEXT] [--json]
profiledeck-cli grok-build profile fork <source-profile-id> <destination-profile-id> --credential-binding share-parent|copy-new --config-binding share-parent|copy-new [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--name NAME] [--description TEXT] [--json]
profiledeck-cli grok-build profile save-current [--json]
profiledeck-cli grok-build profile set-config <profile-id> <config-set-id> [--json]
profiledeck-cli grok-build profile delete <profile-id> --yes [--json]

profiledeck-cli grok-build config-set list [--json]
profiledeck-cli grok-build config-set show <config-set-id> [--json]
profiledeck-cli grok-build config-set create <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli grok-build config-set copy <source-id> <new-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli grok-build config-set update <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli grok-build config-set delete <config-set-id> --yes [--json]
```

The first `profile create` saves the current file-based login and uses the `shared` Config Set. If `shared` does not exist, ProfileDeck creates it from the current `config.toml`; a missing file becomes empty settings. A pre-created `shared` Config Set is reused without changing it. Later creates reuse the current Profile's saved Config Set without reading or overwriting the working `config.toml`, unless you pass `--new-config-set`. `auth.json` must be present, non-empty, and valid.

`fork` requires choices for both the login and Config Set, and at least one choice must be `copy-new`. The destination can be a new Profile or an existing Profile that has no Grok Build data. When reusing a Profile, omitted `--name` and `--description` values leave its details unchanged. `save-current` requires a valid `auth.json` and an existing, valid `config.toml`; an empty `config.toml` is valid. If either requirement fails, neither the saved login nor settings change. Config Set list and detail output never contains `config.toml`.

Set `--grok-home` before `grok-build` when needed:

```bash
profiledeck-cli --grok-home /path/to/grok-home grok-build detect
```

The Provider remains bound to the first initialized Home. Profile creation, `save-current`, and switching are unavailable while `GROK_AUTH` or `GROK_AUTH_PATH` is set. See [Grok Build Profiles](../grok-build/profiles.md) for switching and safety guidance.

## Claude Code

```bash
profiledeck-cli claude-code detect [--json]
profiledeck-cli claude-code profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli claude-code profile list [--json]
profiledeck-cli claude-code profile show <profile-id> [--json]
profiledeck-cli claude-code profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli claude-code profile save-current [--yes] [--json]
profiledeck-cli claude-code profile delete <profile-id> --yes [--json]
```

`create` saves the current Claude Code account login and makes the new Profile current. `save-current` updates the login used by the current Profile. If that saved login is shared, the command reports how many Profiles will change and requires `--yes`.

There is no `claude` alias. Preview with `profiledeck-cli switch claude-code <profile-id> --dry-run` when needed, then apply with `profiledeck-cli switch claude-code <profile-id> --yes`. Commands show login status and safe metadata, never token values.

See [Claude Code Profiles](../claude-code/profiles.md) for login requirements and verification.

## Antigravity

```bash
profiledeck-cli antigravity detect [--json]
profiledeck-cli antigravity profile list [--json]
profiledeck-cli antigravity profile show <profile-id> [--json]
profiledeck-cli antigravity profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli antigravity profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck-cli antigravity profile save-current [--json]
profiledeck-cli antigravity profile delete <profile-id> --yes [--json]
```

`agy` is an alias for `antigravity`. `create` and `save-current` require a valid current Antigravity consumer OAuth login. Output shows safe metadata and never prints login values.

See [Antigravity Profiles](../antigravity/profiles.md) for compatibility and switching advice.

## Preview and switch

```bash
profiledeck-cli switch --dry-run [--json] <provider-id> <profile-id>
profiledeck-cli switch --yes [--plan-fingerprint FINGERPRINT] [--json] <provider-id> <profile-id>
```

`switch --dry-run` is an optional read-only preview. Use `switch --yes` to apply the change. Pass the fingerprint returned by the preview when you want ProfileDeck to reject any state that changed after your review.

## Usage

```bash
profiledeck-cli usage sync codex [--codex-dir PATH] [--json]
profiledeck-cli usage sync grok-build [--json]
profiledeck-cli --grok-home PATH usage sync grok-build [--json]
profiledeck-cli usage summary [--provider codex|grok-build] [--json]
profiledeck-cli usage report [--provider codex|grok-build] [--range today|7d|30d|all] [--json]
```

Only local Codex and Grok Build usage is supported. The default Provider is Codex. `report` defaults to `7d`; `summary` gives a shorter all-time view. See [Codex Usage and Cost](../codex/usage-cost.md) or [Grok Build Usage and Cost](../grok-build/usage-cost.md) for report fields and estimation limits.

## Other tools and configuration files

The following commands are advanced CLI features for tools other than the built-in Codex, Claude Code, Antigravity, and Grok Build workflows. Use each built-in tool's dedicated commands above; generic target commands cannot manage their saved logins or settings.

```bash
profiledeck-cli provider list [--json]
profiledeck-cli provider show <id> [--json]
profiledeck-cli provider create <id> [--name NAME] [--adapter ID] [--metadata-json JSON] [--json]
profiledeck-cli provider update <id> [--name NAME] [--adapter ID] [--metadata-json JSON] [--json]
profiledeck-cli provider delete <id> --yes [--json]

profiledeck-cli profile list [--json]
profiledeck-cli profile show <id> [--json]
profiledeck-cli profile create <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck-cli profile update <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck-cli profile delete <id> --yes [--json]
```

Deleting a Provider removes all ProfileDeck data owned by it, including its settings, saved resources and bindings, file targets, current-Profile state, usage reports, and completed operation records. Global Profiles, Desktop Agent preferences, tool-owned working logins, settings, and files remain. Deletion stops while that Provider has an unfinished operation.

All five Profile delete commands perform the same global deletion. An Agent-specific command deletes the complete Profile even when it contains data only for another Agent. Deletion stops if the Profile is current in any Agent or has an unfinished operation. Saved logins and Config Sets used only by that Profile are deleted; shared saved data and unrelated unbound data remain. Completed operation records that refer to the Profile are also removed. Tool-owned working logins, settings, and files do not change.

Target commands:

```bash
profiledeck-cli profile target add <profile-id> <target-id> --provider ID --path PATH --format FORMAT --strategy STRATEGY --value-json JSON [--disabled] [--metadata-json JSON] [--json]
profiledeck-cli profile target list <profile-id> [--provider ID] [--all] [--json]
profiledeck-cli profile target show <profile-id> <provider-id> <target-id> [--json]
profiledeck-cli profile target update <profile-id> <provider-id> <target-id> [--path PATH] [--format FORMAT] [--strategy STRATEGY] [--value-json JSON] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck-cli profile target delete <profile-id> <provider-id> <target-id> --yes [--json]
```

See [Other Configuration Files](../guide/generic-targets.md) before adding a target.

## Backups, diagnostics, and recovery

```bash
profiledeck-cli backup create [--json]
profiledeck-cli backup list [--json]
profiledeck-cli backup show <backup-id> [--json]
profiledeck-cli backup export <backup-id> --output <file> [--json]
profiledeck-cli backup restore [<backup-id> | --file <file>] --yes [--json]
profiledeck-cli backup delete <backup-id> --yes
profiledeck-cli backup key status [--json]
profiledeck-cli backup key export --output <file> --yes [--json]
profiledeck-cli backup key import --file <file> [--replace] --yes [--json]
profiledeck-cli doctor [--json]
profiledeck-cli doctor repair-lock --yes [--json]
profiledeck-cli recover <operation-id> --yes [--json]
```

Application backups contain the complete ProfileDeck database but do not contain tool-owned working files or system credential-store entries. Export the recovery key separately before moving backups to another system. Replacing a different key requires both `--replace` and `--yes`, and backups encrypted to the old key will no longer open with the current key.

`recover` is only for an unfinished switch reported by Diagnostics. A successful switch cannot be undone. See [Diagnostics and Recovery](../operations/recovery.md) for the safe action in each state.
