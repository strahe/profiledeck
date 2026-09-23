# ProfileDeck

ProfileDeck saves local AI coding tool logins and settings as Profiles, then lets you review and apply a switch when you need a different setup.

## Choose how to use ProfileDeck

| Option | Best for | Start here |
| --- | --- | --- |
| macOS Desktop | Managing Profiles, updates, and recovery in one app | [Install the Desktop app on macOS](./guide/getting-started.md#install-the-desktop-app-on-macos) |
| Linux amd64 package | Desktop and CLI together | [Install a DEB or RPM](./guide/getting-started.md#deb-or-rpm-package) |
| Linux portable Desktop | Desktop only; in-app updates in a user directory | [Portable Desktop](./guide/getting-started.md#portable-desktop) |
| CLI source build | Terminal workflows and automation | [Build and use the CLI](./guide/getting-started.md#build-and-use-the-cli) |

## Supported tools

| Tool | What ProfileDeck switches | What stays unchanged |
| --- | --- | --- |
| Codex | A saved login and reusable user-level settings | Sessions, logs, skills, project settings, and system policy |
| Claude Code | Account login from `/login` | Claude Code settings, plugins, API keys, cloud providers, and Claude Desktop |
| Antigravity | A consumer OAuth login | Sign-in flow, settings, quotas, Manager data, and SSH or container login files |
| Grok Build | A file-based login and reusable user-level settings | Sessions, logs, plugins, project settings, managed configuration, and quotas |

See [Getting Started](./guide/getting-started.md) for the first Profile, [Switching](./operations/switching.md) before changing a tool, and [Local Data and Security](./reference/data-security.md) before moving saved data.
