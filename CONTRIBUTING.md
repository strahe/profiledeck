# Contributing to ProfileDeck

Use this guide to prepare and submit changes to ProfileDeck.

## Before You Start

- Search the [issues](https://github.com/strahe/profiledeck/issues) before starting work.
- Open an issue before proposing a substantial feature, dependency, data-format change, or supported-platform change.
- Report security vulnerabilities through [SECURITY.md](SECURITY.md), not through an issue.

## Development Setup

Install these prerequisites:

- Go 1.26, as declared in `go.mod`
- Node.js 26 with npm, as used by the CI workflows
- Make
- The `golangci-lint` and `wails3` versions declared in `Makefile` when working on the full Desktop application

Clone the repository and install the JavaScript dependencies:

```bash
git clone https://github.com/strahe/profiledeck.git
cd profiledeck
go mod download
npm --prefix desktop/frontend ci
npm --prefix docs ci
```

Desktop builds require platform development libraries. The Linux packages used by CI are listed in [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

## Make Changes

- Keep each change focused on one problem.
- Follow the style and structure of the surrounding code.
- Add tests for meaningful behavior and regressions.
- Update documentation when commands, setup, behavior, or supported workflows change.
- Use synthetic or redacted test data. Do not commit credentials, personal data, runtime databases, exports, backups, logs, or local build output.

## Validate Changes

Use the Makefile targets that match the change:

```bash
make core-check
make desktop-check
make docs-check
```

Run the complete project check before submitting when the required platform dependencies are available:

```bash
make check
```

If a check cannot run in your environment, state that clearly in the pull request.

## Submit a Pull Request

- Use a short branch name with a conventional prefix such as `feat/`, `fix/`, or `docs/`.
- Use a concise Conventional Commit-style title.
- Link the relevant issue.
- Describe the problem, the observable result, and the validation performed.
- Keep unrelated formatting, dependency, and documentation changes out of the pull request.
