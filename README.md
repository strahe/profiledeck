# ProfileDeck

Safe profile switching for AI coding tools.

ProfileDeck is currently a Go CLI with a Codex-first workflow. It can capture Codex user config and file-based auth, switch profiles through a guarded transaction pipeline, import local Codex token usage, and recover interrupted switch operations.

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

## Codex Quick Start

```bash
profiledeck init
profiledeck codex detect
profiledeck codex profile capture work
profiledeck plan codex work
profiledeck switch codex work --yes
```

Full Codex account switching requires file credentials. If `$CODEX_HOME/auth.json` is missing, set `cli_auth_credentials_store = "file"` in `$CODEX_HOME/config.toml` and run `codex login` again.

Stored Codex auth is sensitive. ProfileDeck stores it locally in `profiledeck.db`, and switch backups may contain previous `auth.json` content.
