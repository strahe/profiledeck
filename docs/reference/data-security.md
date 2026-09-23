# Local Data and Security

ProfileDeck stores Profiles, logins, settings, usage reports, and backups on your device. Treat its data directory as sensitive.

## Find the data directory

| System | Default location |
| --- | --- |
| macOS | `~/Library/Application Support/profiledeck` |
| Linux | `$XDG_CONFIG_HOME/profiledeck` or `~/.config/profiledeck` |
| Windows | `%AppData%\profiledeck` |

`--config-dir <directory>` uses `<directory>/profiledeck` instead. The directory contains `profiledeck.db`, encrypted application backups, and recovery files for unfinished switches.

## Protect saved data

Application backups are encrypted with age X25519. The live database and unfinished-switch recovery files are not separately encrypted and may contain complete logins or settings. Do not sync, commit, upload, or share the data directory. Use full-disk encryption and a screen lock. `profiledeck-cli doctor` can report file permissions that allow other local users access.

The private backup recovery key is stored in your system credential store, not in a backup. Export it separately before moving backups to another computer, and keep exported key files out of shared folders. See [Backups and Recovery](../operations/recovery.md) for commands.

## Network access

- Usage reports read local Codex and Grok Build sessions. Price-list checks download public rates from GitHub; local usage is not uploaded. Disable automatic checks with `profiledeck-cli usage pricing auto off`.
- ChatGPT Codex limit checks contact Codex or OpenAI with the selected login. API Key limit checks send the saved key to the Profile's custom Base URL; HTTP does not encrypt it in transit.
- Grok Build credits checks use the installed Grok Build app and may renew its current login.
- Antigravity limit checks send the current access token to an unpublished Google Cloud Code service and may carry account risk. They do not refresh or write back the token.
- Desktop update checks and downloads contact ProfileDeck releases on GitHub.

Limit and credits results remain in memory. ProfileDeck does not provide cloud sync or send telemetry.

## Output and usage reports

Previews, commands, logs, errors, and backup summaries hide saved login values and sensitive settings. Exported backups remain encrypted; exported recovery keys are separate sensitive files.

Usage reports store token counts, models, dates, and estimates, but not raw prompts, completions, agent results, API keys, complete session records, or full source-file paths. They cannot reliably attribute historical activity to a Profile, saved login, or account.
