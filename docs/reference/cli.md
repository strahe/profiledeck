# CLI Reference

All commands accept the global option:

```text
--config-dir string  Use a custom ProfileDeck config directory
```

## Root commands

| Command | Purpose |
| --- | --- |
| `backup` | Inspect ProfileDeck backups. |
| `codex` | Manage Codex provider profiles. |
| `doctor` | Diagnose ProfileDeck runtime state. |
| `init` | Initialize the application store. |
| `plan` | Build a read-only switch plan. |
| `provider` | Manage AI tool providers. |
| `profile` | Manage ProfileDeck profiles and targets. |
| `recover` | Recover a failed switch from its backup checkpoint. |
| `rollback` | Roll back an applied switch backup. |
| `status` | Print application store status. |
| `switch` | Apply a profile switch. |
| `usage` | Import and summarize local token usage. |
| `version` | Print version information. |

## Core runtime

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
profiledeck codex profile create <profile-id> [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile fork <source-profile-id> <new-profile-id> --auth-binding share-parent|copy-new [--codex-dir PATH] [--name NAME] [--description TEXT] [--json]
profiledeck codex profile sync <profile-id> [--auth-update update-shared|fork-new] [--codex-dir PATH] [--json]
```

`create` reads the current Codex `config.toml` and `auth.json` and creates a profile with a new hidden credential. `fork` copies an existing profile and requires an explicit auth binding choice. `sync` updates an existing profile from current Codex files; use `--auth-update` when changing a shared credential or intentionally forking it.

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
```

Only Codex local session usage is supported currently.

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

## Backup and recovery

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
