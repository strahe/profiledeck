# Diagnostics, Backups, and Recovery

Desktop shows available recovery actions for blocked or interrupted switches in **Diagnostics**. An unfinished switch blocks new switches and application restore until it is resolved.

## Resolve an unfinished switch

```bash
profiledeck-cli doctor
profiledeck-cli recover <operation-id> --yes
```

Use the operation ID and action offered by Diagnostics. Recovery may restore a tool's working files or system login, but does not change the current Profile. If a target changed outside ProfileDeck or cannot be checked safely, recovery stops without writing. A failed recovery can be retried after resolving the reported problem.

Use `profiledeck-cli doctor repair-lock --yes` only when Diagnostics confirms that no switch is running and offers lock repair. If it reports **Temporary recovery files need cleanup**, run `profiledeck-cli doctor retry-cleanup --yes`. Cleanup does not change tool logins or settings; switching and application restore remain blocked until it succeeds.

## Back up ProfileDeck data

An application backup is an encrypted copy of the complete ProfileDeck database. It includes saved Profiles, settings, usage, and credentials held in that database, but excludes tool-owned working files and system credential-store entries.

Desktop can manage and restore backups in **Settings → Backups**.

```bash
profiledeck-cli backup create
profiledeck-cli backup list
profiledeck-cli backup export <backup-id> --output <private-file>
```

Automatic backups are on by default and run roughly daily while Desktop or Tray is active. Additional backups are made before updates, healthy-database restores, and local-data upgrades; up to ten automatic backups are retained. Manual backups remain until you delete them.

The recovery key lives in the system credential store, outside the backup. Export it separately before moving a backup to another computer:

```bash
profiledeck-cli backup key export --output <private-key-file> --yes
profiledeck-cli backup key import --file <private-key-file> --yes
```

Keep the key file private. Replacing the current key with `--replace --yes` does not re-encrypt older backups; import the old key again to open them.

## Restore application data

```bash
profiledeck-cli backup restore <backup-id> --yes
profiledeck-cli backup restore --file <private-file> --yes
```

Restore verifies the backup before replacing ProfileDeck data. When the current database is healthy, ProfileDeck first makes a safety backup. A damaged database may be restored after confirmation without that backup.

After restore, no Profile is current and any unfinished switches in the backup are closed. Tool working files and system logins remain unchanged.

Restart ProfileDeck after a CLI restore, then explicitly switch to the Profile you need. Close other ProfileDeck processes before restoring from the CLI.

If the database cannot open at startup, Desktop can still restore a backup. If ProfileDeck reports an unsupported local data format, restore a compatible backup. To start with new data instead, close ProfileDeck and move the entire [data directory](../reference/data-security.md) to a private location before reopening it. Keep that directory for later inspection.
