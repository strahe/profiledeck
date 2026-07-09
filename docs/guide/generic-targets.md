# Generic Targets

Generic targets are lower-level building blocks for advanced local workflows. Most Codex users should start with `profiledeck codex profile create`.

## Create provider and profile

```bash
profiledeck provider create my-tool --adapter generic --name "My Tool"
profiledeck profile create work --name "Work"
```

## Add a target

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

Merge strategies need the existing file content during planning. Invalid existing JSON, TOML, or env content causes plan generation to fail.

## Preview and apply

```bash
profiledeck plan my-tool work
profiledeck switch my-tool work --yes
```

The plan shows redacted previews and SHA-256 hashes. Symlink targets are not followed and are reported as unsupported.

## Update and inspect

```bash
profiledeck provider list
profiledeck profile list
profiledeck profile target list work
profiledeck profile target show work my-tool settings
```

CRUD commands update ProfileDeck state only. They do not write target files.
