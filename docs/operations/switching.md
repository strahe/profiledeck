# Review and Switch Profiles

Switching changes the login or settings used by Codex, Claude Code, or Antigravity. ProfileDeck lets you review the change and creates a backup before applying it.

## Switch in the Desktop app

1. Open **Codex**, **Claude Code**, or **Antigravity**.
2. Select the Profile you want to use.
3. Select **Use Profile**.
4. Review the files or login that will change and any warnings.
5. Confirm the switch.

ProfileDeck marks the Profile as current only after the change succeeds. Restart the selected tool or open a new session if it does not pick up the new login immediately.

## Preview from the CLI

Run `plan` before switching:

```bash
profiledeck plan codex work
profiledeck plan claude-code personal
profiledeck plan antigravity work
```

Add `--json` if you need structured output:

```bash
profiledeck plan codex work --json
```

For files, the preview shows which path will be created, updated, or left unchanged. For saved logins, it shows only a safe target name and action. Sensitive login values remain hidden in all previews.

Warnings tell you when a file or login is missing, invalid, unsupported, or unsafe to change. Resolve blocking warnings before applying the switch.

## Apply from the CLI

```bash
profiledeck switch codex work --yes
profiledeck switch claude-code personal --yes
profiledeck switch antigravity work --yes
```

To apply only the exact state you previously reviewed, copy the fingerprint from `plan`:

```bash
profiledeck switch codex work \
  --plan-fingerprint <fingerprint> \
  --yes
```

If the Profile or selected tool changes after the preview, ProfileDeck rejects the fingerprint without writing anything. Run `plan` again and review the new result.

## What happens during a switch

Before changing the selected tool, ProfileDeck:

1. checks that another ProfileDeck change is not still running;
2. checks the current files or login again;
3. saves valid updates from the Profile you are leaving when supported;
4. creates a private backup;
5. changes only the required files or login;
6. marks the new Profile as current after every change succeeds.

ProfileDeck stops without applying the switch if it cannot verify the current state or create a usable backup. An interrupted or failed operation stays visible in Diagnostics so it can be recovered safely.

## Keep backups private

Switch backups may contain previous Codex files, Claude Code subscription logins, or Antigravity logins. Keep the ProfileDeck data directory private and do not commit, upload, or share its backup files.

Recovery and rollback restore the selected tool and the previously current Profile. Updates that were already saved into a Profile remain saved. See [Diagnostics and recovery](./recovery.md) for the commands and recovery choices.
