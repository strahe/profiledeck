# Profiles, Logins, and Settings

A Profile names the login and settings you want to use for one supported tool. Saving a Profile does not immediately change that tool; files and system logins change only when you confirm a switch or recovery of an unfinished switch.

## What a Profile saves

| Tool | Saved login | Saved settings |
| --- | --- | --- |
| Codex | One Codex login | Saved Codex settings, called a Config Set |
| Claude Code | One official subscription login | Not included |
| Antigravity | One consumer OAuth login | Not included |

Each Profile has a permanent ID used by CLI commands and links. Profile IDs share one namespace across tools, and one Profile can contain saved data for more than one tool.

## Current Profile

ProfileDeck records one current Profile for each supported tool. The current Profile is the one represented by that tool's working login or files:

- Codex uses `auth.json` and `config.toml` in the active Codex home.
- Claude Code uses its official subscription login in Keychain on macOS or its credential file on Linux and Windows.
- Antigravity uses its current login in the system credential store.

Before leaving the current Profile, ProfileDeck preserves a valid refreshed login or valid Codex settings when it can do so safely. Missing, invalid, or unsupported content is reported instead of being saved silently.

## Saved logins

A saved login can be shared by more than one Profile. Updating a shared login changes every Profile that uses it, so Desktop shows the affected Profile count before saving.

ProfileDeck may show the final characters of a Codex Account ID to help distinguish logins. This value is display information only; it does not decide which login is updated or shared.

## Codex Config Sets

A Config Set is a reusable copy of the Codex settings in the user-level `config.toml`. The first Codex Profile creates one named `shared`. Later Profiles can reuse it or save a separate copy.

When Profiles share a Config Set, saving changed Codex settings updates all of them. Copy the Config Set when one Profile needs settings that can change independently. A Config Set can be deleted only when no Profile uses it.

Config Sets do not include sessions, logs, skills, plugin caches, project `.codex/config.toml` files, or system policy.

## What ProfileDeck changes

Creating, editing, forking, or importing a Profile changes only saved ProfileDeck data. Confirming a switch or unfinished-switch recovery may change the selected tool's working login or files.

Every such change is reviewed against the current tool state and gets a temporary operation recovery point first. See [Review and Switch](../operations/switching.md) for the normal flow and [Diagnostics and Recovery](../operations/recovery.md) when a change does not finish. Successful switches cannot be undone.

## Local data

Profiles, Config Sets, saved logins, preferences, usage reports, and operation history are stored in ProfileDeck's local data directory. Supported tools continue to own their working files and system credential entries.

Saved data, operation recovery points, and encrypted application backups may contain complete sign-in data. Read [Data and Security](../reference/data-security.md) before copying, exporting, or deleting ProfileDeck data.
