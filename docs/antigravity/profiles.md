# Antigravity Profiles

ProfileDeck saves and switches Antigravity's consumer OAuth login from the operating system credential store. It does not sign you in or manage legacy storage, settings, or separate SSH and container logins.

## Before you start

Sign in to Antigravity and confirm it works. CLI users run `profiledeck-cli init` once. Use `detect` to check whether the current login is supported before creating a Profile:

```bash
profiledeck-cli antigravity detect
profiledeck-cli antigravity profile create work
```

The first Profile becomes current. To save another login, sign in to that account in Antigravity and create another Profile.

## Switch and save a refreshed login

Close Antigravity before switching when practical so it cannot refresh its login during the change. See [Review and Switch](../operations/switching.md) for the CLI command.

Antigravity may refresh its login while running. ProfileDeck saves a valid refreshed login when you switch away; you can save it before signing in to another account with:

```bash
profiledeck-cli antigravity profile save-current
```

A short-lived access token's expiry does not tell you how long the saved Profile remains reusable. See [Profiles and settings](../guide/concepts.md) for sharing and deletion effects.

## Check usage limits

Desktop checks the current Profile at startup and after switching; further checks are manual. The check sends its access token to an unpublished Google Cloud Code service. This may carry account risk. ProfileDeck does not refresh or write back the token during the check.

Limit results remain in memory and are not saved to usage reports or backups. They do not identify which Profile produced earlier activity. Limit checks are available only in Desktop.
