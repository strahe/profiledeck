#!/bin/bash
set -euo pipefail
directory=""
version=""
short_version=""
build_number=""
product=""
binary=""
bundle_id=""
min_version=""
public_key=""
updater=""
signature=""
installer=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --directory) directory="${2-}"; shift 2 ;;
    --version) version="${2-}"; shift 2 ;;
    --short-version) short_version="${2-}"; shift 2 ;;
    --build-number) build_number="${2-}"; shift 2 ;;
    --product) product="${2-}"; shift 2 ;;
    --binary) binary="${2-}"; shift 2 ;;
    --bundle-id) bundle_id="${2-}"; shift 2 ;;
    --min-version) min_version="${2-}"; shift 2 ;;
    --public-key) public_key="${2-}"; shift 2 ;;
    --updater) updater="${2-}"; shift 2 ;;
    --signature) signature="${2-}"; shift 2 ;;
    --installer) installer="${2-}"; shift 2 ;;
    *) echo "Could not verify the macOS release. Check the release command and try again." >&2; exit 1 ;;
  esac
done
for value in "$directory" "$version" "$short_version" "$build_number" "$product" "$binary" \
  "$bundle_id" "$min_version" "$public_key" "$updater" "$signature" "$installer"; do
  if [[ -z "$value" ]]; then
    echo "Could not verify the macOS release. Required release information is missing." >&2
    exit 1
  fi
done
umask 077
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-verify.XXXXXX")"
private_log="$temp_dir/command.log"
mount_path="$temp_dir/dmg"
mounted="false"
cleanup() {
  if [[ "$mounted" == "true" ]]; then
    diskutil eject "$mount_path" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$temp_dir"
}
trap cleanup EXIT HUP INT TERM
run_private() {
  local message="$1"
  shift
  if ! "$@" >"$private_log" 2>&1; then
    : >"$private_log"
    echo "$message" >&2
    return 1
  fi
}
plist_value() {
  local plist="$1"
  local key="$2"
  if ! plutil -extract "$key" raw -o - "$plist" >"$private_log" 2>&1; then
    : >"$private_log"
    echo "The application metadata is incomplete. Rebuild the release and try again." >&2
    return 1
  fi
  cat "$private_log"
  : >"$private_log"
}
verify_app() {
  local app_path="$1"
  local executable="$app_path/Contents/MacOS/$binary"
  local plist="$app_path/Contents/Info.plist"
  if [[ ! -f "$executable" || ! -x "$executable" ]]; then
    echo "The application executable is missing or cannot run. Rebuild the release and try again." >&2
    return 1
  fi
  run_private "Could not inspect the application architectures. Rebuild the release and try again." \
    xcrun lipo -archs "$executable"
  architectures="$(tr ' ' '\n' <"$private_log" | sed '/^$/d' | sort | tr '\n' ' ')"
  if [[ "$architectures" != "arm64 x86_64 " ]]; then
    echo "The application is not a Universal macOS build. Rebuild the release and try again." >&2
    return 1
  fi
  keys=(CFBundleIdentifier CFBundleExecutable CFBundleIconFile CFBundleIconName CFBundleShortVersionString CFBundleVersion LSMinimumSystemVersion CFBundleInfoDictionaryVersion)
  values=("$bundle_id" "$binary" icons appicon "$short_version" "$build_number" "$min_version" 6.0)
  for ((index = 0; index < ${#keys[@]}; index++)); do
    actual="$(plist_value "$plist" "${keys[$index]}")" || return 1
    if [[ "$actual" != "${values[$index]}" ]]; then
      echo "The application metadata does not match this release. Rebuild it and try again." >&2
      return 1
    fi
  done
  for resource in Assets.car icons.icns; do
    if [[ ! -s "$app_path/Contents/Resources/$resource" ]]; then
      echo "The application icon resources are incomplete. Rebuild the release and try again." >&2
      return 1
    fi
  done
  run_private "The application signature could not be verified. Sign the release again." \
    codesign --verify --deep --strict --verbose=2 "$app_path"
  run_private "The application signature details could not be inspected. Sign the release again." \
    codesign --display --verbose=4 "$app_path"
  for required in "Authority=Developer ID Application:" runtime "Timestamp="; do
    if ! grep -Fq "$required" "$private_log"; then
      echo "The application signature is incomplete. Sign the release again." >&2
      return 1
    fi
  done
  run_private "The application notarization ticket could not be verified. Notarize the release again." \
    xcrun stapler validate "$app_path"
  run_private "Gatekeeper did not accept the application. Review its signing and notarization." \
    spctl --assess --type execute --verbose=4 "$app_path"
}
updater_path="$directory/$updater"
signature_path="$directory/$signature"
installer_path="$directory/$installer"
run_private "The update signature could not be verified. Sign the update again." \
  go run ./scripts/releasetool verify-update-signature \
  --public-key "$public_key" --artifact "$updater_path" --signature "$signature_path"
extract_path="$temp_dir/updater"
mkdir -p "$extract_path"
run_private "The update archive could not be opened. Rebuild the release and try again." \
  ditto -x -k "$updater_path" "$extract_path"
entry_count="$(find "$extract_path" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')"
app_name="$product.app"
if [[ "$entry_count" != "1" || ! -d "$extract_path/$app_name" || -L "$extract_path/$app_name" ]]; then
  echo "The update archive must contain one application. Rebuild the release and try again." >&2
  exit 1
fi
verify_app "$extract_path/$app_name"
run_private "The installer image is damaged. Rebuild the release and try again." hdiutil verify "$installer_path"
run_private "The installer signature could not be verified. Sign the release again." \
  codesign --verify --verbose=2 "$installer_path"
run_private "The installer signature details could not be inspected. Sign the release again." \
  codesign --display --verbose=4 "$installer_path"
for required in "Authority=Developer ID Application:" "Timestamp="; do
  if ! grep -Fq "$required" "$private_log"; then
    echo "The installer signature is incomplete. Sign the release again." >&2
    exit 1
  fi
done
run_private "The installer notarization ticket could not be verified. Notarize the release again." \
  xcrun stapler validate "$installer_path"
run_private "Gatekeeper did not accept the installer. Review its signing and notarization." \
  spctl --assess --type open --context context:primary-signature --verbose=4 "$installer_path"
mkdir "$mount_path"
mounted="true"
run_private "The installer image could not be mounted. Rebuild the release and try again." \
  diskutil image attach --readOnly --nobrowse --mountPoint "$mount_path" "$installer_path"
if [[ ! -L "$mount_path/Applications" || "$(readlink "$mount_path/Applications")" != "/Applications" ]]; then
  echo "The installer is missing its Applications shortcut. Rebuild the release and try again." >&2
  exit 1
fi
verify_app "$mount_path/$app_name"
run_private "The installer image could not be ejected. Eject it before retrying." diskutil eject "$mount_path"
mounted="false"

printf 'Verified macOS release %s (build %s).\n' "$version" "$build_number"
