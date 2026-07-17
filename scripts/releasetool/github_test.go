package main

import (
	"strings"
	"testing"
)

func TestValidateDraftRelease(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	const commit = "0123456789abcdef0123456789abcdef01234567"
	release := githubRelease{
		Body:            "Release notes",
		IsDraft:         true,
		IsPrerelease:    true,
		TagName:         version.tag(),
		TargetCommitish: commit,
		Assets: []githubAsset{
			{Name: "SHA256SUMS"},
			{Name: updaterZIPName(version)},
			{Name: installerDMGName(version)},
		},
	}
	if err := validateDraftRelease(release, version, commit, true); err != nil {
		t.Fatal(err)
	}
	release.Body = " "
	if err := validateDraftRelease(release, version, commit, true); err == nil {
		t.Fatal("Draft Release without notes passed validation")
	}
	release.Body = "Release notes"
	release.Assets = append(release.Assets, githubAsset{Name: "release-metadata.json"})
	if err := validateDraftRelease(release, version, commit, true); err == nil {
		t.Fatal("Draft Release with an extra asset passed validation")
	}
	release.Assets = release.Assets[:len(release.Assets)-1]
	release.IsPrerelease = false
	if err := validateDraftRelease(release, version, commit, true); err == nil {
		t.Fatal("Beta Draft Release without prerelease state passed validation")
	}
}

func TestValidatePublishedReleaseChannel(t *testing.T) {
	t.Parallel()
	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, value := range []string{"1.2.3", "1.2.4-beta.1"} {
		version, _ := parseReleaseVersion(value)
		release := githubRelease{
			IsPrerelease:    version.channel() == "beta",
			TagName:         version.tag(),
			TargetCommitish: commit,
			Assets: []githubAsset{
				{Name: "SHA256SUMS"},
				{Name: updaterZIPName(version)},
				{Name: installerDMGName(version)},
			},
		}
		if err := validatePublishedRelease(release, version, commit); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		release.IsPrerelease = !release.IsPrerelease
		if err := validatePublishedRelease(release, version, commit); err == nil {
			t.Fatalf("%s accepted the wrong prerelease state", value)
		}
	}
}

func TestValidateRepository(t *testing.T) {
	t.Parallel()
	if err := validateRepository("strahe/profiledeck"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "profiledeck", "a/b/c", "../repo"} {
		if err := validateRepository(value); err == nil {
			t.Fatalf("validateRepository(%q) succeeded, want error", value)
		}
	}
}

func TestParseGitHubReleasePagesIncludesDraftsAndAssetIDs(t *testing.T) {
	t.Parallel()
	releases, err := parseGitHubReleasePages([]byte(`[[
		{
			"id": 42,
			"body": "Notes",
			"draft": true,
			"prerelease": true,
			"tag_name": "v1.2.3-beta.4",
			"target_commitish": "0123456789abcdef0123456789abcdef01234567",
			"html_url": "https://github.com/example/project/releases/tag/untagged-draft",
			"assets": [{"id": 99, "name": "SHA256SUMS"}]
		}
	]]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].ID != 42 || !releases[0].IsDraft ||
		releases[0].Assets[0].ID != 99 ||
		!strings.Contains(releases[0].URL, "untagged-draft") {
		t.Fatalf("unexpected GitHub Release: %#v", releases)
	}
}
