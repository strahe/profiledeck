# Claude Code Profiles

Claude Code Profiles let you save and switch official Claude Code subscription logins. Each Profile binds one hidden login; Claude Code settings, MCP servers, plugins, API keys, cloud providers, and usage attribution remain outside this integration.

Claude Code and Claude Desktop are separate products. This feature reads and changes only the official Claude Code credential target and does not inspect or modify Claude Desktop.

## Requirements

- Initialize ProfileDeck with `profiledeck init`.
- Sign in from Claude Code with `/login` before capturing the first Profile.
- Use a subscription login whose credential contains the current `claudeAiOauth` subscription fields.

Console/API-key logins and cloud-provider authentication are not captured. ProfileDeck does not perform the OAuth login itself.

## Save two accounts

Sign in to the first account from Claude Code, then save it:

```bash
profiledeck claude-code detect
profiledeck claude-code profile create personal --name "Personal"
```

Use Claude Code `/login` to sign in to the second account, then save it separately:

```bash
profiledeck claude-code profile create work --name "Work"
profiledeck claude-code profile list
```

ProfileDeck assigns each saved login an opaque internal ID. User, organization, and subscription fields inside OAuth data are not used to identify, merge, or overwrite saved logins.

## Switch accounts

Preview and apply a switch with the dedicated Provider ID:

```bash
profiledeck plan claude-code personal
profiledeck switch claude-code personal --yes
```

Start a new Claude Code session after switching, then use `/status` to confirm the account. ProfileDeck does not stop or change already running Claude Code processes.

When the current working login is a new valid version of the active saved login, a switch saves that update before selecting the next Profile. A working login that matches another known Profile is not written over the active Profile. Expired working logins are not saved automatically, but an expired saved Profile can still be selected so Claude Code can renew it through `/login`.

Use this command to save the current Claude Code login explicitly into the active Profile:

```bash
profiledeck claude-code profile save-current
```

If the hidden login is shared by multiple Profiles, review the affected Profile count and confirm with `--yes`.

## Credential location

ProfileDeck fixes the credential location when it first creates the `claude-code` Provider:

- macOS: the single generic-password Keychain item with service `Claude Code-credentials` and the current macOS short username;
- Linux and Windows: `.credentials.json` below `CLAUDE_CONFIG_DIR`, or `~/.claude/.credentials.json` when that variable is unset.

Later commands continue using that saved location. A CLI process that observes a different `CLAUDE_CONFIG_DIR` reports a non-authoritative warning instead of silently switching targets.

On macOS, Claude Code must create the Keychain item with `/login`; ProfileDeck only reads and updates the one exact existing item. On Linux, ProfileDeck writes the credential file with `0600` permissions and repairs that mode during a switch when needed. On Windows, it uses an atomic replacement in the target directory without changing directory access-control lists.

Opening the Claude Code Profiles page, running `detect`, and running Diagnostics use a passive Keychain check and do not open a macOS authorization dialog. When macOS requires permission, Desktop shows an explicit **Authorize** action. Authorizing, capturing, or switching a login may then show a system dialog; enter the macOS login password to grant Keychain access to ProfileDeck. This is not a Claude account password check. Each tool's Keychain item has its own access policy, so another integration may be readable without the same prompt.

## Authentication override warnings

ProfileDeck reports only the names of supported authentication override variables visible to its own process. It does not read their values and cannot determine the environment of another terminal or an already running Claude Code process.

Claude Code settings, `apiKeyHelper`, API-key variables, and cloud-provider switches may take precedence over the selected subscription login. Review the [Claude Code authentication documentation](https://code.claude.com/docs/en/team) when a new session does not use the expected account.

## Current limits

The first release does not include Claude Desktop, credential deletion, sensitive export/import, quota checks, usage attribution, Console or API-key accounts, Claude Code settings switching, or parallel account sessions.
