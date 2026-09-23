# Switch Other Configuration Files

Generic targets are an advanced CLI feature for local configuration files you select. They cannot manage Codex, Claude Code, Antigravity, or Grok Build logins and settings; use those tools' dedicated Profile commands.

Use an absolute path to a regular file. Symbolic links are not supported. Decide whether to replace the entire file or merge selected values; review the preview when it may contain secrets.

## Save a file target

```bash
profiledeck-cli init
profiledeck-cli provider create my-tool --adapter generic --name "My Tool"
profiledeck-cli profile create work --name "Work"
profiledeck-cli profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"example-model"}'
```

| Strategy | Format | `--value-json` |
| --- | --- | --- |
| `replace-file` | `text`, `json`, `toml`, `env` | `{"content":"..."}` replaces the whole file. |
| `json-merge` | `json` | JSON object merged into the file. |
| `toml-merge` | `toml` | JSON object converted to TOML and merged. |
| `env-merge` | `env` | JSON object with string values converted to assignments. |

Merge requires valid existing content. Adding or editing a target changes only ProfileDeck's saved rule; the external file changes after a successful switch.

## Switch the file

```bash
profiledeck-cli switch my-tool work --dry-run
profiledeck-cli switch my-tool work --yes
```

The optional preview hides sensitive-looking values. See [Review and Switch](../operations/switching.md) for switching and recovery.
