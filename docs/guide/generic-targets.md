# Generic Targets

Generic targets let advanced users switch local configuration files for tools that do not have a dedicated ProfileDeck workflow. Codex and Antigravity users should use their dedicated Profile commands; generic target CRUD cannot change those managed bindings.

The dedicated workflows also own the `codex` and `antigravity` Provider adapter and metadata. Generic Provider updates can change only their display name or enabled state.

## Create provider and profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

## Add a configuration file

```bash
profiledeck profile target add work settings \
  --provider my-tool \
  --path /absolute/path/to/settings.json \
  --format json \
  --strategy json-merge \
  --value-json '{"model":"gpt-5.3-codex"}'
```

Target paths must be absolute.

## Supported formats and strategies

| Strategy | Formats | `value-json` shape |
| --- | --- | --- |
| `replace-file` | `text`, `json`, `toml`, `env` | `{"content":"..."}` |
| `json-merge` | `json` | JSON object merged into the target file. |
| `toml-merge` | `toml` | JSON object converted to TOML and merged. |
| `env-merge` | `env` | JSON object with string values converted to env assignments. |

Merge strategies read the existing file when preparing the preview. If the file is not valid JSON, TOML, or env content, ProfileDeck stops and asks you to fix it before switching.

## Preview and apply

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

The preview hides sensitive values. For safety, ProfileDeck does not change files reached through symbolic links.

## Update and inspect

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

These commands save the file rules in ProfileDeck but do not change the tool's files. Files change only when you run `profiledeck switch`.
