<div align="center">

# ProfileDeck

**Save and switch Profiles for AI Agents**

[![Release](https://img.shields.io/github/v/release/strahe/profiledeck?include_prereleases&label=release)](https://github.com/strahe/profiledeck/releases)
[![License](https://img.shields.io/github/license/strahe/profiledeck)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](docs/guide/getting-started.md)

[English](README.md) · [简体中文](README_ZH.md) | [Documentation](docs/index.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [Releases](https://github.com/strahe/profiledeck/releases)

</div>

ProfileDeck saves AI Agent logins and settings as reusable **Profiles**. Use the Desktop app or CLI to switch between Profiles and preview the changes before applying them.

## Supported Agents

ProfileDeck supports Codex, Claude Code, Antigravity, and Grok Build. See [Supported tools](docs/index.md#supported-tools) for what each Profile includes.

## Install

| Option | Includes | Details |
| --- | --- | --- |
| **macOS app** | Desktop | [Download from Releases](https://github.com/strahe/profiledeck/releases) |
| **Linux DEB or RPM** | Desktop and CLI | [Install a Linux package](docs/guide/getting-started.md#deb-or-rpm-package) |
| **Linux portable app** | Desktop | [Install portable Desktop](docs/guide/getting-started.md#portable-desktop) |
| **Build from source** | CLI | [Build and use the CLI](docs/guide/getting-started.md#build-and-use-the-cli) |

## CLI example

For a Codex Profile with ID `work`:

```bash
profiledeck-cli codex profile list
profiledeck-cli switch codex work --yes
profiledeck-cli usage summary --provider codex
```

Use `--json` on supported commands when a script needs machine-readable output. See the [CLI reference](docs/reference/cli.md) for common commands, or run `profiledeck-cli --help` for full syntax.

## License

[Apache License 2.0](LICENSE)
