# ProfileDeck

Safe profile switching for AI coding tools.

ProfileDeck is a Go CLI and macOS desktop app with a Codex-first workflow. A Codex Profile saves one login and one reusable Config Set, so you can switch accounts and settings together, preserve valid changes made in Codex, and recover from interrupted switches. ProfileDeck also turns local Codex activity into usage reports and can check current ChatGPT Codex limits for individual Profiles.

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

Wails-native Desktop development is available through the root Taskfile:

```bash
wails3 build
wails3 dev
wails3 task run
```

The Taskfile owns only the cross-platform Wails build, run, bindings, frontend, and hot-reload lifecycle. The Makefile remains the authoritative interface for tests, linting, generated-binding checks, documentation, and CI validation.

During desktop development, set `PROFILEDECK_CONFIG_DIR` to a temporary directory to avoid touching the normal ProfileDeck runtime. Set `PROFILEDECK_CODEX_DIR` as well when testing against an isolated Codex working copy.

```bash
PROFILEDECK_CONFIG_DIR=/tmp/profiledeck-dev PROFILEDECK_CODEX_DIR=/tmp/profiledeck-codex wails3 dev
```

Global Desktop settings let you choose the language and appearance. Appearance defaults to System, and the Agent sidebar remembers whether it is expanded. The sidebar lists Codex and keeps global Settings and Diagnostics in its footer. Diagnostics shows only issues that need attention and offers recovery when it is safe.

Codex settings let you choose how often Usage reports update and whether each Profile refreshes limits or renews its sign-in automatically. Per-Profile options also appear on the Profile detail page. Usage reports update while ProfileDeck is open or hidden in the tray; the available intervals are 5, 15, 30, and 60 seconds, with 15 seconds as the default. Automatic limit refresh and sign-in renewal are off by default.

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

Saved Codex logins and Config Sets are sensitive. ProfileDeck stores them locally in `profiledeck.db`; switch backups may contain previous `auth.json` and `config.toml` content.

Codex Profile exports are sensitive backups. They contain complete Codex sign-in data and settings in a JSON file with `0600` permissions on POSIX systems. Keep the file private and outside the ProfileDeck data directory before deleting a development database.

Usage analysis stays local and aggregate-only. The Desktop Usage page defaults to an API-equivalent cost trend and can switch to token trends; it never guesses which Profile or ChatGPT account produced a session. Limit checks are separate from Usage reports. Optional per-Profile limit refresh and sign-in renewal are off by default and run only while ProfileDeck is open or hidden in the tray.
