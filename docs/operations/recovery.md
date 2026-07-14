# Diagnostics and Recovery

Open Diagnostics when a switch or rollback does not finish, Profile switching is blocked, or ProfileDeck reports a local-data problem.

## Check the problem first

In the Desktop app, open **Diagnostics** and review the recommended action.

From the CLI, run:

```bash
profiledeck doctor
profiledeck doctor --json
```

Diagnostics checks whether ProfileDeck can read its local data, whether an operation failed or did not finish, whether another change may still be running, and whether sensitive local files are private.

## Restore Profile switching

If Diagnostics says no change is still running and offers to restore switching, use its Desktop action or run:

```bash
profiledeck doctor repair-lock --yes
```

Do not use this command merely because a switch is taking longer than expected. ProfileDeck refuses it when the situation cannot be verified safely.

## Recover a failed switch

When Desktop Diagnostics shows a failed switch with a usable backup, choose **Recover** and confirm.

From the CLI, run `profiledeck doctor`. For a failed switch reported as recoverable, use the identifier shown for that switch:

```bash
profiledeck recover <failed-switch-id> --yes
```

Recovery restores the files or login and the previously current Profile from the backup created before that switch. It supports Codex, Claude Code, and Antigravity. Use rollback, not recovery, to undo a switch that completed successfully.

## Undo a successful switch

List and inspect backups:

```bash
profiledeck backup list
profiledeck backup show <backup-id>
```

Then restore the backup you want:

```bash
profiledeck rollback <backup-id> --yes
```

Rollback supports Codex, Claude Code, and Antigravity. Before restoring the older state, ProfileDeck creates another backup of the current state. If the rollback succeeds, it restores both the selected tool and the Profile that was current when the chosen backup was created.

## Choose the right action

| Situation | Action |
| --- | --- |
| Diagnostics says switching is blocked but no change is running | Restore Profile switching |
| A failed switch has a usable backup | Choose **Recover** in Diagnostics or run the CLI recovery command |
| A switch completed but you want the previous state | Roll back its backup |

If Diagnostics does not offer a safe action or no usable backup exists, do not remove ProfileDeck data or backups manually. Preserve the ProfileDeck data directory and review the reported error before trying again.
