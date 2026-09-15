# Grok Build Usage and Cost

ProfileDeck reads local Grok Build session records to show token usage, activity, static API-equivalent cost estimates, and cost reported by Grok Build. Reports stay offline and do not assign activity to a Profile, saved login, or account.

## Sync in the Desktop app

The Desktop app syncs after startup and continues while ProfileDeck is open or in the menu bar.

To change the interval, open **Grok Build → Settings → Usage reports → Update frequency** and choose 15 seconds, 30 seconds, 1 minute, 2 minutes, or 5 minutes. The default is 1 minute. Codex and Grok Build use separate intervals and sync status.

When session files have not changed, background sync checks their metadata without reading their contents. Normal appends read only a small integrity boundary and the new part of each file.

Background sync uses the existing Grok Build Provider. If it has not been created yet, open **Grok Build → Profiles** to create a Profile, or run an explicit CLI sync.

## Sync from the CLI

Run:

```bash
profiledeck-cli usage sync grok-build
```

ProfileDeck resolves Grok Home from `--grok-home`, then `GROK_HOME`, then `~/.grok`. If the Provider already exists, the resolved location must match its saved Grok Home. To use an explicit location:

```bash
profiledeck-cli --grok-home /path/to/grok-home usage sync grok-build
```

An existing Provider never silently switches to another Grok Home.

ProfileDeck reads ordinary files matching:

```text
<grok-home>/sessions/*/*/updates.jsonl
```

It does not follow symbolic links. Nested `subagents` records are excluded because Grok Build already includes successful child-agent usage in the completed parent turn.

You can repeat a sync safely; previously imported usage is not counted again. Records with missing, empty, or incomplete usage are skipped. If a file changes while it is being read, contains an oversized or malformed terminal record, or has an unrecognized terminal format, ProfileDeck leaves all new data from that file uncommitted. Background sync checks that file again after it changes; a CLI sync checks it immediately. The source file is never moved or changed.

New additive fields in Grok Build session records are ignored safely. Known fields still need their expected types and consistent token totals.

Copied fork history is counted once. When the same completed turn appears in multiple sessions with identical usage, ProfileDeck assigns it to one stable derived session without storing the original session identifier. Conflicting usage for that same turn leaves the affected file uncommitted until it changes or you run another CLI sync.

Deleting the Grok Build Provider also deletes its saved usage reports, import progress, and sync setting. Desktop background sync will not recreate a deleted Provider. Running this CLI sync is an explicit request: it can set up the Provider again and reimport usage still present in local session files.

## View a summary

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage summary --provider grok-build --json
```

The summary includes event count, input and output tokens, cached input, total tokens, API-equivalent cost when available, Grok-reported cost when available, and the number of events with unknown cost for each figure.

## View a report

```bash
profiledeck-cli usage report --provider grok-build
profiledeck-cli usage report --provider grok-build --range today
profiledeck-cli usage report --provider grok-build --range 30d --json
profiledeck-cli usage report --provider grok-build --range all
```

The default range is `7d`. Reports use your computer's local time zone and include token totals, session count, cache hit rate, API-equivalent and Grok-reported cost subtotals, coverage for both figures, model details, and sync status. Records without a timestamp are included in all-time totals and model details, reported separately, and excluded from the timeline.

## Understand cost figures

ProfileDeck uses the short-context xAI Standard API-equivalent prices included with the installed version:

| Model | Input | Cached input | Output |
| --- | ---: | ---: | ---: |
| `grok-4.5` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.5-build` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.5-latest` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.6` | $2.00 / 1M tokens | $0.50 / 1M tokens | $6.00 / 1M tokens |
| `grok-4.6-build` | $2.00 / 1M tokens | $0.50 / 1M tokens | $6.00 / 1M tokens |
| `grok-build-latest` | $2.00 / 1M tokens | $0.30 / 1M tokens | $6.00 / 1M tokens |

The prices come from [xAI pricing](https://docs.x.ai/developers/pricing). [Grok Build uses Grok 4.6](https://docs.x.ai/build/overview), so ProfileDeck treats the `grok-4.6-build` identifier found in current session records as pricing-equivalent to Grok 4.6. Aggregated session records do not show whether an individual request entered a long-context pricing tier, so ProfileDeck does not apply the 2× long-context multiplier.

When a session record includes cache-creation tokens, ProfileDeck keeps the event and reports a partial estimate because usage facts currently price only input, cached input, and output tokens.

When a completed turn contains `costUsdTicks`, ProfileDeck imports the per-model value as Grok-reported cost; 10,000,000,000 ticks equal US$1. A top-level value is used only when the turn contains exactly one model, so the same amount is never assigned to several models. Missing or nonpositive values remain unknown, and `costIsPartial` is shown as partial.

API-equivalent estimates and Grok-reported amounts remain separate and are never added together. Reports show the known subtotal and coverage when some completed turns are missing either figure. An unrecognized model keeps its token totals and Grok-reported amount but has unknown API-equivalent cost. Existing estimates are not recalculated when a later ProfileDeck version changes its built-in prices; facts with unknown API-equivalent cost can receive an estimate when their model becomes recognized.

The Grok Build Usage Limit panel may cover only activity since the process started or was last resumed, while ProfileDeck reports completed turns found in local session history for the selected dates. Their totals can therefore differ even when both values came from the same local session.

Neither figure is an invoice, credit balance, quota, or account charge. ProfileDeck does not contact xAI or a billing API when syncing or producing a report.

## Privacy limits

Usage storage excludes raw prompts, agent results, API keys, direct session identifiers, and full source-file paths. ProfileDeck does not upload usage data or use it for telemetry. See [Local data and security](../reference/data-security.md) for storage and backup guidance.
