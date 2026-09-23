# Profiles, Logins, and Settings

A Profile names saved logins and settings. One Profile can contain data for several tools, but each tool has its own current Profile. Creating or editing a Profile changes only ProfileDeck's saved data; [switching](../operations/switching.md) changes the selected tool's working login or files.

Profile IDs are permanent and shared across tools. Saved logins can be shared by several Profiles. Updating one changes every Profile that uses it; ProfileDeck shows the affected count before saving.

## Config Sets

Codex and Grok Build also save user-level `config.toml` settings as Config Sets. Their Config Sets are separate. The first Profile uses `shared`, creating it from current settings when needed; later Profiles can reuse it or save a separate copy.

Changes to a shared Config Set affect every Profile using it. Copy it when settings must change independently. Config Sets do not include sessions, logs, plugins, project settings, or system policy.

## Delete a Profile

```bash
profiledeck-cli profile delete <profile-id> --yes
```

Deletion removes the complete Profile from every tool, including saved logins and Config Sets used only by that Profile. Shared saved data stays. A current Profile or one referenced by an unfinished switch cannot be deleted. Deletion does not sign out tools or change their working files.

## Local data

ProfileDeck stores Profiles, saved logins, settings, usage reports, and backups locally. Its live database and unfinished-switch recovery data may contain complete sign-in data. See [Local Data and Security](../reference/data-security.md) before copying or sharing these files.
