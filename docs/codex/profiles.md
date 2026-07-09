# Codex Profiles

Codex profiles restore the two user-level files that matter for day-to-day switching:

- `$CODEX_HOME/config.toml`
- `$CODEX_HOME/auth.json`

ProfileDeck does not split or move `sessions/`, logs, skills, or other Codex state. Those remain shared under the same `CODEX_HOME`.

Internally, a profile stores the full desired `config.toml` content and binds to a hidden auth credential. A credential stores the latest desired `auth.json` payload and may be shared by multiple profiles. Codex `tokens.account_id` is display metadata only; it is never used as a ProfileDeck identity or merge key.

## Requirements

Codex must use file credentials. If `$CODEX_HOME/auth.json` is missing, add this to `$CODEX_HOME/config.toml` and login again:

```toml
cli_auth_credentials_store = "file"
```

Then run:

```bash
codex login
```

## Create a profile from current files

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
```

`create` requires both `config.toml` and `auth.json`. It stores the full desired `config.toml` content and creates a new hidden credential for the current `auth.json` payload.

## Fork or sync a profile

Forking copies an existing profile. Choose whether the fork shares the source credential lifecycle or starts with an independent copied credential:

```bash
profiledeck codex profile fork work client --auth-binding share-parent
profiledeck codex profile fork work client-isolated --auth-binding copy-new
```

Sync updates an existing profile from current Codex files:

```bash
profiledeck codex profile sync work
profiledeck codex profile sync work --auth-update update-shared
profiledeck codex profile sync work --auth-update fork-new
```

When a profile shares a hidden credential with another profile and `auth.json` changed, choose explicitly between updating the shared credential or forking this profile to a new credential.

## Switch to a profile

```bash
profiledeck plan codex work
profiledeck switch codex work --yes
```

`plan` is read-only. `switch` writes `config.toml` and `auth.json` only through the transaction pipeline.

## Create another login profile

1. Login with the other Codex account so `$CODEX_HOME/auth.json` represents that login.
2. Create it under a different profile:

```bash
profiledeck codex profile create personal
```

After both profiles exist:

```bash
profiledeck switch codex work --yes
profiledeck switch codex personal --yes
```
