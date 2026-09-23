# Claude Code Profiles

ProfileDeck saves the account login created by Claude Code `/login`. It does not sign you in or manage API keys, Console or cloud-provider authentication, Claude Code settings, or Claude Desktop.

## Before you start

Run `/login` in Claude Code, then initialize the CLI with `profiledeck-cli init` if needed. On macOS, ProfileDeck may need permission to read the Claude Code Keychain item. macOS asks for your computer login password, not your Claude account password.

## Save and switch accounts

```bash
profiledeck-cli claude-code profile create personal
```

To save another account, run `/login` for that account in Claude Code, then create another Profile. The first saved Profile becomes current. Switching uses the [shared switch command](../operations/switching.md). After switching, start a new Claude Code session and run `/status` to confirm the account; existing processes keep their previous state.

ProfileDeck saves a valid refreshed login when you switch away. You can save it before another `/login` with:

```bash
profiledeck-cli claude-code profile save-current
```

If several Profiles share that login, review the affected count before confirming with `--yes`. Sharing and deletion effects are explained under [Profiles and settings](../guide/concepts.md).

## Login location and overrides

On Linux and Windows, ProfileDeck uses `CLAUDE_CONFIG_DIR/.credentials.json`, or `~/.claude/.credentials.json` when the variable is unset. It keeps the location chosen during first setup and warns if a later CLI process points elsewhere.

Claude Code settings, `apiKeyHelper`, API-key environment variables, or cloud-provider options can take precedence over the selected account. If the wrong account is active, start a new session, check `/status`, and consult [Claude Code authentication](https://code.claude.com/docs/en/authentication). ProfileDeck cannot inspect another terminal's environment or an already running Claude Code process.
