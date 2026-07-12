# Codex Usage and Cost

ProfileDeck imports local Codex activity and provides offline analysis by time, model, and session count. It does not query account limits or guess which Profile or ChatGPT account produced a session. Limit checks on Desktop Profile pages are a separate feature and never change Usage reports.

## Automatic Desktop sync

The Desktop app syncs once after startup, then continues in the background while ProfileDeck is open or hidden in the tray. Set the interval to 5, 15, 30, or 60 seconds under **Codex > Settings > Local usage sync interval**. The default is 15 seconds.

Syncs never overlap. If one scan is still running when the next interval arrives, that interval is skipped. A failed scan is retried at the next interval. The Usage page shows the latest status without repeating notifications.

## Manual CLI sync

```bash
profiledeck usage sync codex
```

By default, ProfileDeck scans:

```text
$CODEX_HOME/sessions/**/*.jsonl
$CODEX_HOME/archived_sessions/*.jsonl
```

If `CODEX_HOME` is not set, it falls back to `~/.codex`. Use `--codex-dir` for a specific Codex home:

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

Repeated syncs do not count the same usage twice, including sessions moved or copied into `archived_sessions`. When a forked session contains earlier usage from its parent, ProfileDeck counts that earlier usage once and keeps new post-fork usage separate. Invalid, oversized, and unsupported records are skipped and reported without storing their contents.

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
- estimated cost in USD when all events can be fully priced
- unknown cost event count

If any record has unknown or partial cost, `estimated_cost_usd` is null in JSON output and the cost status is `unknown`. Use the report for partial-cost details and the known subtotal.

## Report

```bash
profiledeck usage report
profiledeck usage report --range today
profiledeck usage report --range 30d --json
profiledeck usage report --range all
```

The default range is `7d`. Available ranges are:

- `today`: the current local calendar day in hourly buckets;
- `7d`: today and the previous six local calendar days;
- `30d`: today and the previous 29 local calendar days;
- `all`: monthly buckets for spans up to 36 months, otherwise yearly buckets.

Calendar boundaries use the machine's local time zone, including daylight-saving transitions. Empty buckets are included. Records without a timestamp are included in all-time totals and model statistics, reported separately, and excluded from trends.

Reports include records, unique session count, fresh and cached input, output, total tokens, cache hit rate, known cost subtotal, token-weighted pricing coverage, model statistics, and import status. The Desktop Usage page defaults to the known API-equivalent cost trend and can switch to token usage. Hovering or focusing a time period shows its exact values.

## Estimation limits

Costs are local estimates based on exact model names and a static table of [OpenAI Standard API prices](https://developers.openai.com/api/docs/pricing) embedded in the app version that imports or backfills an event. Provider prefixes, dated variants, and other unlisted aliases are not mapped to a priced model. If the model or price is unavailable, ProfileDeck keeps the token counts and marks cost as `unknown`.

GPT-5.6 Sol, Terra, and Luna have separate [cache-write pricing](https://developers.openai.com/api/docs/guides/prompt-caching#requirements), but Codex session logs do not expose cache-write token counts. ProfileDeck therefore calculates their Standard input, cached-input, and output base cost, leaves any cache-write-specific portion unpriced, and marks affected events as `partial`.

The report always shows the known subtotal. If any selected record has unknown pricing, the overall status is `unknown`; otherwise, any missing cost component makes it `partial`. Pricing coverage is the share of tokens with a known price.

These values are API-equivalent estimates, not subscription billing, account limits, or actual ChatGPT balances. Usage reports do not query provider billing APIs, balances, or relay services. Profile limit checks are separate and are never used for billing or session attribution.
