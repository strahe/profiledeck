# Claude Code Usage and Cost

ProfileDeck reports usage from readable local Claude Code project and subagent sessions. Shared fork history is counted once. Activity cannot be assigned to a Profile, saved login, or account. Desktop syncs while running, with an independent interval in Claude Code Settings.

## Sync and view usage

```bash
profiledeck-cli usage sync claude-code
profiledeck-cli usage sync claude-code --claude-dir /path/to/claude-home
profiledeck-cli usage report --provider claude-code --range 30d
profiledeck-cli usage summary --provider claude-code --json
```

The log directory defaults to `CLAUDE_CONFIG_DIR`, then `~/.claude`. `--claude-dir` changes only where logs are read. Sync does not read credentials or change source files. Repeating it does not double-count requests. Report ranges are `today`, `7d`, `30d`, and `all`; dates use your local time zone.

## Understand incomplete usage

When a final record is missing, verified input and cache tokens still contribute. Totals show “at least” and missing output shows “unknown”. A later sync can complete the request. Restored history copies with input, output, and cache token totals cleared are skipped without a warning. Invalid candidates are skipped; contradictory valid final records are excluded from token and cost totals. The report shows incomplete and conflicting request counts. Data notes name the rejected usage fields, such as a cache write total that disagrees with its duration counts. Rejected-record counts cover all scanned logs; request and cost counts follow the selected date range.

If a file is truncated or previously imported usage is rewritten, the accepted report is retained and sync shows a warning. Unreadable, unsupported, deleted, or unrecorded activity cannot be recovered. Claude Desktop and cloud sessions are not included. Deleting the Claude Code Provider also deletes its saved usage; explicit sync can reimport logs that remain available.

## Understand estimates

Estimates use [Claude Standard API prices](https://platform.claude.com/docs/en/about-claude/pricing), matched by exact model and date. Five-minute and one-hour cache writes have separate rates. Unknown cache duration, unsupported models, and unsupported pricing modes can leave cost partial or unknown. Only known components contribute to the subtotal. The report distinguishes missing final output counts, missing cache duration, and unavailable cache rates. Missing final output leaves only its output cost undetermined when input and cache rates are available.

The first available estimate retains its selected rates when missing usage is completed. Price updates do not recalculate classified estimates. The bundled price list works offline; `usage pricing auto off` disables price-list updates. Reports do not connect to a billing service or upload usage.

These figures are not invoices, subscription charges, or account limits. Saved usage excludes raw request IDs, prompts, completions, credentials, and log content; see [Local Data and Security](../reference/data-security.md).
