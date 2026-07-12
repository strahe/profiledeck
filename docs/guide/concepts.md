# Concepts

ProfileDeck saves the parts of an AI coding tool setup that you want to switch together.

## Provider

A provider is an AI tool integration. ProfileDeck currently supports:

- `codex` for the guided Codex workflow;
- `antigravity` for Antigravity agy v2 login Profiles;
- `generic` for advanced workflows that manage explicitly selected local files.

The Desktop sidebar calls Codex and Antigravity Agents. Provider is the underlying data and switching namespace; Agent is the supported tool workspace shown in the UI.

## Profile

A Profile is a global named composition that can participate in more than one Provider workflow. A Codex Profile contains:

- a saved Codex login;
- a Config Set with the Codex settings that should be used with that login.

The login and Config Set can be shared independently. For example, two Profiles can use the same settings with different logins, or the same login with different settings.

An Antigravity Profile contains one saved agy v2 login. The Antigravity workspace shows only Profiles that have that managed binding.

## Current Profile

Current Profile state is tracked per Provider. Codex working copies remain normal `auth.json` and `config.toml` files. Antigravity keeps its working login in the system credential store.

Before switching, ProfileDeck preserves valid changes made to the current Codex files. If a required file is missing or invalid, ProfileDeck shows a warning and does not silently save it.

## Saved login

A saved login contains tool sign-in data used by one or more Profiles. It is a hidden lifecycle resource, not a separate account managed inside ProfileDeck.

ProfileDeck may show the final characters of the Codex Account ID to help distinguish logins. This value is informational only and is never used to decide which saved login should be updated or shared.

## Config Set

A Config Set contains one complete user-level Codex configuration. The first Profile creates a Config Set named `shared`; you can rename it, copy it, or create separate Config Sets for Profiles that need different settings.

A Config Set can be deleted only when no Profile uses it.

## Codex files

ProfileDeck works with:

- `$CODEX_HOME/config.toml` for the current Codex settings;
- `$CODEX_HOME/auth.json` for the current Codex login.

Skills, plugin caches, project `.codex/config.toml` files, sessions, logs, and system policy are not part of a Config Set.

ProfileDeck changes these files only after you review and apply a switch, rollback, or recovery action. Creating, editing, forking, or importing a Profile changes saved ProfileDeck data only.

## Local ProfileDeck data

Profiles, Config Sets, saved logins, settings, usage reports, and operation history are stored locally in `profiledeck.db`. Target tools continue to own their files and system credential-store entries.
