# Getting Started

Use the Desktop app for a visual workflow, or use the CLI for terminal work. Both use the same Profiles, application backups, operation recovery, and switching rules.

## Before you start

- The Universal Desktop app requires macOS 14 or later and supports Apple silicon and Intel Macs.
- Linux releases support amd64. The Desktop requires GTK 4 and WebKitGTK 6.0.
- Building the CLI requires Git, Go 1.26, Make, and a POSIX shell.
- Install the AI Agent you want to manage and sign in before saving its first Profile.

## Install the Desktop app on macOS

1. Download the latest macOS Universal DMG from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases). Stable releases use `X.Y.Z`; Beta releases use `X.Y.Z-beta.N`.
2. Open the DMG and drag `ProfileDeck.app` to Applications.
3. Open ProfileDeck. The app creates its local data automatically. If you already have a current Codex or Antigravity Profile, startup also checks its limits. Codex may refresh its saved login during that check; Antigravity checks are read-only.
4. Select Codex, Claude Code, or Antigravity in the sidebar, then open **Profiles**.

Published DMGs are Developer ID signed and notarized by Apple. If macOS reports that the app is damaged or cannot verify its developer, delete that copy and download it again from the official Releases page instead of bypassing the warning.

macOS Desktop releases can follow Stable or Beta updates from **Settings → General → App updates**. Local development builds do not check for updates. See [Update the Desktop app](./updates.md).

## Install on Linux amd64

Choose one install path:

- **DEB or RPM package:** Desktop and CLI together; install newer packages from GitHub Releases.
- **Portable Desktop:** Desktop only; keep it in a user-writable directory so ProfileDeck can apply in-app updates. See [Update the Desktop app](./updates.md).

### DEB or RPM package

Stable and Beta DEB and RPM packages install both `profiledeck` and `profiledeck-desktop`, add a desktop launcher, and declare GTK 4 and WebKitGTK 6.0 dependencies. DEB installation is smoke-tested on Ubuntu 24.04, and RPM installation on Fedora 44.

On Ubuntu 24.04, download `ProfileDeck_<version>_linux_amd64.deb` from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases), then install it:

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
```

On Fedora 44, download `ProfileDeck_<version>_linux_amd64.rpm`, then install it:

```bash
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

Open ProfileDeck from the application menu, or run `profiledeck-desktop`. The `profiledeck` CLI is on your `PATH` as `/usr/bin/profiledeck`.

These installs do not check for or download in-app updates. Download a newer matching package from GitHub Releases and install it with the same command. See [Update a Linux package](./updates.md#update-a-linux-package).

Continue with [Save your first Profile](#save-your-first-profile).

### Portable Desktop

Use this when you want a Desktop-only install with in-app updates.

1. Install the runtime libraries:

```bash
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4
# or: sudo dnf install gtk4 webkitgtk6.0
```

2. Download `ProfileDeck_<version>_linux_amd64.tar.gz` from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases).

3. Extract and link the app into a directory your user can write:

```bash
mkdir -p "$HOME/.local/opt/profiledeck" "$HOME/.local/bin"
tar -xzf ProfileDeck_<version>_linux_amd64.tar.gz -C "$HOME/.local/opt/profiledeck"
ln -sf "$HOME/.local/opt/profiledeck/profiledeck-desktop" "$HOME/.local/bin/profiledeck-desktop"
```

4. Run `profiledeck-desktop` (ensure `$HOME/.local/bin` is on your `PATH`).

The archive contains only `profiledeck-desktop`, not the CLI. Use a DEB or RPM when you need `profiledeck`. Keep the install directory writable so ProfileDeck can replace the app after it verifies an update. See [Update the Desktop app](./updates.md).

Continue with [Save your first Profile](#save-your-first-profile).

## Build and use the CLI

Clone the public repository and build the command:

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
make build
export PATH="$PWD/bin:$PATH"
profiledeck version
profiledeck init
```

`profiledeck init` creates ProfileDeck's local database, encrypted application-backup folder, and operation-recovery folder. To use a different location, pass the parent config directory:

```bash
profiledeck --config-dir /path/to/config-root init
```

ProfileDeck creates a `profiledeck` folder below that directory.

The shell command `export PATH=...` updates `PATH` for the current shell. For future terminals, add this repository's `bin` directory to your shell profile.

## Save your first Profile

Prepare the selected tool first:

- **Codex:** confirm that `config.toml` and `auth.json` exist in `CODEX_HOME` or `~/.codex`. If `auth.json` is missing, follow the [Codex prerequisite](../codex/profiles.md#before-you-start).
- **Claude Code:** run `/login` in Claude Code.
- **Antigravity:** sign in to Antigravity and confirm that it works.

In Desktop, select the tool and use the save action on its Profiles page. Enter a permanent Profile ID and a display name. To save another account, sign in to that account in the tool, return to ProfileDeck, and save another Profile.

Use these minimal CLI flows instead:

### Codex

```bash
profiledeck codex detect
profiledeck codex profile create work
profiledeck plan codex work
profiledeck switch codex work --yes
```

### Claude Code

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

Start a new Claude Code session after switching and run `/status` to confirm the account.

### Antigravity

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

Close Antigravity before switching when practical, then restart it afterward.

## Confirm the result

Desktop marks the selected Profile as **Current** after a successful switch. In the CLI, list the Profiles for the selected tool:

```bash
profiledeck codex profile list
profiledeck claude-code profile list
profiledeck antigravity profile list
```

If ProfileDeck reports an incomplete change or blocks another switch, open **Diagnostics** or run:

```bash
profiledeck doctor
```

Follow only the recovery action that Diagnostics recommends. See [Diagnostics and Recovery](../operations/recovery.md) for unfinished-switch recovery and application backup restore. Successful switches cannot be undone.

## Next steps

- [Codex Profiles](../codex/profiles.md)
- [Claude Code Profiles](../claude-code/profiles.md)
- [Antigravity Profiles](../antigravity/profiles.md)
- [Update the Desktop app](./updates.md)
- [Switching safely](../operations/switching.md)
- [Data and security](../reference/data-security.md)
