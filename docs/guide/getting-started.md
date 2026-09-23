# Getting Started

Sign in to a supported tool before saving its first Profile. Desktop initializes ProfileDeck automatically; CLI users run `profiledeck-cli init` once.

## Install the Desktop app on macOS

macOS 14 or later is required. Download the Universal DMG from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) and install `ProfileDeck.app`.

If macOS reports that the app is damaged or cannot verify its developer, download a fresh copy from the official Releases page. Do not bypass the warning. See [Desktop updates](./updates.md) for Stable and Beta channels.

## Install on Linux amd64

Linux Desktop requires GTK 4 and WebKitGTK 6.0. DEB and RPM packages include Desktop and CLI; the portable archive contains Desktop only.

### DEB or RPM package

Download the matching package from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases):

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# or
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

The CLI is installed as `profiledeck-cli`. Install newer packages the same way; these builds do not update in the app.

### Portable Desktop

Install the runtime libraries, then download the Linux tar archive from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases):

```bash
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4
# or: sudo dnf install gtk4 webkitgtk6.0
```

Extract and run it from a user-writable directory:

```bash
mkdir -p "$HOME/.local/opt/profiledeck"
tar -xzf ProfileDeck_<version>_linux_amd64.tar.gz -C "$HOME/.local/opt/profiledeck"
"$HOME/.local/opt/profiledeck/profiledeck"
```

Keep that directory writable for [in-app updates](./updates.md). The archive does not include the CLI.

## Build and use the CLI

Building from source requires Git, Go 1.27, Make, and a POSIX shell:

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
make build
export PATH="$PWD/bin:$PATH"
profiledeck-cli init
```

Use `--config-dir /path/to/config-root` before the command to store ProfileDeck data elsewhere.

## Save your first Profile

Each tool has a different sign-in prerequisite:

| Tool | Before saving | CLI example |
| --- | --- | --- |
| Codex | Save a file-based login and valid `config.toml` ([details](../codex/profiles.md#before-you-start)) | `profiledeck-cli codex profile create work` |
| Claude Code | Run `/login` ([details](../claude-code/profiles.md#before-you-start)) | `profiledeck-cli claude-code profile create personal` |
| Antigravity | Sign in to Antigravity ([details](../antigravity/profiles.md#before-you-start)) | `profiledeck-cli antigravity profile create work` |
| Grok Build | Save a valid `auth.json` ([details](../grok-build/profiles.md#before-you-start)) | `profiledeck-cli grok-build profile create work` |

Choose one command after `profiledeck-cli init`. To save another account, sign in to that account in its tool, then create another Profile. Profile IDs are permanent.

Preview and apply a switch, for example:

```bash
profiledeck-cli switch codex work --dry-run
profiledeck-cli switch codex work --yes
```

`--dry-run` is optional. See [Review and Switch](../operations/switching.md) for switching effects and recovery.
