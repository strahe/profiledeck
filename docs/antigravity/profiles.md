# Antigravity Profiles

ProfileDeck supports the consumer OAuth login used by Antigravity agy v2. Legacy Antigravity storage and other Antigravity versions are not supported.

## Save the current login

Sign in through Antigravity agy v2 first, then run:

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work --name Work
```

`detect` reports `valid`, `missing`, `invalid`, or `unavailable` without printing login values. `profile create` requires a valid current login, saves it as a hidden credential, and makes the new Profile current.

The Desktop app provides the same workflow under the Antigravity Agent in the sidebar.

## List and inspect Profiles

```bash
profiledeck antigravity profile list
profiledeck antigravity profile show work
profiledeck antigravity profile update work --name "Work account"
```

List and show output contains Profile details, login expiry, reference count, and warnings. It never contains access or refresh tokens.

## Switch Profiles

```bash
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

Antigravity plans show only the safe target label and `create`, `update`, or `noop`. Credential-store location, login payload, previews, and login hashes remain hidden.

ProfileDeck creates a private backup before updating the system credential store. It verifies the current value again immediately before writing. System credential stores do not provide cross-process compare-and-swap, so Antigravity can still refresh its login in the final interval before a write. Close Antigravity before switching when practical, then restart it after the switch.

## Save a refreshed login

Antigravity may refresh the current login while it runs. A switch captures a valid refreshed login into the previously current Profile. You can save it explicitly with:

```bash
profiledeck antigravity profile save-current
```

## Scope

Antigravity support does not include OAuth login, legacy storage, Manager import, quota reads, usage attribution, or other Antigravity versions.
