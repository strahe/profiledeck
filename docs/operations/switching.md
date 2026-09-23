# Review and Switch Profiles

A switch changes the selected tool's working login or settings. ProfileDeck checks their current state before writing, saves valid updates from the Profile you are leaving when supported, and makes the new Profile current only after the switch succeeds. Sensitive values remain hidden in the preview.

## Switch from the CLI

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

The preview is optional. For another tool, replace `codex` and `work` with its tool and Profile IDs. Use `--plan-fingerprint <fingerprint>` with `--yes` to require the state to match a previous preview. If it has changed, preview again.

ProfileDeck stops before writing if it cannot check the current state or create a recovery point. If a switch is interrupted or blocked, run `profiledeck-cli doctor` and follow [Diagnostics and Recovery](./recovery.md). Keep the [local data directory](../reference/data-security.md) private because unfinished-switch recovery files may contain logins.

A successful switch has no undo. Switch to another Profile if you need a different setup. A tool already running may need a new session to use its changed login.
