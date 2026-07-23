#!/bin/bash
set -euo pipefail
action="${1-}"
if [[ -n "$action" ]]; then
  shift
fi
version=""
build_number=""
repository=""
commit=""
platforms=""
bundle=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="${2-}"; shift 2 ;;
    --build-number) build_number="${2-}"; shift 2 ;;
    --repo) repository="${2-}"; shift 2 ;;
    --commit) commit="${2-}"; shift 2 ;;
    --platforms) platforms="${2-}"; shift 2 ;;
    --bundle) bundle="${2-}"; shift 2 ;;
    *) echo "Could not prepare the GitHub Draft. Check the release command and try again." >&2; exit 1 ;;
  esac
done
if [[ "$action" != "check" && "$action" != "draft" ]]; then
  echo "Could not prepare the GitHub Draft. Choose the check or draft action." >&2
  exit 1
fi
if [[ -z "$version" || -z "$repository" || -z "$commit" || -z "$platforms" ]]; then
  echo "Could not prepare the GitHub Draft. Required release information is missing." >&2
  exit 1
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ || ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Could not prepare the GitHub Draft. The repository or commit is invalid." >&2
  exit 1
fi
if [[ "$action" == "draft" && ( -z "$build_number" || -z "$bundle" ) ]]; then
  echo "Could not prepare the GitHub Draft. The verified bundle and build number are required." >&2
  exit 1
fi
umask 077
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/profiledeck-github.XXXXXX")"
private_log="$temp_dir/command.log"
cleanup() {
  rm -rf -- "$temp_dir"
}
trap cleanup EXIT HUP INT TERM
capture() {
  local output="$1"
  local message="$2"
  shift 2
  if ! "$@" >"$output" 2>"$private_log"; then
    : >"$private_log"
    echo "$message" >&2
    return 1
  fi
}
contract_args=(go run ./scripts/releasetool contract --version "$version" --platforms "$platforms")
tag="$("${contract_args[@]}" --field tag)"
channel="$("${contract_args[@]}" --field channel)"
product="$("${contract_args[@]}" --field product)"
expected_assets="$temp_dir/expected-assets"
"${contract_args[@]}" --field public-assets >"$expected_assets"
sort -o "$expected_assets" "$expected_assets"

capture "$temp_dir/published-tags" "Could not read the published releases. Check GitHub access and try again." \
  gh release list --repo "$repository" --limit 1000 --exclude-drafts --json tagName --jq '.[].tagName'
go run ./scripts/releasetool version-check --version "$version" --tags "$temp_dir/published-tags" >/dev/null
capture /dev/null "Could not authenticate with GitHub. Sign in with gh and try again." gh auth status
capture /dev/null "The release commit is not available on GitHub. Sync it before retrying." \
  gh api "repos/$repository/commits/$commit" --silent

tag_commit=""
tag_found="false"
read_tag() {
  local reference="$temp_dir/tag-reference"
  if gh api "repos/$repository/git/ref/tags/$tag" --jq '.object.type + " " + .object.sha' >"$reference" 2>"$private_log"; then
    tag_found="true"
  elif grep -q 'HTTP 404' "$private_log"; then
    tag_found="false"
    tag_commit=""
    : >"$private_log"
    return 0
  else
    : >"$private_log"
    echo "Could not inspect the release tag. Check GitHub access and try again." >&2
    return 1
  fi
  read -r object_type object_sha <"$reference"
  if [[ "$object_type" == "tag" ]]; then
    capture "$reference" "Could not resolve the release tag. Check it on GitHub and try again." \
      gh api "repos/$repository/git/tags/$object_sha" --jq '.object.type + " " + .object.sha'
    read -r object_type object_sha <"$reference"
  fi
  if [[ "$object_type" != "commit" || ! "$object_sha" =~ ^[0-9a-f]{40}$ ]]; then
    echo "The release tag does not resolve to a commit. Correct it on GitHub before retrying." >&2
    return 1
  fi
  tag_commit="$object_sha"
}
release_found="false"
release_state="$temp_dir/release-state"
release_assets="$temp_dir/release-assets"
read_release() {
  if gh release view "$tag" --repo "$repository" \
    --json isDraft,isPrerelease,tagName,url \
    --jq '[.isDraft, .isPrerelease, .tagName, .url] | @tsv' >"$release_state" 2>"$private_log"; then
    release_found="true"
  else
    capture "$temp_dir/release-tags" "Could not inspect the GitHub Releases. Check access and try again." \
      gh release list --repo "$repository" --limit 1000 --json tagName --jq '.[].tagName'
    if grep -Fxq "$tag" "$temp_dir/release-tags"; then
      echo "Could not read the existing release. Check it on GitHub before retrying." >&2
      return 1
    fi
    release_found="false"
    : >"$private_log"
    : >"$release_assets"
    return 0
  fi
  capture "$release_assets" "Could not inspect the Draft assets. Check GitHub access and try again." \
    gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name'
  sort -o "$release_assets" "$release_assets"
}
validate_release() {
  [[ "$release_found" == "true" ]] || return 0
  IFS=$'\t' read -r is_draft is_prerelease release_tag release_url <"$release_state"
  if [[ "$is_draft" != "true" || "$release_tag" != "$tag" ]]; then
    echo "This version already has a published or incompatible Release. Choose a new version." >&2
    return 1
  fi
  expected_prerelease="false"
  [[ "$channel" == "beta" ]] && expected_prerelease="true"
  if [[ "$is_prerelease" != "$expected_prerelease" ]]; then
    echo "The Draft channel does not match this version. Correct it on GitHub before retrying." >&2
    return 1
  fi
  if [[ -n "$(uniq -d "$release_assets")" ]]; then
    echo "The Draft contains duplicate asset names. Correct it on GitHub before retrying." >&2
    return 1
  fi
  while IFS= read -r asset; do
    [[ -z "$asset" ]] && continue
    if ! grep -Fxq "$asset" "$expected_assets"; then
      echo "The Draft contains an unexpected asset. Correct it on GitHub before retrying." >&2
      return 1
    fi
  done <"$release_assets"
}
read_tag
if [[ "$tag_found" == "true" && "$tag_commit" != "$commit" ]]; then
  echo "The release tag points to another commit. Use a new version." >&2
  exit 1
