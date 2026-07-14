#!/bin/bash
set -euo pipefail

if [[ "$#" -ne 3 ]]; then
	echo "usage: verify-macos-artifact.sh <zip> <version> <build-number>" >&2
	exit 1
fi

artifact_path="$1"
version="$2"
build_number="$3"
short_version="${version%%-*}"
expected_name="ProfileDeck_${version}_darwin_arm64.zip"

if [[ "$(basename "$artifact_path")" != "$expected_name" ]]; then
	echo "artifact filename must be $expected_name" >&2
	exit 1
fi

top_levels="$(unzip -Z1 "$artifact_path" | awk -F/ 'NF { print $1 }' | sort -u)"
if [[ "$top_levels" != "ProfileDeck.app" || "$top_levels" == *"__MACOSX"* ]]; then
	echo "artifact must contain only ProfileDeck.app at the top level" >&2
	exit 1
fi

verify_dir="$(mktemp -d)"
trap 'rm -rf "$verify_dir"' EXIT
ditto -x -k "$artifact_path" "$verify_dir"
app_path="$verify_dir/ProfileDeck.app"
entry_count="$(find "$verify_dir" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')"
if [[ "$entry_count" -ne 1 || ! -d "$app_path" ]]; then
	echo "extracted artifact must contain exactly one ProfileDeck.app" >&2
	exit 1
fi

codesign --verify --deep --strict --verbose=2 "$app_path"
signature_details="$(codesign --display --verbose=4 "$app_path" 2>&1)"
if [[ "$signature_details" != *"Signature=adhoc"* ]]; then
	echo "application is not ad-hoc signed" >&2
	exit 1
fi
if [[ "$(lipo -archs "$app_path/Contents/MacOS/profiledeck-desktop")" != "arm64" ]]; then
	echo "application executable is not arm64-only" >&2
	exit 1
fi
if [[ "$(plutil -extract CFBundleIdentifier raw -o - "$app_path/Contents/Info.plist")" != "io.github.strahe.profiledeck" ]]; then
	echo "unexpected bundle identifier" >&2
	exit 1
fi
if [[ "$(plutil -extract CFBundleExecutable raw -o - "$app_path/Contents/Info.plist")" != "profiledeck-desktop" ]]; then
	echo "unexpected bundle executable" >&2
	exit 1
fi
if [[ "$(plutil -extract CFBundleShortVersionString raw -o - "$app_path/Contents/Info.plist")" != "$short_version" ]]; then
	echo "unexpected bundle short version" >&2
	exit 1
fi
if [[ "$(plutil -extract CFBundleVersion raw -o - "$app_path/Contents/Info.plist")" != "$build_number" ]]; then
	echo "unexpected bundle build number" >&2
	exit 1
fi
if [[ "$(plutil -extract LSMinimumSystemVersion raw -o - "$app_path/Contents/Info.plist")" != "14.0" ]]; then
	echo "unexpected minimum macOS version" >&2
	exit 1
fi

echo "verified $expected_name"
