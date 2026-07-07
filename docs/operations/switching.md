# Switching

Switching is the only normal path that writes target tool files.

## Preview

```bash
profiledeck plan codex work
profiledeck plan codex work --json
```

The plan is read-only. It includes:

- target path
- action: `create`, `update`, `noop`, or `unsupported`
- status reason
- before and desired SHA-256 hashes
- redacted previews
- plan fingerprint

Sensitive-looking values are redacted. Codex auth previews are fully redacted.

## Apply

```bash
profiledeck switch codex work --yes
```

For stricter apply, pass the fingerprint from a previous plan:

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

If the rebuilt plan does not match the expected fingerprint, the switch fails before writing targets.

## Safety pipeline

`switch` performs these steps:

1. Create a pending operation record.
2. Acquire the ProfileDeck switch lock.
3. Rebuild the plan from the current database and target files.
4. Verify target file hashes.
5. Create a backup checkpoint.
6. Verify hashes again.
7. Write changed files atomically.
8. Update active state and mark the operation applied.

If the operation fails, ProfileDeck keeps the failed operation record for `doctor` and `recover`.

## Backups

Every applied switch stores a backup manifest under the runtime backup directory. Backup manifests include paths, actions, hashes, modes, and relative backup entries. They do not include raw desired content in normal command output.

Codex backups may contain previous `auth.json` content. Treat the backup directory as sensitive.
