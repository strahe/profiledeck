#!/bin/bash
set -euo pipefail
plist="${1-}"
# A valid plist may still be unsafe to ship when release template values remain.
if [[ -z "$plist" || ! -f "$plist" || ! -r "$plist" ]]; then
  echo "The application metadata is incomplete. Rebuild the application package and try again." >&2
  exit 1
fi
if LC_ALL=C grep -Eq '@[A-Z][A-Z0-9_]*@' "$plist"; then
  echo "The application metadata is incomplete. Rebuild the application package and try again." >&2
  exit 1
fi
