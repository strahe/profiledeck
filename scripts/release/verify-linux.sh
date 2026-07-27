#!/usr/bin/env bash
set -euo pipefail

version=""
portable=""
deb=""
rpm=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --version) version="${2-}"; shift 2 ;;
    --portable) portable="${2-}"; shift 2 ;;
    --deb) deb="${2-}"; shift 2 ;;
    --rpm) rpm="${2-}"; shift 2 ;;
    *) echo "Could not verify the Linux release: unsupported argument." >&2; exit 1 ;;
  esac
done

if [[ -z "$version" ]]; then
  echo "Could not verify the Linux release: the release version is missing." >&2
  exit 1
fi
for path in "$portable" "$deb" "$rpm"; do
  if [[ -z "$path" || ! -s "$path" || -L "$path" ]]; then
    echo "Could not verify the Linux release: a required asset is unavailable." >&2
    exit 1
  fi
done
for tool in tar dpkg-deb rpm file stat; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Could not verify the Linux release: required tool $tool is unavailable." >&2
    exit 1
  fi
done

fail() {
  echo "Could not verify the Linux release: $1" >&2
  exit 1
}

deb_version="$(go run ./scripts/releasetool contract --version "$version" --field linux-deb-version)"
rpm_version="$(go run ./scripts/releasetool contract --version "$version" --field linux-rpm-version)"
package_release="$(go run ./scripts/releasetool contract --version "$version" --field linux-package-release)"
updater_entry="$(go run ./scripts/releasetool contract --version "$version" --field linux-updater-entry)"
cli_install_path="/usr/bin/profiledeck-cli"
desktop_install_path="/usr/bin/$updater_entry"

umask 077
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-linux-verify.XXXXXX")"
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

if ! entries="$(tar -tzf "$portable")"; then
  fail "the updater archive could not be opened."
fi
if [[ "$entries" != "$updater_entry" ]]; then
  fail "the updater archive must contain one Desktop executable."
fi
portable_root="$work_dir/portable"
mkdir "$portable_root"
if ! tar --extract --gzip --file="$portable" --directory="$portable_root" \
  --same-permissions -- "$updater_entry"; then
  fail "the updater executable could not be extracted."
fi
portable_executable="$portable_root/$updater_entry"
if [[ ! -s "$portable_executable" || ! -f "$portable_executable" ||
      -L "$portable_executable" || "$(stat -c '%a' "$portable_executable")" != "755" ]]; then
  fail "the updater archive does not contain one regular 0755 executable."
fi
portable_type="$(file -b "$portable_executable")"
if [[ "$portable_type" != *"ELF 64-bit LSB"* || "$portable_type" != *"x86-64"* ]]; then
  fail "the updater executable is not a Linux amd64 ELF binary."
fi

if [[ "$(dpkg-deb --field "$deb" Package)" != "profiledeck" ||
      "$(dpkg-deb --field "$deb" Version)" != "$deb_version" ||
      "$(dpkg-deb --field "$deb" Architecture)" != "amd64" ]]; then
  fail "the DEB metadata does not match."
fi
deb_root="$work_dir/deb"
mkdir "$deb_root"
if ! dpkg-deb --extract "$deb" "$deb_root"; then
  fail "the DEB payload could not be extracted."
fi
expected_package_files="$(
  printf '%s\n' \
    ".$cli_install_path" \
    ".$desktop_install_path" \
    './usr/share/applications/profiledeck.desktop' \
    './usr/share/icons/hicolor/1024x1024/apps/profiledeck.png' \
    './usr/share/licenses/profiledeck/LICENSE' |
    LC_ALL=C sort
)"
actual_deb_files="$(
  cd "$deb_root"
  find . -mindepth 1 ! -type d -print | LC_ALL=C sort
)"
if [[ "$actual_deb_files" != "$expected_package_files" ]]; then
  fail "the DEB payload contains unexpected install paths."
fi
verify_deb_file() {
  local path="$1"
  local mode="$2"
  local full_path="$deb_root$path"
  if [[ ! -s "$full_path" || ! -f "$full_path" || -L "$full_path" ||
        "$(stat -c '%a' "$full_path")" != "$mode" ]]; then
    fail "the DEB payload has an invalid file or mode at $path."
  fi
}
verify_deb_file "$cli_install_path" 755
verify_deb_file "$desktop_install_path" 755
verify_deb_file /usr/share/applications/profiledeck.desktop 644
verify_deb_file /usr/share/icons/hicolor/1024x1024/apps/profiledeck.png 644
verify_deb_file /usr/share/licenses/profiledeck/LICENSE 644
for executable in "$deb_root$cli_install_path" "$deb_root$desktop_install_path"; do
  executable_type="$(file -b "$executable")"
  if [[ "$executable_type" != *"ELF 64-bit LSB"* || "$executable_type" != *"x86-64"* ]]; then
    fail "the DEB contains a non-amd64 executable."
  fi
done
if ! grep -Fxq "Exec=$updater_entry" \
  "$deb_root/usr/share/applications/profiledeck.desktop" ||
  ! grep -Fxq 'Icon=profiledeck' \
    "$deb_root/usr/share/applications/profiledeck.desktop"; then
  fail "the DEB desktop entry does not launch ProfileDeck."
fi
rpm_metadata="$(rpm -qp --qf '%{NAME}\n%{VERSION}\n%{RELEASE}\n%{ARCH}\n' "$rpm")"
if [[ "$rpm_metadata" != $'profiledeck\n'"$rpm_version"$'\n'"$package_release"$'\nx86_64' ]]; then
  fail "the RPM metadata does not match."
fi
if ! rpm_files="$(rpm -qp --qf \
  '[%{FILENAMES}\t%{FILEMODES:perms}\t%{FILEUSERNAME}\t%{FILEGROUPNAME}\t%{FILESIZES}\n]' \
  "$rpm")"; then
  fail "the RPM payload could not be inspected."
fi
actual_rpm_files="$(
  printf '%s' "$rpm_files" |
    awk -F $'\t' '$2 !~ /^d/ { print "." $1 }' |
    LC_ALL=C sort
)"
if [[ "$actual_rpm_files" != "$expected_package_files" ]]; then
  fail "the RPM payload contains unexpected install paths."
fi
verify_rpm_file() {
  local expected_path="$1"
  local expected_mode="$2"
  local entry
  entry="$(printf '%s' "$rpm_files" | awk -F $'\t' -v path="$expected_path" '$1 == path { print; exit }')"
  local path mode owner group size
  IFS=$'\t' read -r path mode owner group size <<< "$entry"
  if [[ "$path" != "$expected_path" || "$mode" != "$expected_mode" ||
        "$owner" != "root" || "$group" != "root" ||
        ! "$size" =~ ^[1-9][0-9]*$ ]]; then
    fail "the RPM payload has an invalid file, mode, or owner at $expected_path."
  fi
}
verify_rpm_file "$cli_install_path" -rwxr-xr-x
verify_rpm_file "$desktop_install_path" -rwxr-xr-x
verify_rpm_file /usr/share/applications/profiledeck.desktop -rw-r--r--
verify_rpm_file /usr/share/icons/hicolor/1024x1024/apps/profiledeck.png -rw-r--r--
verify_rpm_file /usr/share/licenses/profiledeck/LICENSE -rw-r--r--

printf 'Verified Linux release %s.\n' "$version"
