#!/usr/bin/env bash
set -euo pipefail

expected_commit="${1-}"
if [[ ! "$expected_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Could not build the release: the requested source commit is invalid." >&2
  exit 1
fi

actual_commit="$(git rev-parse 'HEAD^{commit}' 2>/dev/null || true)"
if [[ "$actual_commit" != "$expected_commit" ]]; then
  echo "Could not build the release: the working tree does not match the requested commit." >&2
  exit 1
fi
if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "Could not build the release: the working tree contains local changes." >&2
  exit 1
fi
