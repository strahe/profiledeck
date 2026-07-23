#!/bin/sh
set -eu

unsafe_paths=
while IFS= read -r tracked_path; do
	case "$tracked_path" in
		.DS_Store | */.DS_Store \
		| profiledeck.db | profiledeck.db-* | */profiledeck.db | */profiledeck.db-* \
		| profiles.json | */profiles.json \
		| *.profiledeck-backup \
		| pre-launch-audit-report-*.md | */pre-launch-audit-report-*.md \
		| pre-launch-data-model-audit-*.md | */pre-launch-data-model-audit-*.md \
		| build.log | */build.log \
		| cc-switch/* | codex-codebase/* | Antigravity-Manager/*)
			unsafe_paths="${unsafe_paths}${tracked_path}
"
			;;
	esac
done <<EOF
$(git ls-files)
EOF

if [ -n "$unsafe_paths" ]; then
	printf '%s\n' "Source hygiene check failed: private or generated paths are tracked." >&2
	printf '%s' "$unsafe_paths" >&2
	exit 1
fi
