#!/bin/bash
set -euo pipefail
action="${1-}"
if [[ -n "$action" ]]; then
  shift
fi
input=""
profile=""
keychain=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --input) input="${2-}"; shift 2 ;;
    --profile) profile="${2-}"; shift 2 ;;
    --keychain) keychain="${2-}"; shift 2 ;;
    *) echo "Could not run notarization. Check the release command and try again." >&2; exit 1 ;;
  esac
done
if [[ "$action" != "check" && "$action" != "submit" ]]; then
  echo "Could not run notarization. Choose the check or submit action." >&2
  exit 1
fi
if [[ -z "$profile" || ( "$action" == "submit" && -z "$input" ) ]]; then
  echo "Could not run notarization. A notary profile and input file are required." >&2
  exit 1
fi
umask 077
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-notary.XXXXXX")"
cleanup() {
  rm -rf -- "$temp_dir"
}
trap cleanup EXIT HUP INT TERM
credentials=(--keychain-profile "$profile")
if [[ -n "$keychain" ]]; then
  credentials+=(--keychain "$keychain")
fi
result="$temp_dir/notary-result"
if [[ "$action" == "check" ]]; then
  if ! xcrun notarytool history "${credentials[@]}" --output-format json >"$result" 2>&1; then
    echo "Could not use the notarization profile. Check its credentials and Keychain access." >&2
    exit 1
  fi
  echo "The notarization profile is ready."
  exit 0
fi
if ! xcrun notarytool submit "$input" "${credentials[@]}" --wait --output-format json >"$result" 2>&1; then
  echo "Could not submit the release for notarization. Check the notary credentials and try again." >&2
  exit 1
fi
status="$(plutil -extract status raw -o - "$result" 2>/dev/null || true)"
if [[ "$status" != "Accepted" ]]; then
  echo "Apple did not accept the release for notarization. Review it in notarytool history before retrying." >&2
  exit 1
fi
printf 'Notarization accepted for %s.\n' "${input##*/}"
