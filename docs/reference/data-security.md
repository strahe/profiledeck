# Local Data and Security

ProfileDeck keeps Profiles, saved logins, settings, usage reports, backups, and operation history on your device. Treat its data directory as sensitive.

## Find the data directory

The default location is:

```text
<user-config-directory>/profiledeck
```

Common examples are:

| System | Default location |
| --- | --- |
| macOS | `~/Library/Application Support/profiledeck` |
| Linux | `$XDG_CONFIG_HOME/profiledeck` or `~/.config/profiledeck` |
| Windows | `%AppData%\profiledeck` |

If you pass `--config-dir <directory>`, ProfileDeck uses `<directory>/profiledeck` instead.

The directory contains `profiledeck.db` (and SQLite WAL sidecars `profiledeck.db-wal` / `profiledeck.db-shm` when present), encrypted application backups, and temporary recovery material for unfinished switches. Codex, Claude Code, Antigravity, and Grok Build logins may be stored in the database or operation recovery material because ProfileDeck needs them to switch Profiles safely. Saved Grok Build Config Sets may contain the complete local `config.toml`.

## Protect local data

ProfileDeck encrypts `.profiledeck-backup` files with age X25519. The live database and unfinished-switch recovery material are not separately encrypted, so anyone who can read your local files may be able to read saved logins. On macOS and Linux, ProfileDeck restricts its own private files and directories where possible. Desktop Diagnostics and `profiledeck-cli doctor` report ProfileDeck, Codex, Grok Build, or Claude Code paths whose permissions may allow access by other local users. These checks do not block startup or Profile switching, and ProfileDeck does not change files owned by those tools during a check.

- Use your operating system's full-disk encryption and screen lock.
- Do not sync, commit, upload, or share the complete ProfileDeck data directory.
- Use an exported encrypted application backup and separately exported recovery key when moving or reinstalling ProfileDeck.
- Keep recovery-key files outside repositories and shared folders.

Claude Code support is separate from Claude Desktop. ProfileDeck does not read or change Claude Desktop logins, settings, or processes.

## Understand application backups and operation recovery

Application backups contain the complete ProfileDeck database and are encrypted before they are published in `backups/`. ProfileDeck also creates an encrypted backup before updating existing local data to a newer format. If verification or backup creation fails, ProfileDeck stops before updating the data. If the update later fails, startup stops and keeps the encrypted backup available from the recovery screen.

Manual backups remain until you delete them. Automatic backups run every 24 hours and before update restart, database restore, or a local-data update. ProfileDeck keeps up to ten automatic backups in total and up to three local-data update backups within that total.

The private X25519 recovery key is stored in the operating system credential store. ProfileDeck does not store it inside a backup. Export the key separately before moving backups to another system, and remember that replacing the current key does not re-encrypt existing files.

Before a switch changes an external tool, ProfileDeck creates a private recovery point under `recovery/<operation-id>/`. It may contain complete Codex or Grok Build files, a Claude Code account login, or an Antigravity login without application-backup encryption. It exists only for an unfinished switch and is deleted after success. It is not listed, exported, or usable to undo a successful switch.

ProfileDeck records a cleanup obligation before an operation becomes authoritative, then clears it only after the recovery directory has been synchronized. A crash or filesystem error can therefore leave completed-operation material visible as a cleanup warning. While that warning is active, Profile switching and application restore pause, but reads, Doctor, and application backups remain available. Run `profiledeck-cli doctor retry-cleanup --yes` or use **Retry cleanup** in Desktop Diagnostics. The cleanup does not change tool sign-ins or settings.

Backup lists and previews show only safe metadata. Keep encrypted backup files private as defense in depth, and never share operation recovery material.

## Know when ProfileDeck connects to the internet

Most ProfileDeck actions use local data only.

- Usage sync and reports read local Codex or Grok Build session files and do not contact a billing service.
- Usage price checks download the public ProfileDeck price list from GitHub. The Desktop app checks at most once a day while running; `usage sync` checks first when due. No local usage is sent. Turn automatic checks off in **Settings → Usage prices** or with `profiledeck-cli usage pricing auto off`.
- ChatGPT Codex limit checks contact Codex or OpenAI with the selected saved login. That login is never sent to a custom model-service URL from the saved Codex settings.
- API Key limit checks send the saved API Key to `/v1/usage` on the custom Base URL configured by that Profile. ProfileDeck makes one request without redirects. An HTTP Base URL does not encrypt the API Key or response in transit. Limit results stay in memory and are not added to usage reports or application backups.
- Grok Build credits checks use the current managed Profile and follow the installed Grok Build app's network and sign-in settings. Grok may renew its current sign-in during the check. ProfileDeck does not query inactive Profiles, and the result remains in memory instead of being added to the database, usage reports, or application backups.
- Antigravity limit checks send the current Antigravity access token to a fixed, unpublished Google Cloud Code service. Using this service may carry account risk. ProfileDeck does not refresh, save, or write back the token during a check. The result stays in app memory and is not added to the database, usage reports, or application backups.
- Desktop update checks and downloads contact the public ProfileDeck release on GitHub.

ProfileDeck does not provide cloud sync and does not send telemetry or analytics data. Automatic Codex limit refresh and sign-in renewal are off by default and run only while the Desktop app is open or in the menu bar.

## What output and usage reports omit

Normal previews, commands, logs, errors, and backup summaries hide saved login values and other sensitive-looking settings. Exported application backups remain encrypted; recovery-key exports are separate sensitive files that must be kept private.

Usage reports store token counts, model names, time information, derived session identifiers, and cost estimates. They do not store raw prompts, raw completions, agent results, API keys, direct session identifiers, complete session records, or full source-file paths. Local Codex and Grok Build activity cannot reliably identify the Profile, saved login, or account that served a request, so ProfileDeck does not guess that attribution.
