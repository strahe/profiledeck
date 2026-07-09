# Getting Started

ProfileDeck is currently a Go CLI. Command examples assume the `profiledeck` executable is available on `PATH`.

## Build from source

```bash
make build
```

The binary is written to `bin/profiledeck`.

Command examples use `profiledeck` as the executable name. Install the binary or add `bin/` to your shell path before following the examples from a source checkout.

## Development shortcuts

| Command | Purpose |
| --- | --- |
| `make fmt` | Format Go packages with `go fmt ./...`. |
| `make vet` | Run `go vet ./...`. |
| `make test` | Run `go test ./...`. |
| `make build` | Build `bin/profiledeck` from `cmd/profiledeck`. |
| `make check` | Run format, vet, tests, and build. |
| `make clean` | Remove local build output. |

Documentation uses npm scripts:

```bash
cd docs
npm install
npm run dev
npm run build
npm run preview
```

## Initialize ProfileDeck

```bash
profiledeck init
profiledeck status
```

`init` creates the ProfileDeck runtime directory and SQLite application store. By default, the runtime root is:

```text
<os-user-config-dir>/profiledeck
```

The application database is stored at:

```text
<os-user-config-dir>/profiledeck/profiledeck.db
```

Use `--config-dir` to place the runtime under a different user config directory:

```bash
profiledeck --config-dir /tmp/profiledeck-dev init
```

ProfileDeck creates runtime, backup, export, log, and lock directories with restrictive permissions on POSIX systems when possible.

## First Codex profile

```bash
profiledeck codex detect
profiledeck codex profile capture work
profiledeck codex profile list
profiledeck plan codex work
profiledeck switch codex work --yes
```

`codex profile capture` reads the current Codex home. Resolution order is:

1. `--codex-dir`
2. `CODEX_HOME`
3. `~/.codex`

For full account switching, Codex must have `$CODEX_HOME/auth.json`.
