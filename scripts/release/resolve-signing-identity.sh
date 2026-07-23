#!/bin/bash
set -euo pipefail
requested=""
keychain=""
output="fingerprint"
interactive="false"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --requested) requested="${2-}"; shift 2 ;;
    --keychain) keychain="${2-}"; shift 2 ;;
    --output) output="${2-}"; shift 2 ;;
    --interactive) interactive="true"; shift ;;
    *) echo "Could not resolve the signing identity. Check the release command and try again." >&2; exit 1 ;;
  esac
done
if [[ "$output" != "fingerprint" && "$output" != "name" ]]; then
  echo "Could not resolve the signing identity. Choose fingerprint or name output." >&2
  exit 1
fi
umask 077
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-identity.XXXXXX")"
cleanup() {
  rm -rf -- "$temp_dir"
}
trap cleanup EXIT HUP INT TERM
identity_log="$temp_dir/security.log"
identity_list="$temp_dir/identities"
security_args=(find-identity -v -p codesigning)
if [[ -n "$keychain" ]]; then
  security_args+=("$keychain")
fi
if ! security "${security_args[@]}" >"$identity_log" 2>&1; then
  echo "Could not inspect Developer ID signing identities. Check Keychain access and try again." >&2
  exit 1
fi
awk '
  /Developer ID Application:/ {
    fingerprint = ""
    for (field = 1; field <= NF; field++) {
      if ($field ~ /^[0-9A-Fa-f]{40}$/) {
        fingerprint = toupper($field)
        break
      }
    }
    quote = index($0, "\"")
    if (fingerprint == "" || quote == 0) next
    rest = substr($0, quote + 1)
    end_quote = index(rest, "\"")
    if (end_quote == 0) next
    name = substr(rest, 1, end_quote - 1)
    print fingerprint "\t" name
  }
' "$identity_log" | sort -u >"$identity_list"
fingerprints=()
names=()
while IFS=$'\t' read -r fingerprint name; do
  [[ -n "$fingerprint" && -n "$name" ]] || continue
  fingerprints+=("$fingerprint")
  names+=("$name")
done <"$identity_list"
selected=-1
if [[ -n "$requested" ]]; then
  requested_upper="$(printf '%s' "$requested" | tr '[:lower:]' '[:upper:]')"
  for ((index = 0; index < ${#fingerprints[@]}; index++)); do
    if [[ "${fingerprints[$index]}" == "$requested_upper" || "${names[$index]}" == "$requested" ]]; then
      if [[ "$selected" -ge 0 ]]; then
        echo "The requested signing identity is ambiguous. Use its full SHA-1 fingerprint." >&2
        exit 1
      fi
      selected=$index
    fi
  done
  if [[ "$selected" -lt 0 ]]; then
    echo "The requested signing identity is unavailable. Check Keychain access or choose another identity." >&2
    exit 1
  fi
elif [[ "${#fingerprints[@]}" -eq 1 ]]; then
  selected=0
elif [[ "$interactive" == "true" && "${#fingerprints[@]}" -gt 1 ]]; then
  if [[ ! -r /dev/tty || ! -w /dev/tty ]]; then
    echo "Multiple signing identities are available. Set SIGN_IDENTITY to a full SHA-1 fingerprint." >&2
    exit 1
  fi
  {
    echo "Multiple Developer ID Application identities are available:"
    echo
    for ((index = 0; index < ${#fingerprints[@]}; index++)); do
      fingerprint="${fingerprints[$index]}"
      printf '%d. %s\n   SHA-1: %s...%s\n' \
        "$((index + 1))" "${names[$index]}" "${fingerprint:0:8}" "${fingerprint: -6}"
    done
    echo
  } >/dev/tty
  while true; do
    printf 'Select an identity [1-%d], or q to cancel: ' "${#fingerprints[@]}" >/dev/tty
    IFS= read -r choice </dev/tty || choice="q"
    if [[ "$choice" == "q" || "$choice" == "Q" ]]; then
      echo "Signing identity selection was cancelled." >&2
      exit 1
    fi
    if [[ "$choice" =~ ^[0-9]+$ ]] && ((choice >= 1 && choice <= ${#fingerprints[@]})); then
      selected=$((choice - 1))
      break
    fi
    echo "Enter one of the listed numbers, or q to cancel." >/dev/tty
  done
else
  echo "Expected one Developer ID Application identity. Set SIGN_IDENTITY to a full SHA-1 fingerprint." >&2
  exit 1
fi

if [[ "$output" == "name" ]]; then
  printf '%s\n' "${names[$selected]}"
else
  printf '%s\n' "${fingerprints[$selected]}"
fi