fi
read_release
validate_release
if [[ "$release_found" == "true" && "$tag_found" != "true" ]]; then
  echo "The Draft has no matching release tag. Correct it on GitHub before retrying." >&2
  exit 1
fi
if [[ "$action" == "check" ]]; then
  printf 'GitHub release state is ready for %s at %s.\n' "$tag" "$commit"
  exit 0
fi
go run ./scripts/releasetool verify-bundle \
  --version "$version" --build-number "$build_number" --commit "$commit" \
  --platforms "$platforms" --directory "$bundle" >/dev/null
if [[ "$tag_found" != "true" ]]; then
  if ! gh api "repos/$repository/git/refs" --method POST \
    -f "ref=refs/tags/$tag" -f "sha=$commit" --silent >"$private_log" 2>&1; then
    read_tag
    if [[ "$tag_found" != "true" || "$tag_commit" != "$commit" ]]; then
      : >"$private_log"
      echo "Could not create the release tag. Check GitHub access and try again." >&2
      exit 1
    fi
  fi
  read_tag
  if [[ "$tag_found" != "true" || "$tag_commit" != "$commit" ]]; then
    echo "The release tag could not be verified. Check it on GitHub before retrying." >&2
    exit 1
  fi
fi
if [[ "$release_found" != "true" ]]; then
  create_args=(release create "$tag" --repo "$repository" --verify-tag --draft --generate-notes --title "$product $version")
  if [[ "$channel" == "beta" ]]; then
    create_args+=(--prerelease --latest=false)
  fi
  capture /dev/null "Could not create the Draft Release. Check GitHub access and try again." gh "${create_args[@]}"
  for delay in 0 1 2 4 8 15; do
    [[ "$delay" -eq 0 ]] || sleep "$delay"
    read_release
    if [[ "$release_found" == "true" ]]; then
      break
    fi
  done
  if [[ "$release_found" != "true" ]]; then
    echo "GitHub did not make the Draft visible. Rerun the same release workflow." >&2
    exit 1
  fi
  validate_release
fi
existing_download="$temp_dir/existing"
mkdir "$existing_download"
if [[ -s "$release_assets" ]]; then
  capture /dev/null "Could not download the existing Draft assets. Check GitHub access and try again." \
    gh release download "$tag" --repo "$repository" --dir "$existing_download"
  while IFS= read -r asset; do
    [[ -z "$asset" ]] && continue
    if [[ ! -f "$bundle/$asset" || ! -f "$existing_download/$asset" ]] || \
      ! cmp -s "$bundle/$asset" "$existing_download/$asset"; then
      echo "An existing Draft asset differs from this release. Use a new version." >&2
      exit 1
    fi
  done <"$release_assets"
fi

while IFS= read -r asset; do
  [[ -z "$asset" ]] && continue
  if ! grep -Fxq "$asset" "$release_assets"; then
    capture /dev/null "Could not upload a release asset. Rerun the same workflow to continue." \
      gh release upload "$tag" "$bundle/$asset" --repo "$repository"
  fi
done <"$expected_assets"

for delay in 0 1 2 4 8 15; do
  [[ "$delay" -eq 0 ]] || sleep "$delay"
  read_release
  validate_release
  if cmp -s "$expected_assets" "$release_assets"; then
    break
  fi
done
if ! cmp -s "$expected_assets" "$release_assets"; then
  echo "GitHub did not make every release asset visible. Rerun the same workflow." >&2
  exit 1
fi

final_download="$temp_dir/final"
mkdir "$final_download"
capture /dev/null "Could not download the completed Draft assets. Rerun the same workflow." \
  gh release download "$tag" --repo "$repository" --dir "$final_download"
while IFS= read -r asset; do
  [[ -z "$asset" ]] && continue
  if [[ ! -f "$final_download/$asset" ]] || ! cmp -s "$bundle/$asset" "$final_download/$asset"; then
    echo "A downloaded Draft asset does not match this release. Keep the Draft and investigate before retrying." >&2
    exit 1
  fi
done <"$expected_assets"
read_tag
read_release
validate_release
if [[ "$tag_commit" != "$commit" ]] || ! cmp -s "$expected_assets" "$release_assets"; then
  echo "The completed Draft changed during verification. Keep it unpublished and investigate." >&2
  exit 1
fi
IFS=$'\t' read -r _ _ _ release_url <"$release_state"
printf 'Draft Release is ready for review: %s\n' "$release_url"
