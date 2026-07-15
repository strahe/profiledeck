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

The directory contains `profiledeck.db`, encrypted application backups, and temporary recovery material for unfinished switches. Codex, Claude Code, and Antigravity logins may be stored in the database or operation recovery material because ProfileDeck needs them to switch Profiles safely.

## Protect local data

ProfileDeck encrypts `.profiledeck-backup` files with age X25519. The live database and unfinished-switch recovery material are not separately encrypted, so anyone who can read your local files may be able to read saved logins. ProfileDeck restricts their file permissions where the operating system allows it.

- Use your operating system's full-disk encryption and screen lock.
- Do not sync, commit, upload, or share the complete ProfileDeck data directory.
- Use an exported encrypted application backup and separately exported recovery key when moving or reinstalling ProfileDeck.
- Keep recovery-key files outside repositories and shared folders.

Claude Code support is separate from Claude Desktop. ProfileDeck does not read or change Claude Desktop logins, settings, or processes.

## Understand application backups and operation recovery

Application backups contain the complete ProfileDeck database and are encrypted before they are published in `backups/`. Manual backups remain until you delete them. Automatic backups run every 24 hours and before update restart or database restore; the latest ten automatic backups are retained together.

The private X25519 recovery key is stored in the operating system credential store. ProfileDeck does not store it inside a backup. Export the key separately before moving backups to another system, and remember that replacing the current key does not re-encrypt existing files.

Before a switch changes an external tool, ProfileDeck creates a private recovery point under `recovery/<operation-id>/`. It may contain complete Codex files, a Claude Code subscription login, or an Antigravity login without application-backup encryption. It exists only for an unfinished switch and is deleted after success. It is not listed, exported, or usable to undo a successful switch.

Backup lists and previews show only safe metadata. Keep encrypted backup files private as defense in depth, and never share operation recovery material.

## Export a Codex Profile safely

`profiledeck codex profile export` creates an explicitly sensitive backup containing the selected Profile's complete Codex login and saved settings. Anyone with that file may be able to use the account.

Choose a private destination outside a repository or shared folder. Do not commit or share the export. Keep it outside any ProfileDeck data directory you plan to delete.

Import checks the file and reports conflicts before saving anything. It does not make the imported Profile current, change Codex files, or enable automatic limit refresh and sign-in renewal.

See [Codex Profiles](../codex/profiles.md#back-up-and-restore-profiles) for the export and import commands.

## Know when ProfileDeck connects to the internet

Most ProfileDeck actions use local data only.

- Usage sync and reports read local Codex session files and do not contact a billing service.
- Codex limit checks contact Codex or OpenAI with the selected saved login. That login is never sent to a custom model-service URL from the saved Codex settings. Limit results are temporary and are not added to usage reports.
- Antigravity limit checks send the current Antigravity access token to the fixed Google Cloud Code service used for the check. ProfileDeck does not refresh or save the token during a check. The result stays in app memory and is not added to the database, usage reports, exports, or backups.
- Desktop update checks and downloads contact the public ProfileDeck release on GitHub.

ProfileDeck does not provide cloud sync and does not send telemetry or analytics data. Automatic Codex limit refresh and sign-in renewal are off by default and run only while the Desktop app is open or in the menu bar.

## What output and usage reports omit

Normal previews, commands, logs, errors, and backup summaries hide saved login values and other sensitive-looking settings. Encrypted application backup exports copy ciphertext unchanged. Only a sensitive Codex Profile export that you explicitly create exposes complete login and settings data in its private bundle.

Usage reports store token counts, model names, time information, and cost estimates. They do not store raw prompts, raw completions, API keys, complete session records, or full source-file paths. Local Codex activity cannot reliably identify the Profile or ChatGPT account that served a request, so ProfileDeck does not guess that attribution.
