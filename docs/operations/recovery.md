# Recovery

Use Diagnostics when a switch or rollback does not finish, Profile switching is blocked, or local data needs attention.

## Check Diagnostics

```bash
profiledeck doctor
profiledeck doctor --json
```

Diagnostics checks:

- whether ProfileDeck can read its local data;
- changes that did not finish or failed;
- whether another ProfileDeck change may still be running;
- whether sensitive local files are private.

Desktop shows the same issues in user-facing language and offers an action only when it is safe to continue.

## Restore Profile switching

```bash
profiledeck doctor repair-lock --yes
```

Use this only when Diagnostics says Profile switching can be restored safely. ProfileDeck refuses the command when another change may still be running or the situation cannot be verified.

## Recover a failed switch

```bash
profiledeck recover <switch-operation-id> --yes
```

Recovery uses the backup saved before the failed switch. Use it for an interrupted or failed switch, not as a normal undo action.

## Roll back a successful switch

```bash
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck rollback <backup-id> --yes
```

Rollback restores the external targets and selected Profile from a backup. ProfileDeck also backs up the current target state before restoring the older state. This applies to Codex files and Antigravity system credentials.
