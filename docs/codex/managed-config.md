# Codex Managed Config

Managed config mode changes only selected keys in `$CODEX_HOME/config.toml`:

- `model`
- `model_provider`
- `openai_base_url`

It is useful for model and base URL switching when you do not need to capture Codex auth.

## Create or update a managed profile

```bash
profiledeck codex profile set work --model gpt-5.3-codex
```

`model_provider` defaults to `openai` when omitted:

```bash
profiledeck codex profile set work \
  --model gpt-5.3-codex \
  --model-provider openai
```

Set a custom OpenAI-compatible base URL:

```bash
profiledeck codex profile set relay \
  --model gpt-5.3-codex \
  --openai-base-url https://api.example.com/v1
```

## Desired-state semantics

`profile set` writes a complete ProfileDeck desired state for the managed keys. If `--openai-base-url` is omitted, ProfileDeck removes its managed `openai_base_url` from `config.toml` during switch.

Non-managed Codex config keys and sections are preserved, but TOML comments and ordering may be rewritten because the file is parsed and re-encoded.

## Bind an existing account

You can bind a managed config profile to a stored Codex account:

```bash
profiledeck codex profile set work-fast \
  --model gpt-5.3-codex \
  --account work
```

The account must already exist through `codex profile capture` or `codex account import`.

## Apply

```bash
profiledeck plan codex work-fast
profiledeck switch codex work-fast --yes
```

When a profile has an auth target, switch also writes `$CODEX_HOME/auth.json`.
