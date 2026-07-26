#!/usr/bin/env bash
# Build Linux release assets. Desktop compilation remains owned by Wails Task.
set -euo pipefail

version=""
release_commit=""
output=""
built_at=""
task_exe="${TASK_EXE:-wails3}"

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --version) version="${2-}"; shift 2 ;;
    --release-commit) release_commit="${2-}"; shift 2 ;;
    --output) output="${2-}"; shift 2 ;;
    --built-at) built_at="${2-}"; shift 2 ;;
    --task-exe) task_exe="${2-}"; shift 2 ;;
    *) echo "Could not build the Linux release: unsupported argument." >&2; exit 1 ;;
  esac
done

if [[ -z "$version" || -z "$release_commit" || -z "$output" || -z "$built_at" ]]; then
  echo "Could not build the Linux release: required release identity is missing." >&2
  exit 1
fi

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repository_root"
scripts/release/verify-release-source.sh "$release_commit"

contract_field() {
  go run ./scripts/releasetool contract --version "$version" --field "$1"
}

portable_name="$(contract_field asset.linux-updater)"
app_name="$(contract_field linux-updater-entry)"
deb_name="$(contract_field asset.linux-deb)"
rpm_name="$(contract_field asset.linux-rpm)"
package_version="$(contract_field linux-package-version)"
package_release="$(contract_field linux-package-release)"
package_basename="${deb_name%.deb}"

mkdir -p "$output"
output="$(cd "$output" && pwd -P)"
for asset in "$portable_name" "$deb_name" "$rpm_name"; do
  if [[ -e "$output/$asset" ]]; then
    echo "Could not build the Linux release: an output asset already exists." >&2
    exit 1
  fi
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-linux-release.XXXXXX")"
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

portable_root="$work_dir/portable"
package_root="$work_dir/package"
mkdir -p "$portable_root" "$package_root"

"$task_exe" task linux:build:release \
  VERSION="$version" COMMIT="$release_commit" BUILD_DATE="$built_at" \
  PORTABLE_OUTPUT="$portable_root/$app_name" \
  PACKAGE_OUTPUT="$package_root/$app_name"
chmod 0755 "$portable_root/$app_name" "$package_root/$app_name"

env GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  go build -trimpath -buildvcs=false \
    -ldflags="-w -s -X main.version=$version -X main.commit=$release_commit -X main.buildDate=$built_at" \
    -o "$package_root/profiledeck" ./cmd/profiledeck
chmod 0755 "$package_root/profiledeck"
scripts/release/verify-release-source.sh "$release_commit"

tar -C "$portable_root" -czf "$output/$portable_name" "$app_name"

for package_format in deb rpm; do
  env \
    GOARCH=amd64 \
    PROFILEDECK_PACKAGE_VERSION="$package_version" \
    PROFILEDECK_PACKAGE_RELEASE="$package_release" \
    PROFILEDECK_CLI_BINARY="$package_root/profiledeck" \
    PROFILEDECK_DESKTOP_BINARY="$package_root/$app_name" \
    "$task_exe" tool package \
      -name "$package_basename" -format "$package_format" -config ./build/linux/nfpm.yaml -out "$output"
done

scripts/release/verify-linux.sh \
  --version "$version" \
  --portable "$output/$portable_name" \
  --deb "$output/$deb_name" \
  --rpm "$output/$rpm_name"
