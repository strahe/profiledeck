# Codex Usage and Cost

ProfileDeck reads local Codex session data to show token usage, activity, and estimated API-equivalent cost. Reports stay offline and do not assign sessions to a Profile or ChatGPT account.

## Sync in the Desktop app

The Desktop app syncs after startup and continues while ProfileDeck is open or in the menu bar.

To change the interval, open **Codex → Settings → Usage reports → Update frequency** and choose 5, 15, 30, or 60 seconds. The default is 15 seconds. The Usage page shows the latest sync result and reports files it could not read.

## Sync from the CLI

Run:

```bash
profiledeck usage sync codex
```

By default, ProfileDeck reads:

```text
$CODEX_HOME/sessions/**/*.jsonl
$CODEX_HOME/archived_sessions/*.jsonl
```

If `CODEX_HOME` is not set, it uses `~/.codex`. To read another Codex home:

```bash
profiledeck usage sync codex --codex-dir /path/to/codex-home
```

You can repeat a sync safely; previously imported usage is not counted again. Invalid, oversized, or unsupported records are skipped and reported without storing their contents.

## View a summary

```bash
profiledeck usage summary
profiledeck usage summary --json
```

The summary includes event count, input and output tokens, cached input, total tokens, estimated cost when available, and the number of events with unknown cost.

## View a report

```bash
profiledeck usage report
profiledeck usage report --range today
profiledeck usage report --range 30d --json
profiledeck usage report --range all
```

The default range is `7d`. Available ranges are:

- `today`: the current local calendar day, grouped by hour;
- `7d`: today and the previous six local calendar days;
- `30d`: today and the previous 29 local calendar days;
- `all`: monthly groups for spans up to 36 months, then yearly groups.

Reports use your computer's local time zone. They include token totals, session count, cache hit rate, known cost, pricing coverage, model details, and sync status. Records without a timestamp are included in all-time totals and model details, reported separately, and excluded from the timeline.

## Understand cost estimates

ProfileDeck estimates each event from its exact model name and the [OpenAI Standard API prices](https://developers.openai.com/api/docs/pricing) included with the installed version when that event first receives a cost estimate. Installing a later version does not recalculate existing estimates.

- `estimated`: all selected usage has a price;
- `partial`: ProfileDeck can estimate only part of the selected usage;
- `unknown`: at least one selected record has no usable price.

The report always keeps token totals and shows the known subtotal. Pricing coverage shows how much of the selected token usage could be priced.

These estimates are not subscription billing, account limits, invoices, or ChatGPT balances. ProfileDeck does not contact a billing API when producing a usage report. [Codex limit checks](./profiles.md#check-limits-and-keep-a-login-active) are separate and never change or attribute usage reports.

## Privacy limits

Usage storage excludes raw prompts, raw completions, API keys, and full source-file paths. ProfileDeck does not upload usage data or use it for telemetry. See [Local data and security](../reference/data-security.md) for storage and backup guidance.
