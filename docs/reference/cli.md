# CLI Reference

All commands accept the global option:

```text
--config-dir string  Use a custom ProfileDeck config directory
```

## Root commands

| Command | Purpose |
| --- | --- |
| `backup` | View ProfileDeck backups. |
| `codex` | Manage Codex provider profiles. |
| `doctor` | Check local data, file permissions, and interrupted changes. |
| `init` | Create ProfileDeck's local data. |
| `plan` | Preview a Profile switch without changing files. |
| `provider` | Manage AI tool providers. |
| `profile` | Manage ProfileDeck profiles and targets. |
| `recover` | Restore files from the backup saved before a failed switch. |
| `rollback` | Undo an applied switch with its backup. |
| `status` | Show ProfileDeck setup status. |
| `switch` | Apply a profile switch. |
| `usage` | Import and analyze local token usage. |
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

The first `profile create` saves the current Codex login and settings and creates the `shared` Config Set. Later creates reuse the current Config Set unless `--new-config-set` is supplied. `fork` requires both sharing choices and at least one `copy-new`; copying settings also requires `--new-config-set`. `save-current` saves the current Codex login and settings, and `set-config` accepts only an inactive Profile.

`config-set create` saves the current `config.toml`. List and show commands return summaries only; they never expose complete sign-in data or TOML. Delete requires a Config Set that no Profile uses.

`profile export` creates a sensitive backup. With no Profile IDs it exports every Codex Profile and Config Set. With Profile IDs it exports only those Profiles and the logins and Config Sets they need. Run `save-current` first when the current Codex login or settings changed. `--output` is required so the backup can be kept outside a ProfileDeck data directory that will be deleted; `--force` is required to replace an existing file.

The backup contains complete Codex sign-in data and settings. ProfileDeck writes it with `0600` permissions on POSIX systems and never prints the sensitive contents to stdout. `import inspect` checks the backup and reports `create`, `unchanged`, and `conflict` actions. `import apply` requires the reviewed fingerprint and makes no changes when an existing ID has different content. Import does not make a Profile current or write Codex files.

## Switching

```bash
profiledeck plan [--json] <provider-id> <profile-id>
profiledeck switch [--yes] [--plan-fingerprint FINGERPRINT] [--json] <provider-id> <profile-id>
```

`switch` requires `--yes`.

## Usage

```bash
profiledeck usage sync codex [--codex-dir PATH] [--json]
profiledeck usage summary [--provider codex] [--json]
profiledeck usage report [--provider codex] [--range today|7d|30d|all] [--json]
```

Only local Codex usage is supported currently. `sync codex` is the manual entry point for CLI-only workflows; the Desktop app syncs automatically at its configured interval. `report` defaults to `7d`; human output prints the overall summary, time trend, and model statistics. JSON output also includes the resolved local-time range, import status, and pricing source. `summary` provides a shorter all-time view.

## Provider and profile CRUD

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

Generic target commands cannot create, update, or delete the files managed by Codex Profiles. Use the Codex commands above for saved logins and Config Sets.

## Backup and recovery

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
