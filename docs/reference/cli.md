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
| `usage` | Import and analyze local token usage. |
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

The first `profile create` captures current Codex files into a hidden credential and the `shared` Config Set. Later creates reuse the active Config Set unless `--new-config-set` is supplied. `fork` requires both binding choices and at least one `copy-new`; copying config also requires `--new-config-set`. `save-current` captures both active working copies, and `set-config` accepts only an inactive Profile.

`config-set create` captures the current `config.toml`. List and show commands return summaries only; they never expose raw auth or raw TOML. Delete requires an unreferenced Config Set.

`profile export` is an explicit sensitive backup. With no Profile IDs it exports every Codex Profile, every referenced hidden credential, and all Config Sets, including unreferenced sets. With Profile IDs it exports only those Profiles and their dependency closure. Run `save-current` first when the active working copies changed. `--output` is required so the bundle can be kept outside a runtime directory that will be deleted; `--force` is required to replace an existing file.

The bundle contains raw `auth.json` and complete `config.toml` payloads. ProfileDeck writes it atomically with `0600` permissions on POSIX systems and never prints those payloads to stdout. `import inspect` validates the bundle and reports `create`, `unchanged`, and `conflict` actions. `import apply` requires the reviewed fingerprint and rejects every differing same-ID conflict without partial writes. Import restores database resources only: it does not set active state or write Codex working files.

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

Only Codex local session usage is supported currently. `sync codex` remains the manual entry point for CLI-only workflows; the Desktop app syncs automatically at its configured interval. `report` defaults to `7d`; human output prints the aggregate summary, time trend, and model statistics in that order. JSON output includes the resolved local-time range, import health, and static pricing provenance. Existing `summary` output remains the lightweight all-time contract.

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

Generic target CRUD cannot create, update, or delete Codex preset targets. Use the Codex commands above for credential and Config Set bindings.

## Backup and recovery

```bash
profiledeck backup list [--json]
profiledeck backup show <backup-id> [--json]
profiledeck doctor [--json]
profiledeck doctor repair-lock --yes [--json]
profiledeck recover <switch-operation-id> --yes [--json]
profiledeck rollback <backup-id> --yes [--json]
```
