#!/bin/bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
	echo "update restart integration test requires macOS arm64" >&2
	exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
work_dir="$(mktemp -d)"
server_pid=""
cleanup() {
	if [[ -n "$server_pid" ]]; then
		kill "$server_pid" >/dev/null 2>&1 || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [[ "${PROFILEDECK_UPDATE_E2E_KEEP:-0}" == "1" ]]; then
		echo "preserved update E2E workspace: $work_dir" >&2
	else
		rm -rf "$work_dir"
	fi
}
trap cleanup EXIT

key_path="$work_dir/update.key"
serve_dir="$work_dir/serve"
installed_dir="$work_dir/installed"
config_dir="$work_dir/config"
marker="$work_dir/result.txt"
address_file="$work_dir/address.txt"
mkdir -p "$serve_dir" "$installed_dir" "$config_dir"

"${WAILS3:-wails3}" updater genkey -out "$key_path" >/dev/null
public_key="$(go run ./scripts/feedtool public-key --private-key "$key_path")"

go run ./scripts/updatee2e/server --root "$serve_dir" --address-file "$address_file" >"$work_dir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
	[[ -s "$address_file" ]] && break
	sleep 0.05
done
if [[ ! -s "$address_file" ]]; then
	echo "temporary update server did not start" >&2
	exit 1
fi
server_url="$(cat "$address_file")"

build_bundle() {
	local target="$1"
	local build_version="$2"
	local build_feed_url="$3"
	mkdir -p "$target/Contents/MacOS"
	local ldflags="-X main.version=$build_version -X main.feedURL=$build_feed_url -X main.publicKey=$public_key -X main.configDir=$config_dir -X main.marker=$marker"
	go build -tags updatee2e -trimpath -buildvcs=false -ldflags "$ldflags" -o "$target/Contents/MacOS/profiledeck-desktop" ./scripts/updatee2e/client
	sed \
		-e 's|@SHORT_VERSION@|0.1.0|g' \
		-e 's|@BUILD_NUMBER@|1|g' \
		build/darwin/Info.plist.tmpl > "$target/Contents/Info.plist"
	chmod 0755 "$target/Contents/MacOS/profiledeck-desktop"
	codesign --force --deep --sign - --timestamp=none "$target" >/dev/null
}

new_app="$work_dir/new/ProfileDeck.app"
artifact="$serve_dir/ProfileDeck_0.1.0-alpha.2_darwin_arm64.zip"
build_bundle "$new_app" "0.1.0-alpha.2" ""
ditto -c -k --norsrc --keepParent "$new_app" "$artifact"
go run ./scripts/feedtool create \
	--private-key "$key_path" \
	--version "0.1.0-alpha.2" \
	--artifact "$artifact" \
	--artifact-url "$server_url/$(basename "$artifact")" \
	--allow-test-source \
	--output "$serve_dir/feed.json"

installed_app="$installed_dir/ProfileDeck.app"
build_bundle "$installed_app" "0.1.0-alpha.1" "$server_url/feed.json"
"$installed_app/Contents/MacOS/profiledeck-desktop"

for _ in $(seq 1 300); do
	[[ -s "$marker" ]] && break
	sleep 0.1
done
if [[ ! -s "$marker" ]]; then
	echo "updated application did not relaunch: $work_dir" >&2
	exit 1
fi
result="$(cat "$marker")"
if [[ "$result" != "ok: 0.1.0-alpha.2" ]]; then
	echo "$result" >&2
	exit 1
fi
if [[ ! -d "$config_dir/profiledeck/updates/backups" ]]; then
	echo "update backup directory is missing" >&2
	exit 1
fi

echo "verified real 0.1.0-alpha.1 to 0.1.0-alpha.2 restart replacement"
