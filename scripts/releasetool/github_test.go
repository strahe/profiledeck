package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDraftReleaseSupportsSafeResume(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	specs := testMacOSSpecs(t, version)
	release := githubRelease{
		ID:           42,
		IsDraft:      true,
		IsPrerelease: true,
		TagName:      version.tag(),
	}
	if err := validateResumableDraftRelease(release, version, specs); err != nil {
		t.Fatalf("empty Draft Release cannot be resumed: %v", err)
	}
	release.Assets = []githubAsset{
		{ID: 1, Name: "SHA256SUMS"},
		{ID: 2, Name: updaterZIPName(version)},
	}
	if err := validateResumableDraftRelease(release, version, specs); err != nil {
		t.Fatalf("partial Draft Release cannot be resumed: %v", err)
	}
	if err := validateDraftRelease(release, version, specs); err == nil {
		t.Fatal("partial Draft Release passed final validation")
	}
	release.Assets = append(release.Assets, githubAsset{ID: 3, Name: installerDMGName(version)})
	if err := validateDraftRelease(release, version, specs); err != nil {
		t.Fatal(err)
	}
}

func TestValidateResumableDraftReleaseRejectsUnsafeState(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	specs := testMacOSSpecs(t, version)
	valid := githubRelease{ID: 42, IsDraft: true, TagName: version.tag()}
	tests := map[string]githubRelease{
		"published": {
			ID: 42, TagName: version.tag(),
		},
		"wrong channel": {
			ID: 42, IsDraft: true, IsPrerelease: true, TagName: version.tag(),
		},
		"unexpected asset": {
			ID: 42, IsDraft: true, TagName: version.tag(),
			Assets: []githubAsset{{ID: 1, Name: "release-metadata.json"}},
		},
		"duplicate asset": {
			ID: 42, IsDraft: true, TagName: version.tag(),
			Assets: []githubAsset{
				{ID: 1, Name: "SHA256SUMS"},
				{ID: 2, Name: "SHA256SUMS"},
			},
		},
		"missing asset ID": {
			ID: 42, IsDraft: true, TagName: version.tag(),
			Assets: []githubAsset{{Name: "SHA256SUMS"}},
		},
	}
	if err := validateResumableDraftRelease(valid, version, specs); err != nil {
		t.Fatal(err)
	}
	for name, release := range tests {
		release := release
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateResumableDraftRelease(release, version, specs); err == nil {
				t.Fatal("unsafe Draft Release state was accepted")
			}
		})
	}
}

func TestDraftReleaseArgsRequireExistingTagAndDoNotUploadAssets(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	args := strings.Join(draftReleaseArgs("example/project", version), " ")
	if !strings.Contains(args, "--verify-tag") {
		t.Fatalf("Draft Release arguments do not verify the tag: %s", args)
	}
	if strings.Contains(args, "--target") {
		t.Fatalf("Draft Release arguments still trust target_commitish: %s", args)
	}
	if strings.Contains(args, ".zip") || strings.Contains(args, ".dmg") || strings.Contains(args, "SHA256SUMS") {
		t.Fatalf("Draft creation unexpectedly uploads assets: %s", args)
	}
	if !strings.Contains(args, "--prerelease --latest=false") {
		t.Fatalf("Beta Draft Release arguments are incomplete: %s", args)
	}
}

