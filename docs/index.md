# ProfileDeck

ProfileDeck saves local AI coding tool logins and settings as Profiles, then lets you review and apply a switch when you need a different setup.

## Choose how to use ProfileDeck

| Option | Best for | Start here |
| --- | --- | --- |
| macOS Desktop | Managing Profiles, updates, and recovery in one app | [Install the Desktop app on macOS](./guide/getting-started.md#install-the-desktop-app-on-macos) |
| Linux amd64 package | Desktop and CLI together | [Install a DEB or RPM](./guide/getting-started.md#deb-or-rpm-package) |
| Linux portable Desktop | Desktop only; in-app updates in a user directory | [Portable Desktop](./guide/getting-started.md#portable-desktop) |
| CLI source build | Terminal workflows and automation | [Build and use the CLI](./guide/getting-started.md#build-and-use-the-cli) |

The Universal Desktop app requires macOS 14 or later and runs natively on Apple silicon and Intel Macs. Linux packages and portable Desktop installs require amd64 plus GTK 4 and WebKitGTK 6.0. The CLI requires Go 1.26 and Make when building from source.

## Supported tools

| Tool | What ProfileDeck switches | What stays unchanged |
| --- | --- | --- |
| Codex | A saved login and reusable user-level settings | Sessions, logs, skills, project settings, and system policy |
| Claude Code | Account login from `/login` | Claude Code settings, plugins, API keys, cloud providers, and Claude Desktop |
| Antigravity | A consumer OAuth login | Sign-in flow, settings, quotas, Manager data, and SSH or container login files |

Codex usage reports are separate from Profile switching. They summarize local session data without assigning activity to a Profile or account.

The Desktop app can also show temporary usage-limit snapshots for the current Codex or Antigravity Profile. These checks do not change limits or add activity attribution.

## What happens when you switch

1. Review what will change. Login values remain hidden.
2. Confirm the switch. ProfileDeck checks the current files or login again.
3. ProfileDeck creates a temporary recovery point before changing the selected tool.
4. The selected Profile becomes current only after the change succeeds.

If a change does not finish, open Diagnostics or run `profiledeck-cli doctor` before switching again.

## Continue

- [Get started](./guide/getting-started.md)
- [Understand Profiles, logins, and settings](./guide/concepts.md)
- [Manage Codex Profiles](./codex/profiles.md)
- [Manage Claude Code Profiles](./claude-code/profiles.md)
- [Manage Antigravity Profiles](./antigravity/profiles.md)
- [Review data and security](./reference/data-security.md)
