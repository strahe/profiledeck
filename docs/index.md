# ProfileDeck

ProfileDeck saves local AI coding tool logins and settings as Profiles, then lets you review and apply a switch when you need a different setup.

## Choose how to use ProfileDeck

| Option | Best for | Start here |
| --- | --- | --- |
| macOS Desktop | Managing Profiles, usage, updates, and recovery in one app | [Download and open the Desktop app](./guide/getting-started.md#use-the-desktop-app) |
| CLI | Terminal workflows and automation from a source build | [Build and initialize the CLI](./guide/getting-started.md#build-and-use-the-cli) |

The Desktop Alpha requires macOS 14 or later on Apple silicon. The CLI requires Go 1.26 and Make when building from source.

## Supported tools

| Tool | What ProfileDeck switches | What stays unchanged |
| --- | --- | --- |
| Codex | A saved login and reusable user-level settings | Sessions, logs, skills, project settings, and system policy |
| Claude Code | An official subscription login | Claude Code settings, plugins, API keys, cloud providers, and Claude Desktop |
| Antigravity | The consumer OAuth login used by Antigravity agy v2 | Login flow, quotas, Manager data, and other Antigravity versions |

Codex usage reports are separate from Profile switching. They summarize local session data without assigning activity to a Profile or account.

## What happens when you switch

1. Review what will change. Login values remain hidden.
2. Confirm the switch. ProfileDeck checks the current files or login again.
3. ProfileDeck creates a backup before changing anything.
4. The selected Profile becomes current only after the change succeeds.

If a change does not finish, open Diagnostics or run `profiledeck doctor` before switching again.

## Continue

- [Get started](./guide/getting-started.md)
- [Understand Profiles, logins, and settings](./guide/concepts.md)
- [Manage Codex Profiles](./codex/profiles.md)
- [Manage Claude Code Profiles](./claude-code/profiles.md)
- [Manage Antigravity Profiles](./antigravity/profiles.md)
- [Review data and security](./reference/data-security.md)
