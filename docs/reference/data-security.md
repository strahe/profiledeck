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

The directory contains ProfileDeck's saved data, switch and update backups, and files needed to complete or recover changes. Codex, Claude Code, and Antigravity logins may be stored there because ProfileDeck needs them to switch and restore Profiles.

## Protect local data

ProfileDeck does not add its own encryption to saved data or backups. It restricts their file permissions where the operating system allows it, but anyone who can read your local files may be able to read saved logins.

- Use your operating system's full-disk encryption and screen lock.
- Do not sync, commit, upload, or share the ProfileDeck data directory.
- Back up the entire directory before moving or reinstalling ProfileDeck.
- Keep backups until you have confirmed that your Profiles switch correctly.

Claude Code support is separate from Claude Desktop. ProfileDeck does not read or change Claude Desktop logins, settings, or processes.

## Understand switch and update backups

Every switch and rollback creates a private backup before changing the selected tool. A backup may contain complete Codex files, a Claude Code subscription login, or an Antigravity login.

Before installing a Desktop update, ProfileDeck also backs up its local data and keeps the three newest update backups. If update verification or installation fails, the current version remains available.

Backup lists and previews hide sensitive contents, but the backup files themselves must remain private.

## Export a Codex Profile safely

`profiledeck codex profile export` creates an explicitly sensitive backup containing the selected Profile's complete Codex login and saved settings. Anyone with that file may be able to use the account.

Choose a private destination outside a repository or shared folder. Do not commit or share the export. Keep it outside any ProfileDeck data directory you plan to delete.

Import checks the file and reports conflicts before saving anything. It does not make the imported Profile current, change Codex files, or enable automatic limit refresh and sign-in renewal.

See [Codex Profiles](../codex/profiles.md#back-up-and-restore-profiles) for the export and import commands.

## Know when ProfileDeck connects to the internet

Most ProfileDeck actions use local data only.

- Usage sync and reports read local Codex session files and do not contact a billing service.
- Codex limit checks contact Codex or OpenAI with the selected saved login. That login is never sent to a custom model-service URL from the saved Codex settings. Limit results are temporary and are not added to usage reports.
- Desktop update checks and downloads contact the public ProfileDeck release on GitHub.

ProfileDeck does not provide cloud sync and does not send telemetry or analytics data. Automatic Codex limit refresh and sign-in renewal are off by default and run only while the Desktop app is open or in the menu bar.

## What output and usage reports omit

Normal previews, commands, logs, errors, and backup summaries hide saved login values and other sensitive-looking settings. Only a sensitive Codex export that you explicitly create contains complete exported login and settings data.

Usage reports store token counts, model names, time information, and cost estimates. They do not store raw prompts, raw completions, API keys, complete session records, or full source-file paths. Local Codex activity cannot reliably identify the Profile or ChatGPT account that served a request, so ProfileDeck does not guess that attribution.
