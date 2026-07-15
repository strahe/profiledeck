# CLI Reference

Use this page for command names and common options. Run `profiledeck --help` or `profiledeck <command> --help` for the exact help included with your installed version; installed help takes precedence if it differs from this page.

Angle brackets mark required values. Square brackets mark optional arguments.

## Global option

Every command accepts:

```text
--config-dir string  Use a custom ProfileDeck config directory
```

This value is the parent config directory. ProfileDeck creates or uses its `profiledeck` folder below it.

## Commands

| Command | Use it to |
| --- | --- |
| `antigravity` | Save and manage Antigravity Profiles. |
| `backup` | Create, export, restore, and manage encrypted application backups. |
| `claude-code` | Save and manage official Claude Code subscription Profiles. |
| `codex` | Manage Codex Profiles and saved settings (Config Sets). |
| `doctor` | Diagnose local-data, permission, and interrupted-operation problems. |
| `init` | Create ProfileDeck's local data. |
| `plan` | Preview a Profile switch without changing the selected tool. |
| `provider` | Configure another AI tool for advanced file switching. |
| `profile` | Manage Profiles and advanced file targets. |
| `recover` | Resolve an interrupted or failed switch. |
| `status` | Check whether ProfileDeck is initialized. |
| `switch` | Apply a Profile switch. |
| `usage` | Import and report local Codex usage. |
| `version` | Print version information. |

## Setup and status

```bash
profiledeck init [--json]
profiledeck status [--json]
profiledeck version
```

## Codex

```bash
profiledeck codex detect [--codex-dir PATH] [--json]
profiledeck codex profile list [--json]
profiledeck codex profile show <profile-id> [--json]
profiledeck codex profile create <profile-id> [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile fork <source-profile-id> <new-profile-id> --credential-binding share-parent|copy-new --config-binding share-parent|copy-new [--new-config-set ID] [--config-set-name NAME] [--config-set-description TEXT] [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile save-current [--codex-dir PATH] [--json]
profiledeck codex profile set-config <profile-id> <config-set-id> [--json]
profiledeck codex profile export [<profile-id> ...] --output PATH [--force] [--json]
profiledeck codex profile import inspect <bundle-path> [--codex-dir PATH] [--json]
profiledeck codex profile import apply <bundle-path> --plan-fingerprint FINGERPRINT --yes [--codex-dir PATH] [--json]

profiledeck codex config-set list [--json]
profiledeck codex config-set show <config-set-id> [--json]
profiledeck codex config-set create <config-set-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set copy <source-id> <new-id> [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set update <config-set-id> [--name NAME] [--description TEXT] [--json]
profiledeck codex config-set delete <config-set-id> --yes [--json]
```

The first `profile create` saves the current Codex login and settings and creates the `shared` Config Set. Later creates reuse the current Config Set unless you pass `--new-config-set`.

`fork` requires choices for both the login and Config Set, and at least one choice must be `copy-new`. Copying settings also requires `--new-config-set`. `save-current` saves the login and settings currently used by Codex. `set-config` changes only a Profile that is not current.

`config-set create` saves the current `config.toml`. List and show commands return safe summaries. You can delete only a Config Set that no Profile uses.

`profile export` creates a sensitive backup. Without Profile IDs, it exports every Codex Profile and Config Set. With IDs, it exports only the selected Profiles and the data they need. Use `--force` to replace an existing output file.

Run `import inspect` first. Review what will be added, what already matches your saved data, and any conflicts, then pass the returned fingerprint to `import apply`. Import stops without changes when existing Codex data conflicts. It does not make a Profile current or change Codex files.

See [Codex Profiles](../codex/profiles.md) for task-based examples and safety guidance.

## Claude Code

```bash
profiledeck claude-code detect [--json]
profiledeck claude-code profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck claude-code profile list [--json]
profiledeck claude-code profile show <profile-id> [--json]
profiledeck claude-code profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck claude-code profile save-current [--yes] [--json]
```

`create` saves the current official Claude Code subscription login and makes the new Profile current. `save-current` updates the login used by the current Profile. If that saved login is shared, the command reports how many Profiles will change and requires `--yes`.

