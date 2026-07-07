# Codex Usage and Cost

ProfileDeck can import local Codex session usage and estimate cost from token counts.

## Sync usage

```bash
profiledeck usage sync codex
```

By default, ProfileDeck scans:

```text
$CODEX_HOME/sessions/**/*.jsonl
```

If `CODEX_HOME` is not set, it falls back to `~/.codex`. Use `--codex-dir` for a specific Codex home:

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

The importer stores derived usage events. It does not store raw prompts, completions, or raw JSONL events.

## Summary

```bash
profiledeck usage summary
profiledeck usage summary --json
```

The summary includes:

- event count
- input tokens
- cached input tokens
- output tokens
- total tokens
- estimated cost in USD when all events can be priced
- unknown cost event count

If any event has unknown cost, `estimated_cost_usd` is null in JSON output and the cost status is `unknown`.

## Estimation limits

Costs are local estimates based on a static model price table in the binary. If the model is unknown, the price is unavailable, or the log does not expose enough billing context, ProfileDeck keeps the token counts and marks cost as `unknown`.

ProfileDeck does not query provider billing APIs, balances, or relay services.
