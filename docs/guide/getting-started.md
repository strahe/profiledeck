# Getting Started

ProfileDeck provides a Go CLI and a macOS Desktop app. Command examples assume the `profiledeck` executable is available on `PATH`.

## Build from source

```bash
make build
```

The binary is written to `bin/profiledeck`.

Command examples use `profiledeck` as the executable name. Install the binary or add `bin/` to your shell path before following the examples from a source checkout.

## Development shortcuts

| Command | Purpose |
| --- | --- |
| `make fmt` | Format all Go packages with gofumpt and gci. |
| `make lint` | Run read-only Go formatting and static-analysis checks. |
| `make test` | Run `go test ./...`. |
| `make build` | Build `bin/profiledeck` from `cmd/profiledeck`. |
| `make core-check` | Run the CLI/core lint, tests, and build. |
| `make desktop-check` | Run the Wails boundary, bindings, frontend, build, and Desktop tests. |
| `make docs-check` | Install documentation dependencies and build the documentation site. |
| `make check` | Run the complete core, Desktop, and documentation gate. |
| `make clean` | Remove local build output. |

`make check` is read-only for tracked source and generated bindings. Run `make fmt` or `make desktop-bindings` explicitly when those files need updating. Full validation requires macOS and compatible `golangci-lint` v2 and `wails3` executables on `PATH`; use `make core-check` for the portable CLI/core gate on other platforms.

Documentation tasks also use Make targets:

```bash
make docs-install
make docs-dev
make docs-build
make docs-preview
```

## Initialize ProfileDeck

```bash
profiledeck init
profiledeck status
```

`init` creates ProfileDeck's local data and backup folders. By default, the data directory is:

```text
<os-user-config-dir>/profiledeck
```

The application database is stored at:

```text
<os-user-config-dir>/profiledeck/profiledeck.db
```

Use `--config-dir` to place ProfileDeck data under a different user config directory:

```bash
profiledeck --config-dir /tmp/profiledeck-dev init
```

ProfileDeck restricts access to its local data and backup folders on POSIX systems when possible.

Distributed macOS arm64 Alpha builds can check for updates while ProfileDeck is running. See [Desktop Updates](/guide/updates) to manage automatic checks and install a downloaded update.

## First Codex profile

```bash
profiledeck codex detect
profiledeck codex profile create work
profiledeck codex profile list
profiledeck plan codex work
profiledeck switch codex work --yes
```

`codex profile create` reads the current Codex home and requires both `config.toml` and `auth.json`. Resolution order is:

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

For Codex profile switching, Codex must have `$CODEX_HOME/auth.json`.

The first Profile creates and activates a Config Set named `shared`. Later Profiles reuse the active Config Set by default, so creating another login usually requires only logging in with that account and running `codex profile create` again. Use `--new-config-set <id>` when the current `config.toml` should become an independent Config Set.

## First Antigravity Profile

Sign in through Antigravity agy v2, then run:

```bash
profiledeck antigravity detect
profiledeck antigravity profile create work
profiledeck plan antigravity work
profiledeck switch antigravity work --yes
```

ProfileDeck supports only the agy v2 consumer OAuth login. It does not start OAuth login or import Antigravity Manager data.