There is no `claude` alias. Switch with `profiledeck plan claude-code <profile-id>` and `profiledeck switch claude-code <profile-id> --yes`. Commands show login status and safe metadata, never token values.

See [Claude Code Profiles](../claude-code/profiles.md) for login requirements and verification.

## Antigravity

```bash
profiledeck antigravity detect [--json]
profiledeck antigravity profile list [--json]
profiledeck antigravity profile show <profile-id> [--json]
profiledeck antigravity profile create <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck antigravity profile update <profile-id> [--name NAME] [--description TEXT] [--json]
profiledeck antigravity profile save-current [--json]
```

`agy` is an alias for `antigravity`. `create` and `save-current` require a valid current Antigravity consumer OAuth login. Output shows safe metadata and never prints login values.

See [Antigravity Profiles](../antigravity/profiles.md) for compatibility and switching advice.

## Preview and switch

```bash
profiledeck plan [--json] <provider-id> <profile-id>
profiledeck switch [--yes] [--plan-fingerprint FINGERPRINT] [--json] <provider-id> <profile-id>
```

`plan` is read-only. `switch` requires `--yes`. Pass the fingerprint returned by `plan` when you want ProfileDeck to reject any state that changed after your review.

## Usage

```bash
profiledeck usage sync codex [--codex-dir PATH] [--json]
profiledeck usage summary [--provider codex] [--json]
profiledeck usage report [--provider codex] [--range today|7d|30d|all] [--json]
```

Only local Codex usage is supported. `report` defaults to `7d`; `summary` gives a shorter all-time view. See [Codex Usage and Cost](../codex/usage-cost.md) for report fields and estimation limits.

## Other tools and configuration files

The following commands are advanced CLI features for tools other than the built-in Codex, Claude Code, and Antigravity workflows. Use each built-in tool's dedicated commands above; generic target commands cannot manage their saved logins or settings.

```bash
profiledeck provider list [--all] [--json]
profiledeck provider show <id> [--json]
profiledeck provider create <id> [--name NAME] [--adapter ID] [--disabled] [--metadata-json JSON] [--json]
profiledeck provider update <id> [--name NAME] [--adapter ID] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck provider delete <id> --yes [--json]

profiledeck profile list [--json]
profiledeck profile show <id> [--json]
profiledeck profile create <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck profile update <id> [--name NAME] [--description TEXT] [--metadata-json JSON] [--json]
profiledeck profile delete <id> --yes [--json]
```

Target commands:

```bash
profiledeck profile target add <profile-id> <target-id> --provider ID --path PATH --format FORMAT --strategy STRATEGY --value-json JSON [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target list <profile-id> [--provider ID] [--all] [--json]
profiledeck profile target show <profile-id> <provider-id> <target-id> [--json]
profiledeck profile target update <profile-id> <provider-id> <target-id> [--path PATH] [--format FORMAT] [--strategy STRATEGY] [--value-json JSON] [--enabled] [--disabled] [--metadata-json JSON] [--json]
profiledeck profile target delete <profile-id> <provider-id> <target-id> --yes [--json]
```

See [Other Configuration Files](../guide/generic-targets.md) before adding a target.

## Backups, diagnostics, and recovery

```bash
profiledeck backup create [--json]
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck backup export <backup-id> --output <file> [--json]
profiledeck backup restore [<backup-id> | --file <file>] --yes [--json]
profiledeck backup delete <backup-id> --yes
profiledeck backup key status [--json]
profiledeck backup key export --output <file> --yes [--json]
profiledeck backup key import --file <file> [--replace] --yes [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <operation-id> --yes [--json]
```

Application backups contain the complete ProfileDeck database but do not contain tool-owned working files or system credential-store entries. Export the recovery key separately before moving backups to another system. Replacing a different key requires both `--replace` and `--yes`, and backups encrypted to the old key will no longer open with the current key.

`recover` is only for an unfinished switch reported by Diagnostics. A successful switch cannot be undone. See [Diagnostics and Recovery](../operations/recovery.md) for the safe action in each state.
