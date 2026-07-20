# Antigravity Profiles

ProfileDeck can save and switch an Antigravity consumer OAuth login stored in the operating system credential store. It does not sign you in to Antigravity.

## Before you start

1. Sign in to Antigravity and confirm that it works.
2. Start ProfileDeck, or run `profiledeck init` if you use the CLI.

Legacy Antigravity storage is not supported.

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

ProfileDeck checks the current login again and creates a private operation recovery point before changing it. If the switch is interrupted, use [Diagnostics and recovery](../operations/recovery.md).

## Save a refreshed login

Antigravity may refresh its login while it runs. The short-lived access-token expiry does not describe how long a saved Profile can be reused, so ProfileDeck does not present it as a login expiry. ProfileDeck saves a valid refreshed login when you switch away from the current Profile. You can also save it explicitly:

```bash
profiledeck antigravity profile save-current
```

In the Desktop app, open the current Profile and select **Update from Current Antigravity**.

## Delete a Profile

Open a Profile's action menu in Desktop and choose **Delete Profile**, or run:

```bash
profiledeck antigravity profile delete work --yes
```

This deletes the complete global Profile from every Agent, not only its Antigravity data. A saved login used only by that Profile is deleted, while shared saved logins remain. A current Profile or one with an unfinished operation cannot be deleted. The current Antigravity login in the system credential store does not change.

## Check usage limits

The Desktop app checks the current Antigravity Profile once at startup and after a successful switch. Select **Refresh limits** on the current Profile to check again. ProfileDeck does not poll in the background.

The Profile list shows a compact summary. Profile details show each available group, its 5-hour and weekly windows, remaining percentage, reset time, and check time. A non-current Profile can keep a snapshot checked earlier in the same app session, but you must use that Profile before refreshing it.

Limit snapshots are temporary. They are not saved to usage reports, exports, backups, or the ProfileDeck database, and they do not identify which Profile produced earlier Antigravity activity.

## What is not supported

ProfileDeck does not manage Antigravity sign-in, settings, legacy-storage migration, Manager data, model-level limit details, usage attribution, or separate login files used by SSH or container sessions. Antigravity limit checks are available only in the Desktop app.
