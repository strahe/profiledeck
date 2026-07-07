# Recovery

ProfileDeck records switch and rollback operations so interrupted writes can be inspected and recovered.

## Diagnose

```bash
profiledeck doctor
profiledeck doctor --json
```

`doctor` reports:

- database initialization and schema health
- pending and failed operations
- switch lock status
- stale lock repair eligibility
- sensitive path permission warnings

## Repair a stale lock

```bash
profiledeck doctor repair-lock --yes
```

Only clearly stale lock files are repairable. If the lock owner still appears active or the lock cannot be verified, repair is rejected.

## Recover a failed switch

```bash
profiledeck recover <switch-operation-id> --yes
```

Recovery uses the failed switch operation's backup checkpoint. It is intended for incomplete switches, not for normal undo.

## Roll back an applied switch

```bash
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck rollback <backup-id> --yes
```

Rollback restores target files from a backup and updates ProfileDeck active state and operation history. It also creates its own backup of the current state before restoring.
