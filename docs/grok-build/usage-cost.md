# Grok Build Usage and Cost

ProfileDeck reports token usage from local Grok Build sessions, API-equivalent estimates, and amounts reported by Grok Build. It cannot assign past activity to a Profile, saved login, or account. Desktop syncs while running; CLI users can sync on demand.

## Sync usage

```bash
profiledeck-cli usage sync grok-build
profiledeck-cli --grok-home /path/to/grok-home usage sync grok-build
```

The selected Grok Home must match the one already saved for this tool. Repeating a sync does not double-count imported usage or change source files. Incomplete records are skipped. If a file changes during reading or has conflicting usage, ProfileDeck leaves its new data unimported; a later CLI sync checks it again. Deleting the Grok Build Provider deletes its report and sync settings. An explicit CLI sync can reimport records still present locally.

## View a report

```bash
profiledeck-cli usage summary --provider grok-build
profiledeck-cli usage report --provider grok-build --range 30d
```

The report defaults to `7d`; other ranges are `today`, `30d`, and `all`. Dates use your local time zone. Undated records contribute to all-time totals but not the timeline. Add `--json` for machine-readable output.

## Understand cost figures

API-equivalent estimates use a price list based on [xAI Standard API prices](https://docs.x.ai/developers/pricing), matched by model and event date. `grok-4.7-build` uses the standard `grok-4.7` rate. Unverified model variants, old dates, and missing details for special rates can leave estimates unknown or partial. Grok-reported amounts are shown separately; missing amounts make their subtotal partial. The two figures are never added together. Session records do not identify long-context pricing, so estimates use short-context rates.

Existing estimates do not change after a price update; syncing again can fill previously unknown costs. The included price list works offline. `usage report` does not connect to a billing service or upload local usage.

Neither figure is an invoice, credits balance, quota, or account charge. [Credits checks](./profiles.md#check-credits) can cover a different period from local session reports. Saved usage excludes prompts, agent results, API keys, and full source-file paths; see [Local Data and Security](../reference/data-security.md).
