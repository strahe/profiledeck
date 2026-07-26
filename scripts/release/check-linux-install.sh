#!/usr/bin/env bash
set -euo pipefail

version="${1-}"
desktop_name="${2-}"
if [[ -z "$version" || ! "$desktop_name" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
  echo "Could not verify the installed Linux package: the release identity is missing." >&2
  exit 1
fi

fail() {
  echo "Could not verify the installed Linux package: $1" >&2
  exit 1
}

verify_file() {
  local path="$1"
  local mode="$2"
  if [[ ! -s "$path" || ! -f "$path" || -L "$path" ]]; then
    fail "$path is not a non-empty regular file."
  fi
  if [[ "$(stat -c '%a:%U:%G' "$path")" != "$mode:root:root" ]]; then
    fail "$path does not have the expected mode and owner."
  fi
}

desktop_path="/usr/bin/$desktop_name"
verify_file /usr/bin/profiledeck 755
verify_file "$desktop_path" 755
verify_file /usr/share/applications/profiledeck.desktop 644
verify_file /usr/share/icons/hicolor/1024x1024/apps/profiledeck.png 644
verify_file /usr/share/licenses/profiledeck/LICENSE 644

for executable in /usr/bin/profiledeck "$desktop_path"; do
  executable_type="$(file -b "$executable")"
  if [[ "$executable_type" != *"ELF 64-bit LSB"* || "$executable_type" != *"x86-64"* ]]; then
    fail "$executable is not a Linux amd64 ELF binary."
  fi
done
if [[ "$(/usr/bin/profiledeck --version)" != "profiledeck version $version" ]]; then
  fail "the CLI version does not match the release."
fi
if ! grep -Fxq "Exec=$desktop_name" /usr/share/applications/profiledeck.desktop ||
  ! grep -Fxq 'Icon=profiledeck' /usr/share/applications/profiledeck.desktop; then
  fail "the desktop entry does not launch ProfileDeck."
fi
if [[ "$(file -b /usr/share/icons/hicolor/1024x1024/apps/profiledeck.png)" != PNG\ image\ data,* ]]; then
  fail "the installed application icon is not a PNG."
fi

printf 'Verified installed Linux package %s.\n' "$version"
