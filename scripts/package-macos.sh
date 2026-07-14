#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${PROFILEDECK_VERSION:-}"
build_number="${PROFILEDECK_BUILD_NUMBER:-}"
public_key="${PROFILEDECK_UPDATE_PUBLIC_KEY_BASE64:-}"
output_dir="${PROFILEDECK_DIST_DIR:-$repo_root/dist}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+-[0-9A-Za-z.-]+$ ]]; then
	echo "PROFILEDECK_VERSION must be a prerelease SemVer without a v prefix" >&2
	exit 1
fi
if [[ ! "$build_number" =~ ^[0-9]+$ ]]; then
	echo "PROFILEDECK_BUILD_NUMBER must contain only digits" >&2
	exit 1
fi
if [[ -z "$public_key" ]]; then
	echo "PROFILEDECK_UPDATE_PUBLIC_KEY_BASE64 is required" >&2
	exit 1
fi

short_version="${version%%-*}"
app_path="$output_dir/ProfileDeck.app"
artifact_name="ProfileDeck_${version}_darwin_arm64.zip"
artifact_path="$output_dir/$artifact_name"
executable_path="$app_path/Contents/MacOS/profiledeck-desktop"
commit="$(git rev-parse HEAD)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

rm -rf "$app_path" "$artifact_path"
mkdir -p "$app_path/Contents/MacOS"

ldflags="-w -s -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date -X main.updateFeedURL=https://raw.githubusercontent.com/strahe/profiledeck/main/updates/alpha.json -X main.updatePublicKeyBase64=$public_key"
CGO_ENABLED=1 \
GOOS=darwin \
GOARCH=arm64 \
MACOSX_DEPLOYMENT_TARGET=14.0 \
CGO_CFLAGS="-O2 -g -mmacosx-version-min=14.0" \
CGO_CXXFLAGS="-O2 -g -mmacosx-version-min=14.0" \
CGO_LDFLAGS="-O2 -g -mmacosx-version-min=14.0" \
	go build -tags production -trimpath -buildvcs=false -ldflags "$ldflags" -o "$executable_path" ./desktop

sed \
	-e "s|@SHORT_VERSION@|$short_version|g" \
	-e "s|@BUILD_NUMBER@|$build_number|g" \
	build/darwin/Info.plist.tmpl > "$app_path/Contents/Info.plist"
plutil -lint "$app_path/Contents/Info.plist" >/dev/null
chmod 0755 "$executable_path"

codesign --force --deep --sign - --timestamp=none "$app_path"
codesign --verify --deep --strict --verbose=2 "$app_path"

mkdir -p "$output_dir"
ditto -c -k --norsrc --keepParent "$app_path" "$artifact_path"

top_levels="$(unzip -Z1 "$artifact_path" | awk -F/ 'NF { print $1 }' | sort -u)"
if [[ "$top_levels" != "ProfileDeck.app" ]]; then
	echo "ZIP must contain only ProfileDeck.app at the top level; found: $top_levels" >&2
	exit 1
fi
if [[ "$top_levels" == *"__MACOSX"* ]]; then
	echo "ZIP must not contain a __MACOSX top-level directory" >&2
	exit 1
fi

verify_dir="$(mktemp -d)"
trap 'rm -rf "$verify_dir"' EXIT
ditto -x -k "$artifact_path" "$verify_dir"
extracted_count="$(find "$verify_dir" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')"
extracted_app="$verify_dir/ProfileDeck.app"
if [[ "$extracted_count" -ne 1 || ! -d "$extracted_app" ]]; then
	echo "Extracted ZIP does not contain exactly one ProfileDeck.app" >&2
	exit 1
fi
codesign --verify --deep --strict --verbose=2 "$extracted_app"
if [[ "$(plutil -extract CFBundleShortVersionString raw -o - "$extracted_app/Contents/Info.plist")" != "$short_version" ]]; then
	echo "Bundle short version does not match $short_version" >&2
	exit 1
fi
if [[ "$(plutil -extract CFBundleVersion raw -o - "$extracted_app/Contents/Info.plist")" != "$build_number" ]]; then
	echo "Bundle build number does not match $build_number" >&2
	exit 1
fi
if [[ "$(lipo -archs "$extracted_app/Contents/MacOS/profiledeck-desktop")" != "arm64" ]]; then
	echo "Bundle executable is not arm64-only" >&2
	exit 1
fi

echo "$artifact_path"
