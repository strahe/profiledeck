# CLI Reference

Run `profiledeck-cli --help` or `profiledeck-cli <command> --help` for the complete syntax supported by your installed version. Put global options before the command:

- `--config-dir <directory>` uses `<directory>/profiledeck` for application data.
- `--grok-home <directory>` selects a Grok Build Home instead of `GROK_HOME` or `~/.grok`.

Replace values in angle brackets with your own IDs or paths.

Add `--json` to a command that offers it when a script needs structured output.

## Setup and Profiles

```bash
profiledeck-cli init
profiledeck-cli status
profiledeck-cli codex detect
profiledeck-cli codex profile create work
profiledeck-cli codex profile list
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

Replace `codex` with `claude-code`, `antigravity`, or `grok-build` for that tool's Profile commands. Each tool also offers `profile show`, `profile save-current`, and `profile delete`. Deleting a Profile removes it from every tool; see [Profiles and settings](../guide/concepts.md). A switch preview is optional.

Codex and Grok Build additionally offer `config-set` management, `profile set-config`, and `profile fork` for sharing or copying logins and settings. Their [Codex](../codex/profiles.md) and [Grok Build](../grok-build/profiles.md) pages show practical examples.

## Usage and prices

```bash
profiledeck-cli usage sync codex
profiledeck-cli usage sync grok-build
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage report --provider grok-build --range 30d
profiledeck-cli usage pricing check
profiledeck-cli usage pricing auto off
```

Use `--provider codex` or `--provider grok-build` for reports; the default is Codex. Report ranges are `today`, `7d`, `30d`, and `all`. See [Codex](../codex/usage-cost.md) or [Grok Build](../grok-build/usage-cost.md) for estimate limits.

## Backups and recovery

```bash
profiledeck-cli doctor
profiledeck-cli backup create
profiledeck-cli backup list
profiledeck-cli backup export <backup-id> --output <private-file>
profiledeck-cli backup key export --output <private-key-file> --yes
profiledeck-cli backup restore <backup-id> --yes
```

Backups are encrypted, but the key must be moved separately. Use `profiledeck-cli backup key import --file <private-key-file> --yes` on the destination computer. Restore does not change tool-owned files or sign-ins. Use `recover <operation-id> --yes`, `doctor repair-lock --yes`, or `doctor retry-cleanup --yes` only when [Diagnostics](../operations/recovery.md) calls for that action.

## Other configuration files

`provider` and `profile target` commands manage advanced local file switching for other tools. They cannot change the managed logins or settings of Codex, Claude Code, Antigravity, or Grok Build. See [Switch Other Configuration Files](../guide/generic-targets.md) for a working example.
