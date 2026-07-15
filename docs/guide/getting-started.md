# Getting Started

Use the macOS Desktop app for a visual workflow, or build the CLI for terminal use. Both use the same Profiles, application backups, operation recovery, and switching rules.

## Before you start

- The Desktop Alpha requires macOS 14 or later on Apple silicon.
- Building the CLI requires Git, Go 1.26, Make, and a POSIX shell.
- Install the AI coding tool you want to manage and sign in before saving its first Profile.

## Use the Desktop app

1. Download the latest macOS arm64 ZIP from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases).
2. Expand the ZIP and move `ProfileDeck.app` to Applications.
3. Open ProfileDeck. The app creates its local data automatically. If you already have a current Codex or Antigravity Profile, startup also checks its limits. Codex may refresh its saved login during that check; Antigravity checks are read-only.
4. Select Codex, Claude Code, or Antigravity in the sidebar, then open **Profiles**.

Current Alpha builds are not notarized. If macOS blocks the first launch, open **System Settings → Privacy & Security** and choose **Open Anyway** for ProfileDeck. Only do this for a file you downloaded from the official Releases page.

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
- **Claude Code:** run `/login` in Claude Code with an official subscription account.
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
- [Switching safely](../operations/switching.md)
- [Data and security](../reference/data-security.md)
