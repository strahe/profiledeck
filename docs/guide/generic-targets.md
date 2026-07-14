# Switch Other Configuration Files

Generic targets are an advanced CLI feature for switching explicitly selected local configuration files. Use the dedicated Profile commands for Codex, Claude Code, and Antigravity; generic target commands cannot change their managed logins or settings.

## Before you start

- Initialize ProfileDeck with `profiledeck init`.
- Use an absolute path to a regular local file.
- Decide whether ProfileDeck should replace the whole file or merge selected values.

ProfileDeck refuses to change files reached through symbolic links. Review the preview carefully when a target file contains secrets.

## Create a tool and Profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

The Provider ID identifies the tool in later commands. The Profile ID identifies the saved setup you want to switch to.

## Add a configuration file

```bash
profiledeck profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"example-model"}'
```

## Choose how the file changes

| Strategy | Formats | Value supplied with `--value-json` |
| --- | --- | --- |
| `replace-file` | `text`, `json`, `toml`, `env` | `{"content":"..."}` replaces the complete file. |
| `json-merge` | `json` | A JSON object merged into the current JSON file. |
| `toml-merge` | `toml` | A JSON object converted to TOML and merged. |
| `env-merge` | `env` | A JSON object with string values converted to environment assignments. |

Merge strategies require the current file to contain valid JSON, TOML, or env data. Fix invalid content before switching.

## Review and switch

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

The preview shows the selected file and hides sensitive-looking values. ProfileDeck checks the file again and creates a backup before applying the change.

## Inspect or recover

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

Adding or editing a target changes only the saved rule. The external file changes only after `profiledeck switch` succeeds.

If a switch does not finish, run `profiledeck doctor` before trying again. Use [Recover or Undo](../operations/recovery.md) to restore a failed or unwanted change.
