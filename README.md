# ProfileDeck

Safe profile switching for AI coding tools.

ProfileDeck is currently a Go CLI and macOS desktop MVP with a Codex-first workflow. It can create full-file Codex profiles from user config and file-based auth, switch profiles through a guarded transaction pipeline, import local Codex token usage, and recover interrupted switch operations.

## Documentation

The full documentation is in `docs/` and is built with VitePress.

```bash
cd docs
npm install
npm run dev
npm run build
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
make vet
make test
make check
make clean
```

Desktop checks are kept separate from the CLI/core check because the Wails version and release policy are still desktop-specific:

```bash
make desktop-check
```

macOS desktop builds target macOS 14.0 by default. Override it with `MACOS_MIN_VERSION=<version>` when running desktop Make targets.

During desktop development, set `PROFILEDECK_CONFIG_DIR` to a temporary directory when you need to avoid touching the normal ProfileDeck runtime.

The desktop app persists its language preference in ProfileDeck settings and currently supports Auto, Simplified Chinese, and English.

## Codex Quick Start

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile create work
profiledeck codex profile list
profiledeck plan codex work
profiledeck switch codex work --yes
```

Codex profile switching requires file credentials. If `$CODEX_HOME/auth.json` is missing, set `cli_auth_credentials_store = "file"` in `$CODEX_HOME/config.toml` and run `codex login` again.

Stored Codex auth is sensitive. ProfileDeck stores it locally in hidden credential records inside `profiledeck.db`, and switch backups may contain previous `auth.json` content.
