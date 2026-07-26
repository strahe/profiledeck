#!/usr/bin/env bash
set -euo pipefail

version=""
deb=""
rpm=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --version) version="${2-}"; shift 2 ;;
    --deb) deb="${2-}"; shift 2 ;;
    --rpm) rpm="${2-}"; shift 2 ;;
    *) echo "Could not verify Linux packages: unsupported argument." >&2; exit 1 ;;
  esac
done
for path in "$deb" "$rpm"; do
  if [[ -z "$path" || ! -s "$path" || -L "$path" ]]; then
    echo "Could not verify Linux packages: a required package is unavailable." >&2
    exit 1
  fi
done

if [[ -z "$version" ]]; then
  echo "Could not verify Linux packages: the release version is missing." >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "Could not verify Linux packages: Docker is unavailable." >&2
  exit 1
fi

deb="$(cd "$(dirname "$deb")" && pwd -P)/$(basename "$deb")"
rpm="$(cd "$(dirname "$rpm")" && pwd -P)/$(basename "$rpm")"
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
checker="$script_directory/check-linux-install.sh"
repository_root="$(cd "$script_directory/../.." && pwd -P)"
desktop_name="$(
  cd "$repository_root"
  go run ./scripts/releasetool contract --version "$version" --field linux-updater-entry
)"

docker run --rm --platform linux/amd64 \
  --volume "$deb:/tmp/profiledeck.deb:ro" \
  --volume "$checker:/tmp/check-linux-install.sh:ro" \
  ubuntu:24.04 bash -euc '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y file /tmp/profiledeck.deb
  bash /tmp/check-linux-install.sh "$1" "$2"
' bash "$version" "$desktop_name"

docker run --rm --platform linux/amd64 \
  --volume "$rpm:/tmp/profiledeck.rpm:ro" \
  --volume "$checker:/tmp/check-linux-install.sh:ro" \
  fedora:44 bash -euc '
  dnf install -y file /tmp/profiledeck.rpm
  bash /tmp/check-linux-install.sh "$1" "$2"
' bash "$version" "$desktop_name"
