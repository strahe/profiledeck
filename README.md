# ProfileDeck

Safe profile switching for AI coding tools.

ProfileDeck is currently a Go CLI and macOS desktop MVP with a Codex-first workflow. A Codex Profile combines a hidden login credential with a reusable Config Set, while guarded transactions switch their on-disk working copies, preserve valid local changes, and support recovery. ProfileDeck imports local Codex session logs for offline analysis and can read current ChatGPT Codex limits for individual Profiles.

## Documentation

The full documentation is in `docs/` and is built with VitePress through Make targets.

```bash
make docs-install
make docs-dev
make docs-build
make docs-preview
```

English is served at `/`; Simplified Chinese is served at `/zh/`.

GitHub Pages deployment is handled by `.github/workflows/docs.yml`. Pull requests build the docs; pushes to `main` deploy the generated site after the repository Pages source is set to GitHub Actions. The workflow uses `VITEPRESS_BASE` from repository variables when present, otherwise it defaults to `/<repository-name>/` for project Pages.

## Build

```bash
make build
```

The binary is written to `bin/profiledeck`.

Command examples assume `profiledeck` is available on `PATH`. When working from a source checkout, install the binary or add `bin/` to your shell path before following the examples.

Useful shortcuts:

```bash
make fmt
make lint
make core-check
make desktop-check
make docs-check
make check
make clean
```

`make check` is the full project gate. Use the component checks for faster feedback while working in one area. Formatting is an explicit mutating command; the check targets do not rewrite tracked source or generated bindings.

Full local validation requires macOS, compatible `golangci-lint` v2 and `wails3` executables on `PATH`, plus Go and a Node/npm version supported by the lockfiles. Use `make core-check` for the portable CLI/core gate on other platforms.

macOS desktop builds target macOS 14.0 by default. Override it with `MACOS_MIN_VERSION=<version>` when running desktop Make targets.

During desktop development, set `PROFILEDECK_CONFIG_DIR` to a temporary directory when you need to avoid touching the normal ProfileDeck runtime.

Global Desktop settings contain the language preference: Auto, Simplified Chinese, or English. Codex-specific settings contain the local usage-sync interval and per-Profile account-limit automation. Usage sync runs while ProfileDeck is open or hidden in the tray; the available intervals are 5, 15, 30, and 60 seconds, with 15 seconds as the default. Account-limit refresh and login keepalive are off by default.

## Codex Quick Start

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
profiledeck codex profile list
profiledeck codex config-set list
profiledeck codex profile save-current
profiledeck codex profile export --output ./profiledeck-codex-profiles.json
profiledeck plan codex work
profiledeck switch codex work --yes
profiledeck usage sync codex
profiledeck usage report --range 7d
```

Codex profile switching requires file credentials. If `$CODEX_HOME/auth.json` is missing, set `cli_auth_credentials_store = "file"` in `$CODEX_HOME/config.toml` and run `codex login` again.

Stored Codex auth and complete Config Set payloads are sensitive. ProfileDeck stores them locally in `profiledeck.db`; switch backups may contain previous `auth.json` and `config.toml` content.

Codex Profile exports are explicit sensitive backups. They contain raw `auth.json` and complete `config.toml` payloads in a deterministic JSON file with `0600` permissions on POSIX systems. Keep the file outside the runtime directory before deleting a development database.

Usage analysis stays local and aggregate-only. The Desktop Usage page defaults to an API-equivalent cost trend and can switch to token trends; it never infers which Profile, credential, or ChatGPT account produced a session. The Desktop Profiles page reads one saved credential at a time on manual refresh. Optional per-Profile automation uses the installed `codex app-server`, runs serially while ProfileDeck remains open or in the tray, and deduplicates shared hidden credentials. Limit snapshots remain in process memory and do not change the offline usage report.
