# Diagnostics, Backups, and Recovery

Open **Diagnostics** when Profile switching is blocked or a switch did not finish. Open **Settings → Backups** to protect or restore ProfileDeck's saved application data. These are separate workflows.

## Check an unfinished switch first

An unresolved switch blocks every new Profile switch and application-data restore. Resolve or safely close it in Diagnostics before starting either operation; ProfileDeck will not discard its recovery point to make another operation proceed.

In the Desktop app, Diagnostics shows only unresolved root switch operations and the safe action available for each one.

From the CLI, run:

```bash
profiledeck doctor
profiledeck doctor --json
```

If Diagnostics says no change is still running and offers to repair the switch lock, use its Desktop action or run:

```bash
profiledeck doctor repair-lock --yes
```

Do not repair a lock merely because a switch is taking longer than expected. ProfileDeck refuses recovery while the switch lock is held or when it cannot recognize every affected target safely.

## Retry recovery cleanup

ProfileDeck normally removes temporary operation recovery files after a switch, recovery, or application restore. If that cleanup cannot finish, Diagnostics shows **Temporary recovery files need cleanup**. Saved data, Doctor, and application backups remain available, but Profile switching and application restore pause until cleanup succeeds.

Use **Retry cleanup** in Diagnostics, or run:

```bash
profiledeck doctor retry-cleanup --yes
```

Cleanup removes only temporary operation recovery files that do not belong to an unresolved switch. It does not change any tool sign-in or setting. Close other ProfileDeck windows if the retry reports that another operation is running. If the warning remains, keep the data directory private and retry after resolving the reported filesystem problem.

## Resolve an unfinished switch

Diagnostics may offer one of two actions:

- **Close unfinished record** when ProfileDeck confirms that no target was changed or every target is already in its pre-switch state.
- **Restore pre-switch state** when every target still matches either its pre-switch or intended state.

Confirm the offered Desktop action, or use the operation ID shown by `doctor`:

```bash
profiledeck recover <operation-id> --yes
```

Recovery may restore tool-owned files or the selected system login and then restores the previously current Profile record. If a target was modified by another program, recovery metadata is damaged, or a target cannot be read, ProfileDeck refuses to write and reports what must be checked. A failed attempt can be retried against the same original switch.

Successful switches do not retain recovery files and cannot be undone. Choose the intended Profile and switch again if you want a different active setup.

## Create and manage application backups

An application backup contains the complete ProfileDeck database, including saved Profiles, settings, usage records, and credentials held in that database; the resulting archive is encrypted. It does not include external tool working files or entries from the operating system credential store.

Create and inspect backups with:

```bash
profiledeck backup create
profiledeck backup list
profiledeck backup show <backup-id>
profiledeck backup export <backup-id> --output <private-file>
```

When a newer ProfileDeck version needs to update existing local data, it first verifies the data and creates an encrypted automatic backup. If verification or backup creation fails, ProfileDeck stops before updating the data. If the update later fails, ProfileDeck stops and keeps the encrypted backup for restore. Close other ProfileDeck windows and retry; if the data remains unavailable, use the Desktop recovery screen to restore a known-good application backup.

Automatic backups are enabled by default. Desktop and Tray create one when the newest automatic backup is more than 24 hours old, and also before an update restart, a healthy-database restore, or a local-data update. ProfileDeck keeps up to ten automatic backups in total and up to three local-data update backups within that total. Manual backups remain until you delete them.

Backup files are encrypted with the recovery key stored in your system credential store. Export that key separately before moving a backup to another computer:

```bash
profiledeck backup key status
profiledeck backup key export --output <private-key-file> --yes
profiledeck backup key import --file <private-key-file> --yes
```

Keep the exported key private. Importing a different key requires `--replace --yes` and makes backups encrypted to the previous key unavailable until that previous key is imported again.

## Restore application data

Restore a managed or exported backup with:

```bash
profiledeck backup restore <backup-id> --yes
profiledeck backup restore --file <private-file> --yes
```

ProfileDeck verifies the encrypted archive and database before replacing current application data. When the current database is healthy, it first creates an automatic safety backup. A damaged current database can be replaced after confirmation without that safety backup.

Restore clears every current-Profile marker and closes unresolved operations so restored history cannot be mistaken for current external tool state. It does not change any tool-owned file or system login and does not apply a Profile. Desktop restarts after success; from the CLI, restart ProfileDeck and explicitly switch to the Profile you want. CLI restore is refused while Desktop or another ProfileDeck process is using the application data.

After restore commits, ProfileDeck removes obsolete operation recovery files. If that cleanup cannot finish, the restored data remains active and Desktop retries during restart. Switching and another application restore stay paused until cleanup succeeds.

If ProfileDeck cannot open its database at startup, the Desktop recovery screen still lets you import the recovery key, list available backups, and restore one.
