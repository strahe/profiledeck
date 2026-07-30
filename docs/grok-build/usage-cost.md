# Grok Build Usage and Cost

ProfileDeck reads local Grok Build session records to show token usage, activity, and estimated API-equivalent cost. Reports stay offline and do not assign activity to a Profile, saved login, or account.

## Sync in the Desktop app

The Desktop app syncs after startup and continues while ProfileDeck is open or in the menu bar.

To change the interval, open **Grok Build → Settings → Usage reports → Update frequency** and choose 5, 15, 30, or 60 seconds. The default is 15 seconds. Codex and Grok Build use separate intervals and sync status.

Background sync uses the existing Grok Build Provider. If it has not been created yet, open **Grok Build → Profiles** to create a Profile, or run an explicit CLI sync.

## Sync from the CLI

Run:

```bash
profiledeck-cli usage sync grok-build
```

ProfileDeck uses the Grok Home already bound to the Provider. Before the Provider exists, Grok Home is resolved from `--grok-home`, then `GROK_HOME`, then `~/.grok`. To use an explicit location:

```bash
profiledeck-cli --grok-home /path/to/grok-home usage sync grok-build
```

An existing Provider never silently switches to another Grok Home.

ProfileDeck reads ordinary files matching:

```text
<grok-home>/sessions/*/*/updates.jsonl
```

It does not follow symbolic links. Nested `subagents` records are excluded because Grok Build already includes successful child-agent usage in the completed parent turn.

You can repeat a sync safely; previously imported usage is not counted again. Records with missing, empty, or incomplete usage are skipped. If a file changes while it is being read, contains an oversized or malformed terminal record, or has an unrecognized terminal format, ProfileDeck leaves all new data from that file uncommitted and retries it during a later sync. The source file is never moved or changed.

Copied fork history is counted once. When the same completed turn appears in multiple sessions with identical usage, ProfileDeck assigns it to one stable derived session without storing the original session identifier. Conflicting usage for that same turn causes the affected file to be retried instead.

Deleting the Grok Build Provider also deletes its saved usage reports, import progress, and sync setting. Desktop background sync will not recreate a deleted Provider. Running this CLI sync is an explicit request: it can set up the Provider again and reimport usage still present in local session files.

## View a summary

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage summary --provider grok-build --json
```

The summary includes event count, input and output tokens, cached input, total tokens, estimated cost when available, and the number of events with unknown cost.

## View a report

```bash
profiledeck-cli usage report --provider grok-build
profiledeck-cli usage report --provider grok-build --range today
profiledeck-cli usage report --provider grok-build --range 30d --json
profiledeck-cli usage report --provider grok-build --range all
```

The default range is `7d`. Reports use your computer's local time zone and include token totals, session count, cache hit rate, known cost, pricing coverage, model details, and sync status. Records without a timestamp are included in all-time totals and model details, reported separately, and excluded from the timeline.

## Understand cost estimates

ProfileDeck uses the short-context xAI Standard API-equivalent prices included with the installed version:

| Model | Input | Cached input | Output |
| --- | ---: | ---: | ---: |
| `grok-4.5` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.5-build` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.5-latest` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-build-latest` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |

The prices come from [Grok 4.5](https://docs.x.ai/developers/models/grok-4.5) and [xAI pricing](https://docs.x.ai/developers/pricing). ProfileDeck treats the `grok-4.5-build` identifier found in Grok Build session records as pricing-equivalent to Grok 4.5. Aggregated session records do not show whether an individual request entered a long-context pricing tier, so ProfileDeck does not apply the 2× long-context multiplier.

Amounts recorded by Grok Build are not imported as billing data. An unrecognized model keeps its token totals but has unknown cost. Existing estimates are not recalculated when a later ProfileDeck version changes its built-in prices; facts with unknown cost can receive an estimate when their model becomes recognized.

These estimates are not invoices, credits, quotas, or account balances. ProfileDeck does not contact xAI or a billing API when syncing or producing a report.

## Privacy limits

Usage storage excludes raw prompts, agent results, API keys, direct session identifiers, and full source-file paths. ProfileDeck does not upload usage data or use it for telemetry. See [Local data and security](../reference/data-security.md) for storage and backup guidance.
