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
- resource bindings and redacted working-copy capture summaries
- plan fingerprint

Sensitive-looking values are redacted. Codex auth previews are fully redacted, and raw credential or Config Set payloads never appear in capture summaries.

For Codex, the fingerprint covers the source and destination bindings, target file hashes, and hashes of valid working-copy state waiting to be captured. A changed working copy therefore invalidates a previously reviewed plan.

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
3. Rebuild the plan from current database bindings and target files, staging valid Codex working-copy captures.
4. Verify target file hashes.
5. Create a backup checkpoint.
6. Verify hashes again.
7. Write changed files atomically.
8. Commit captures, active state, and the applied operation in one database transaction.

When a Codex binding is unchanged, its valid working copy is captured without rewriting the file. When a binding changes, the old valid working copy is staged before the target resource is materialized. A working copy that already matches the target resource is not assigned to the old binding. Missing, invalid, symlinked, or non-regular files are never silently stored; plans warn or block according to the safety risk.

If the operation fails, ProfileDeck keeps the failed operation record for `doctor` and `recover`.

## Backups

Every applied switch stores a backup manifest under the runtime backup directory. Backup manifests include paths, actions, hashes, modes, and relative backup entries. They do not include raw desired content in normal command output.

Codex backups may contain previous `auth.json` and `config.toml` content. Treat the backup directory as sensitive.

Rollback and recovery restore target files and previous active state. They do not undo valid credential or Config Set state already captured by an applied switch.
