# Switching

Switching is the normal way to change the external login or settings used by a configured tool.

## Preview

```bash
profiledeck plan codex work
profiledeck plan codex work --json
```

The preview is read-only. File targets show:

- each file that may change;
- whether the file will be created, updated, left unchanged, or cannot be changed;
- why that action was selected;
- before and after SHA-256 hashes;
- previews with sensitive values hidden;
- warnings that need review;
- a plan fingerprint for applying exactly what was reviewed.

Codex sign-in contents are always hidden. Complete saved login and Config Set data never appears in the preview.

Sensitive targets such as the Antigravity system credential show only a safe label, `create`, `update`, or `noop`, and the reason for that action. The credential-store location, payload, previews, and content hashes remain hidden.

The fingerprint represents the reviewed Profile and current target state. If a relevant target or saved Profile changes after preview, ProfileDeck rejects that fingerprint before writing anything.

## Apply

```bash
profiledeck switch codex work --yes
profiledeck switch antigravity work --yes
```

To require an exact match with a previous preview, pass its fingerprint:

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

## What ProfileDeck protects

Before changing external targets, ProfileDeck:

1. checks that no other ProfileDeck change is still running;
2. rechecks the current external targets and the reviewed switch;
3. preserves valid changes made to the current Codex login and settings;
4. creates a backup;
5. changes only the targets that need updating;
6. records the selected Profile as current only after the external updates succeed.

ProfileDeck stops without applying the switch when it cannot safely confirm the targets, the backup, or the reviewed state. Missing, invalid, symbolic-link, and unsupported targets are shown as warnings or blocking errors instead of being silently saved.

If a switch is interrupted or fails after it starts, the Diagnostics page keeps it visible and offers recovery only when a usable backup is available.

## Backups

Every successful switch saves a backup under the ProfileDeck data directory. File-target entries show paths, actions, hashes, and permissions without printing sensitive contents.

Codex backups may contain previous `auth.json` and `config.toml` contents. Treat the backup directory as sensitive.

Antigravity backups may contain the previous login in a private payload file, written with `0600` permissions on POSIX systems. Public backup summaries omit its location and hashes.

Rollback and recovery restore external targets and the previously selected Profile. Changes already saved to a Profile login or Config Set remain saved.