func TestValidateRepository(t *testing.T) {
	t.Parallel()
	if err := validateRepository("example/project"); err != nil {
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
	releases, err := parseGitHubReleasePages([]byte(`[{
		"not": "a page"
	}]`))
	if err == nil || releases != nil {
		t.Fatal("invalid paginated response was accepted")
	}
	releases, err = parseGitHubReleasePages([]byte(`[[
		{
			"id": 42,
			"draft": true,
			"prerelease": true,
			"tag_name": "v1.2.3-beta.4",
			"target_commitish": "main",
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

func TestReadTagCommitSupportsLightweightAndAnnotatedTags(t *testing.T) {
	t.Parallel()
	const (
		repository = "example/project"
		tag        = "v1.2.3"
		commit     = testReleaseCommit
		tagObject  = "abcdef0123456789abcdef0123456789abcdef01"
	)
	refKey := commandKey("gh", "api", "repos/"+repository+"/git/ref/tags/"+tag)

	lightweight := fakeCommandRunner{outputs: map[string][]byte{
		refKey: tagReference("commit", commit),
	}}
	actual, found, err := readTagCommit(context.Background(), lightweight, repository, tag)
	if err != nil || !found || actual != commit {
		t.Fatalf("lightweight tag = %q, %t, %v", actual, found, err)
	}

	annotated := fakeCommandRunner{outputs: map[string][]byte{
		refKey: tagReference("tag", tagObject),
		commandKey(
			"gh",
			"api",
			"repos/"+repository+"/git/tags/"+tagObject,
		): tagReference("commit", commit),
	}}
	actual, found, err = readTagCommit(context.Background(), annotated, repository, tag)
	if err != nil || !found || actual != commit {
		t.Fatalf("annotated tag = %q, %t, %v", actual, found, err)
	}
}

func TestEnsureReleaseTagCreatesOnlyAMissingTag(t *testing.T) {
	t.Parallel()
	const (
		repository = "example/project"
		tag        = "v1.2.3"
		commit     = testReleaseCommit
	)
	refKey := commandKey("gh", "api", "repos/"+repository+"/git/ref/tags/"+tag)
	createKey := commandKey(
		"gh",
		"api",
		"repos/"+repository+"/git/refs",
		"--method",
		"POST",
		"-f",
		"ref=refs/tags/"+tag,
		"-f",
		"sha="+commit,
		"--silent",
	)
	runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
		refKey: {
			{err: errors.New("gh failed: HTTP 404")},
			{output: tagReference("commit", commit)},
		},
		createKey: {{output: nil}},
	}}
	if err := ensureReleaseTag(context.Background(), runner, repository, tag, commit); err != nil {
		t.Fatal(err)
	}
	if len(runner.results[createKey]) != 0 {
		t.Fatal("tag creation command was not consumed")
	}
}

func TestEnsureReleaseTagRejectsAnotherCommit(t *testing.T) {
	t.Parallel()
	const (
		repository = "example/project"
		tag        = "v1.2.3"
		commit     = testReleaseCommit
		other      = "ffffffffffffffffffffffffffffffffffffffff"
	)
	runner := fakeCommandRunner{outputs: map[string][]byte{
		commandKey("gh", "api", "repos/"+repository+"/git/ref/tags/"+tag): tagReference("commit", other),
	}}
	if err := ensureReleaseTag(context.Background(), runner, repository, tag, commit); err == nil {
		t.Fatal("tag pointing to another commit was accepted")
	}
}

func TestCheckGitHubReleaseAcceptsMissingOrMatchingTag(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	version, _ := parseReleaseVersion("1.2.3")
	definitions := testMacOSDefinitions(t, version)
	baseOutputs := githubCheckOutputs(repository, testReleaseCommit, `[]`, `[[]]`)
	refKey := commandKey("gh", "api", "repos/"+repository+"/git/ref/tags/"+version.tag())

	missing := fakeCommandRunner{
		outputs: cloneOutputs(baseOutputs),
		errors:  map[string]error{refKey: errors.New("gh failed: HTTP 404")},
	}
	if err := checkGitHubRelease(
		context.Background(),
		missing,
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err != nil {
		t.Fatal(err)
	}

	matchingOutputs := cloneOutputs(baseOutputs)
	matchingOutputs[refKey] = tagReference("commit", testReleaseCommit)
	matching := fakeCommandRunner{outputs: matchingOutputs}
	if err := checkGitHubRelease(
		context.Background(),
		matching,
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err != nil {
		t.Fatal(err)
	}
}

func TestCheckGitHubReleaseAcceptsOnlyMatchingResumableDraft(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	version, _ := parseReleaseVersion("1.2.3-beta.2")
	definitions := testMacOSDefinitions(t, version)
	releasePage := fmt.Sprintf(
		`[[{"id":42,"draft":true,"prerelease":true,"tag_name":%q,"assets":[{"id":7,"name":"SHA256SUMS"}]}]]`,
		version.tag(),
	)
	outputs := githubCheckOutputs(repository, testReleaseCommit, `[]`, releasePage)
	outputs[commandKey(
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+version.tag(),
	)] = tagReference("commit", testReleaseCommit)
	if err := checkGitHubRelease(
		context.Background(),
		fakeCommandRunner{outputs: outputs},
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err != nil {
		t.Fatal(err)
	}

	published := cloneOutputs(outputs)
	published[githubReleasePagesKey(repository)] = []byte(strings.Replace(releasePage, `"draft":true`, `"draft":false`, 1))
	if err := checkGitHubRelease(
		context.Background(),
		fakeCommandRunner{outputs: published},
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err == nil {
		t.Fatal("published Release was accepted for resume")
	}

	duplicate := cloneOutputs(outputs)
	duplicate[githubReleasePagesKey(repository)] = []byte(fmt.Sprintf(
		`[[{"id":42,"draft":true,"prerelease":true,"tag_name":%q},{"id":43,"draft":true,"prerelease":true,"tag_name":%q}]]`,
		version.tag(),
		version.tag(),
	))
	if err := checkGitHubRelease(
		context.Background(),
		fakeCommandRunner{outputs: duplicate},
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err == nil {
		t.Fatal("multiple Releases for one tag were accepted")
	}
}

func TestCheckGitHubReleaseRejectsOlderVersionAndWrongTagCommit(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	version, _ := parseReleaseVersion("1.2.3")
	definitions := testMacOSDefinitions(t, version)

	olderOutputs := githubCheckOutputs(
		repository,
		testReleaseCommit,
		`[{"tagName":"v2.0.0"}]`,
		`[[]]`,
	)
	if err := checkGitHubRelease(
		context.Background(),
		fakeCommandRunner{outputs: olderOutputs},
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err == nil {
		t.Fatal("older release version was accepted")
	}

	wrongTagOutputs := githubCheckOutputs(repository, testReleaseCommit, `[]`, `[[]]`)
	wrongTagOutputs[commandKey(
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+version.tag(),
	)] = tagReference("commit", "ffffffffffffffffffffffffffffffffffffffff")
	if err := checkGitHubRelease(
		context.Background(),
		fakeCommandRunner{outputs: wrongTagOutputs},
		repository,
		version,
		testReleaseCommit,
		definitions,
	); err == nil {
		t.Fatal("tag pointing to another commit was accepted")
	}
}

func TestVerifyExistingDraftAssetsRejectsChangedContent(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte("expected"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := githubRelease{Assets: []githubAsset{{ID: 9, Name: "SHA256SUMS"}}}
	assetKey := commandKey(
		"gh",
		"api",
		"-H",
		"Accept: application/octet-stream",
		"repos/"+repository+"/releases/assets/9",
	)
	if err := verifyExistingDraftAssets(
		context.Background(),
		fakeCommandRunner{outputs: map[string][]byte{assetKey: []byte("expected")}},
		repository,
		release,
		directory,
	); err != nil {
		t.Fatal(err)
	}
	if err := verifyExistingDraftAssets(
		context.Background(),
		fakeCommandRunner{outputs: map[string][]byte{assetKey: []byte("changed")}},
		repository,
		release,
		directory,
	); err == nil {
		t.Fatal("changed Draft Release asset was accepted")
	}
}

func TestUploadMissingDraftAssetsSkipsVerifiedAssetsWithoutClobber(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	version, _ := parseReleaseVersion("1.2.3")
	specs := testMacOSSpecs(t, version)
	directory := t.TempDir()
	for _, name := range expectedRemoteAssetNames(specs) {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := githubRelease{Assets: []githubAsset{{ID: 7, Name: updaterZIPName(version)}}}
	runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{}}
	for _, name := range []string{installerDMGName(version), "SHA256SUMS"} {
		key := commandKey(
			"gh",
			"release",
			"upload",
			version.tag(),
			filepath.Join(directory, name),
			"--repo",
			repository,
		)
		runner.results[key] = []scriptedCommandResult{{}}
	}
	if err := uploadMissingDraftAssets(
		context.Background(),
		runner,
		repository,
		version,
		release,
		directory,
		specs,
	); err != nil {
		t.Fatal(err)
	}
	for key, results := range runner.results {
		if strings.Contains(key, "--clobber") {
			t.Fatalf("upload command uses --clobber: %s", key)
		}
		if len(results) != 0 {
			t.Fatalf("upload command was not consumed: %s", key)
		}
	}
}

func TestCreateDraftReleaseResumesPartialDraftAndVerifiesRemoteAssets(t *testing.T) {
	t.Parallel()
	const repository = "example/project"
	version, _ := parseReleaseVersion("1.2.3")
	definitions := testMacOSDefinitions(t, version)
	root := t.TempDir()
	writeMacOSPlatformHandoff(t, root, version, 31, testReleaseCommit)
	bundle, err := assembleRelease(root, version, 31, testReleaseCommit, definitions)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := verifyReleaseBundle(
		bundle,
		version,
		31,
		testReleaseCommit,
		definitions,
	)
	if err != nil {
		t.Fatal(err)
	}

	partialPage := fmt.Sprintf(
		`[[{"id":42,"draft":true,"tag_name":%q,"html_url":"https://example.invalid/draft","assets":[{"id":1,"name":%q}]}]]`,
		version.tag(),
		updaterZIPName(version),
	)
	completePage := fmt.Sprintf(
		`[[{"id":42,"draft":true,"tag_name":%q,"html_url":"https://example.invalid/draft","assets":[{"id":1,"name":%q},{"id":2,"name":%q},{"id":3,"name":"SHA256SUMS"}]}]]`,
		version.tag(),
		updaterZIPName(version),
		installerDMGName(version),
	)
	releasePagesKey := githubReleasePagesKey(repository)
	tagKey := commandKey("gh", "api", "repos/"+repository+"/git/ref/tags/"+version.tag())
	runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
		commandKey("gh", "auth", "status"): {{output: nil}},
		commandKey(
			"gh",
			"api",
			"repos/"+repository+"/commits/"+testReleaseCommit,
			"--silent",
		): {{output: nil}},
		commandKey(
			"gh",
			"release",
			"list",
			"--repo",
			repository,
			"--limit",
			"1000",
			"--exclude-drafts",
			"--json",
			"tagName",
		): {{output: []byte(`[]`)}},
		releasePagesKey: {
			{output: []byte(partialPage)},
			{output: []byte(partialPage)},
			{output: []byte(completePage)},
			{output: []byte(completePage)},
			{output: []byte(completePage)},
		},
		tagKey: {
			{output: tagReference("commit", testReleaseCommit)},
			{output: tagReference("commit", testReleaseCommit)},
			{output: tagReference("commit", testReleaseCommit)},
		},
	}}

	assetIDs := map[string]int64{
		updaterZIPName(version):   1,
		installerDMGName(version): 2,
		"SHA256SUMS":              3,
	}
	for name, id := range assetIDs {
		content, err := os.ReadFile(filepath.Join(bundle, name))
		if err != nil {
			t.Fatal(err)
		}
		downloadKey := commandKey(
			"gh",
			"api",
			"-H",
			"Accept: application/octet-stream",
			fmt.Sprintf("repos/%s/releases/assets/%d", repository, id),
		)
		results := []scriptedCommandResult{{output: content}}
		if name == updaterZIPName(version) {
			results = append(results, scriptedCommandResult{output: content})
		}
		runner.results[downloadKey] = results
	}
	for _, name := range []string{installerDMGName(version), "SHA256SUMS"} {
		uploadKey := commandKey(
			"gh",
			"release",
			"upload",
			version.tag(),
			filepath.Join(bundle, name),
			"--repo",
			repository,
		)
		runner.results[uploadKey] = []scriptedCommandResult{{}}
	}

	release, err := createDraftRelease(
		context.Background(),
		runner,
		repository,
		version,
		bundle,
		metadata,
		definitions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if release.URL != "https://example.invalid/draft" || len(release.Assets) != 3 {
		t.Fatalf("unexpected resumed Draft Release: %#v", release)
	}
	for key, results := range runner.results {
		if len(results) != 0 {
			t.Fatalf("expected command was not consumed: %s", strings.ReplaceAll(key, "\x00", " "))
		}
	}
}

func testMacOSSpecs(t *testing.T, version releaseVersion) []releaseAssetSpec {
	t.Helper()
	specs, err := platformAssetSpecs(macOSPlatform, version)
	if err != nil {
		t.Fatal(err)
	}
	return specs
}

func testMacOSDefinitions(t *testing.T, version releaseVersion) []releasePlatformDefinition {
	t.Helper()
	definitions, err := parseReleasePlatforms(macOSPlatform, version)
	if err != nil {
		t.Fatal(err)
	}
	return definitions
}

func githubCheckOutputs(
	repository string,
	commit string,
	published string,
	allReleases string,
) map[string][]byte {
	return map[string][]byte{
		commandKey("gh", "auth", "status"):                                          nil,
		commandKey("gh", "api", "repos/"+repository+"/commits/"+commit, "--silent"): nil,
		commandKey(
			"gh",
			"release",
			"list",
			"--repo",
			repository,
			"--limit",
			"1000",
			"--exclude-drafts",
			"--json",
			"tagName",
		): []byte(published),
		githubReleasePagesKey(repository): []byte(allReleases),
	}
}

func githubReleasePagesKey(repository string) string {
	return commandKey(
		"gh",
		"api",
		"repos/"+repository+"/releases?per_page=100",
		"--paginate",
		"--slurp",
	)
}

type scriptedCommandResult struct {
	output []byte
	err    error
}

type scriptedCommandRunner struct {
	results map[string][]scriptedCommandResult
}

func (runner *scriptedCommandRunner) run(
	_ context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	key := commandKey(name, args...)
	results := runner.results[key]
	if len(results) == 0 {
		return nil, fmt.Errorf("unexpected command: %s", strings.ReplaceAll(key, "\x00", " "))
	}
	result := results[0]
	runner.results[key] = results[1:]
	return result.output, result.err
}

func commandKey(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), "\x00")
}

func tagReference(kind, sha string) []byte {
	return []byte(fmt.Sprintf(`{"object":{"type":%q,"sha":%q}}`, kind, sha))
}

func cloneOutputs(source map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
