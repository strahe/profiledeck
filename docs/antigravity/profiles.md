# Antigravity Profiles

ProfileDeck can save and switch the consumer OAuth login used by Antigravity agy v2. It does not sign you in to Antigravity.

## Before you start

1. Use Antigravity agy v2.
2. Sign in through Antigravity and confirm that it works.
3. Start ProfileDeck, or run `profiledeck init` if you use the CLI.

Legacy Antigravity storage and other Antigravity versions are not supported.

## Save a Profile in the Desktop app

1. Open **Antigravity** in the ProfileDeck sidebar.
2. Select **Save Current Login**.
3. Enter a permanent Profile ID and a display name, then select **Save Profile**.

The new Profile becomes the current Antigravity Profile. ProfileDeck never displays its access or refresh tokens.

## Save a Profile from the CLI

Check the current login, then save it:

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work --name Work
```

`detect` reports whether the login is ready without printing it. The create command requires a valid current login.

Review or rename saved Profiles with:

```bash
profiledeck antigravity profile list
profiledeck antigravity profile show work
profiledeck antigravity profile update work --name "Work account"
```

## Switch Profiles

When practical, close Antigravity before switching so it cannot refresh its login during the change. Reopen it after the switch.

In the Desktop app, open the Profile you want, select **Use Profile**, review the change, and confirm it.

From the CLI, preview and apply the same change:

```bash
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

ProfileDeck checks the current login again and creates a private backup before changing it. If the switch is interrupted, use [Diagnostics and recovery](../operations/recovery.md).

## Save a refreshed login

Antigravity may refresh its login while it runs. ProfileDeck saves a valid refreshed login when you switch away from the current Profile. You can also save it explicitly:

```bash
profiledeck antigravity profile save-current
```

In the Desktop app, open the current Profile and select **Update from Current Antigravity**.

## What is not supported

ProfileDeck does not provide Antigravity sign-in, legacy-storage migration, Manager import, quota checks, usage attribution, or support for Antigravity versions other than agy v2.
