# Claude Code Profiles

A Claude Code Profile saves one official subscription login. ProfileDeck does not change Claude Code settings, MCP servers, plugins, API keys, cloud-provider authentication, or Claude Desktop.

## Before you start

- Desktop initializes ProfileDeck automatically. CLI users must run `profiledeck init` once.
- Run `/login` in Claude Code before saving a Profile.
- Use an official Pro, Max, Team, or Enterprise subscription login.

Console/API-key logins and cloud-provider authentication are not saved. ProfileDeck does not perform the Claude Code login for you.

## Save Profiles in Desktop

1. Select **Claude Code → Profiles**.
2. If macOS permission is required, choose **Authorize** and allow ProfileDeck to read the Claude Code login from Keychain.
3. Choose **Save Current Login**, then enter a permanent Profile ID and a display name.
4. Run `/login` in Claude Code for another account, return to ProfileDeck, and save another Profile.

The first saved Profile becomes current. Saving another Profile does not change Claude Code settings.

## Save Profiles with the CLI

Sign in to the first account, then run:

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal --name "Personal"
```

Sign in to the second account with `/login`, then save it separately:

```bash
profiledeck claude-code profile create work --name "Work"
profiledeck claude-code profile list
```

List and show commands display login status and expiry information without printing token values.

## Switch accounts

In Desktop, choose **Use Profile**, review the login change, and confirm. ProfileDeck creates a private operation recovery point before continuing.

With the CLI:

```bash
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

Start a new Claude Code session after switching and run `/status` to confirm the account. Already running Claude Code processes do not change.

If Claude Code refreshed the current login, ProfileDeck saves a valid update before switching away. An expired saved Profile can still be selected so Claude Code can renew it through `/login`.

## Save a refreshed login

Use **Save Current Claude Code Login** on the current Profile in Desktop, or run:

```bash
profiledeck claude-code profile save-current
```

When the saved login is shared by multiple Profiles, ProfileDeck shows how many Profiles will change. Review that count before confirming with `--yes` in the CLI.

## Allow Keychain access on macOS

Claude Code must create its Keychain login with `/login` before ProfileDeck can save it. Opening the Profiles page, running `detect`, or opening Diagnostics only checks whether the login is available.

When access is required, Desktop shows **Authorize**. macOS may then ask for your macOS login password to grant ProfileDeck access to the existing Claude Code Keychain item. This is not a request for your Claude account password.

Keychain permissions are specific to each item. Another tool working without a prompt does not mean Claude Code should do the same.

## Login files on Linux and Windows

ProfileDeck uses `.credentials.json` below `CLAUDE_CONFIG_DIR`, or `~/.claude/.credentials.json` when that variable is unset. It keeps using the location saved when Claude Code support was first set up.

If a later CLI process sees a different `CLAUDE_CONFIG_DIR`, ProfileDeck warns instead of silently switching to another file. On Linux, ProfileDeck keeps the login file readable only by your user account when it writes the file.

## If Claude Code uses the wrong account

Claude Code settings, `apiKeyHelper`, API-key environment variables, and cloud-provider options can take precedence over the selected subscription login. ProfileDeck reports the names of supported authentication override variables visible to its own process, but it cannot inspect another terminal or an already running Claude Code process.

Start a new session, run `/status`, and review the [Claude Code authentication documentation](https://code.claude.com/docs/en/authentication) when the selected account is not active.

## What is not included

Claude Code Profile support does not include Claude Desktop, saved-login deletion, sensitive export/import, quota checks, usage attribution, Console or API-key accounts, Claude Code settings switching, or parallel account sessions.
