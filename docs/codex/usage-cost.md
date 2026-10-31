# Codex Usage and Cost

ProfileDeck reports token usage from local Codex sessions and estimates its Standard API-equivalent cost. It cannot assign past activity to a Profile, saved login, or ChatGPT account. Desktop syncs while running; CLI users can sync on demand.

## Sync usage

```bash
profiledeck-cli usage sync codex
profiledeck-cli usage sync codex --codex-dir /path/to/codex-home
```

ProfileDeck reads sessions under `CODEX_HOME` or `~/.codex` by default. Repeating a sync does not double-count imported usage. Invalid or unsupported records are skipped and reported; source files are not changed. Deleting the Codex Provider deletes its saved usage report. A later explicit CLI sync can import records still present in local sessions.

## View a report

```bash
profiledeck-cli usage summary
profiledeck-cli usage report --range 30d
```

The report defaults to `7d`; other ranges are `today`, `30d`, and `all`. Dates use your local time zone. Undated records contribute to all-time totals but not the timeline. Add `--json` for machine-readable output.

## Understand estimates

ProfileDeck estimates costs using [OpenAI Standard API prices](https://developers.openai.com/api/docs/pricing), matched by exact model and usage date. Unverified model names, dates without verified prices, or incomplete usage details can leave costs unknown or only partly estimated. Token totals and any known cost subtotal remain visible.

Price updates do not recalculate existing estimates. Syncing again can fill previously unknown costs.

The included price list works offline. New Codex models may require a ProfileDeck update before cost estimates are available. `usage report` does not connect to a billing service or upload local usage. To check or disable price-list updates:

```bash
profiledeck-cli usage pricing check
profiledeck-cli usage pricing auto off
```

These figures are not invoices, subscription charges, account limits, or ChatGPT balances. [Limit checks](./profiles.md#check-limits-and-keep-a-login-active) are separate from usage reports. Saved usage excludes prompts, completions, API keys, and full source-file paths; see [Local Data and Security](../reference/data-security.md).
