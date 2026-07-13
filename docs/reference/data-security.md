# Data and Security

ProfileDeck manages local files and hidden sign-in data for supported tools. Treat its data directory and backups as sensitive.

## Local data

The default data directory is:

```text
<os-user-config-dir>/profiledeck
```

It contains the application database, backups, exports, and operational files. `--config-dir` changes the user config directory used for this location.

`profiledeck.db` stores Profiles, Config Sets, saved Codex, Claude Code, and Antigravity logins, settings, usage reports, and operation history. Complete Codex `auth.json` and `config.toml` contents, Claude Code subscription login payloads, and Antigravity agy v2 login payloads may be stored because they are required to restore and switch Profiles.

Claude Code and Claude Desktop have separate product and credential boundaries. The Claude Code integration does not inspect, store, or change Claude Desktop credentials, configuration, or processes.

The database is not encrypted at rest. ProfileDeck restricts file permissions on POSIX systems when possible, but anyone who can read your local files may be able to read saved sign-in data.

Current account-limit information is temporary and is not stored in the database.

## Backups

Switch and rollback backups may contain previous tool files. Codex backups can include complete `auth.json` and `config.toml` contents.

For file targets, backup commands show file names, actions, hashes, and permissions without printing sensitive file contents. Keep the backup directory private.

Antigravity switch backups may contain the previous login in a private payload file. Public plan and backup summaries omit the system credential location, payload, and payload hashes.

Claude Code switch backups may contain the previous subscription login. On macOS, the private backup manifest also stores the persistent reference for the exact Keychain item. Public switch and backup DTOs, logs, errors, ordinary exports, and backup summaries omit that reference, the payload, and payload hashes.

## Sensitive Profile exports

`profiledeck codex profile export` creates a sensitive local backup. The JSON file contains complete Codex sign-in data and settings. Anyone with the file may be able to access your account.

ProfileDeck requires an explicit output path, refuses symbolic-link destinations, and writes the file with `0600` permissions on POSIX systems. It does not create or change the selected parent directory.

Import checks the backup and reports conflicts before making changes. When it attaches Codex bindings to an existing global Profile, it preserves that Profile's name and description. It does not make a Profile current, restore automatic-update settings, or write Codex files. Imported Profiles start with automatic limit refresh and sign-in renewal disabled.

Keep exported backups outside any ProfileDeck data directory you plan to delete. Do not commit or share them.

## Redaction

ProfileDeck hides sensitive-looking values in previews, normal command output, logs, errors, and result summaries. Codex sign-in previews are always fully hidden.

These commands show summaries only and do not print complete saved sign-in payloads or Config Set contents:

```bash
profiledeck codex profile list
profiledeck codex profile show <profile-id>
profiledeck codex config-set list
profiledeck codex config-set show <config-set-id>
profiledeck plan codex <profile-id>
profiledeck claude-code profile list
profiledeck claude-code profile show <profile-id>
profiledeck plan claude-code <profile-id>
profiledeck antigravity profile list
profiledeck antigravity profile show <profile-id>
profiledeck plan antigravity <profile-id>
profiledeck backup show <backup-id>
profiledeck doctor
```

Export and import command output also remains summary-only. Only the explicitly selected backup file contains the complete sign-in data and settings.

## Limit checks and sign-in renewal

Checking a Profile's limits contacts Codex or OpenAI using that Profile's saved sign-in. Profile-controlled model-provider URLs never receive the saved ChatGPT sign-in token.

Automatic limit refresh and sign-in renewal are off by default and run only while ProfileDeck is open or hidden in the tray. Desktop also checks the current Profile once at startup and does not repeat that check when you navigate between pages.

Supported managed logins may be renewed during a limit check. Some external sign-in methods can provide limits but cannot be renewed automatically. If automatic Codex updates are unavailable, a manual check may still work without changing the saved login.

Limit information is kept only while ProfileDeck is running. Limit checks do not display or record raw sign-in tokens or temporary file locations.

## Usage data ProfileDeck does not store

Usage reports store token counts, cost estimates, model names, and time information. ProfileDeck does not store raw Codex session records, prompts, completions, API keys, or full source paths as Usage data.

Usage reports contain aggregate results only. Local Codex activity cannot reliably identify which Profile or ChatGPT account served a request, so ProfileDeck does not guess or publish account-level usage.

Config Sets do not include skills, plugin caches, project `.codex/config.toml` files, or system policy.

ProfileDeck supports only Antigravity agy v2 consumer OAuth payloads. It does not perform Antigravity OAuth login, import Manager data, or expose account identity fields for matching.

ProfileDeck supports only the confirmed official Claude Code `claudeAiOauth` subscription shape. It does not perform Claude Code login and does not use OAuth user, organization, or subscription fields for identity, deduplication, merging, or overwrite decisions.
