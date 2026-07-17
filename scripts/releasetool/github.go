package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type githubAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type githubRelease struct {
	ID           int64         `json:"id"`
	IsDraft      bool          `json:"isDraft"`
	IsPrerelease bool          `json:"isPrerelease"`
	TagName      string        `json:"tagName"`
	URL          string        `json:"url"`
	Assets       []githubAsset `json:"assets"`
}

type githubAPIRelease struct {
	ID         int64         `json:"id"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Assets     []githubAsset `json:"assets"`
}

type githubTagReference struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

func validateRepository(repository string) error {
	if !repositoryPattern.MatchString(repository) {
		return fmt.Errorf("release repository must use the owner/repository form")
	}
	parts := strings.Split(repository, "/")
	if parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return fmt.Errorf("release repository must use the owner/repository form")
	}
	return nil
}

func parseGitHubReleasePages(content []byte) ([]githubRelease, error) {
	var pages [][]githubAPIRelease
	if err := json.Unmarshal(content, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub Release list: %w", err)
	}
	var releases []githubRelease
	for _, page := range pages {
		for _, release := range page {
			releases = append(releases, githubRelease{
				ID:           release.ID,
				IsDraft:      release.Draft,
				IsPrerelease: release.Prerelease,
				TagName:      release.TagName,
				URL:          release.HTMLURL,
				Assets:       release.Assets,
			})
		}
	}
	return releases, nil
}

func listGitHubReleases(
	ctx context.Context,
	runner commandRunner,
	repository string,
) ([]githubRelease, error) {
	output, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/releases?per_page=100",
		"--paginate",
		"--slurp",
	)
	if err != nil {
		return nil, err
	}
	return parseGitHubReleasePages(output)
}

func findGitHubRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
) (githubRelease, bool, error) {
	releases, err := listGitHubReleases(ctx, runner, repository)
	if err != nil {
		return githubRelease{}, false, err
	}
	var match githubRelease
	found := false
	for _, release := range releases {
		if release.TagName != tag {
			continue
		}
		if found {
			return githubRelease{}, false, fmt.Errorf("multiple GitHub Releases use tag %s", tag)
		}
		match = release
		found = true
	}
	return match, found, nil
}

func releaseAssetNames(release githubRelease) []string {
	names := make([]string, 0, len(release.Assets))
	for _, asset := range release.Assets {
		names = append(names, asset.Name)
	}
	sort.Strings(names)
	return names
}

func expectedRemoteAssetNames(specs []releaseAssetSpec) []string {
	names := append(assetSpecNames(specs), "SHA256SUMS")
	sort.Strings(names)
	return names
}

func validateRemoteAssetNames(release githubRelease, specs []releaseAssetSpec) error {
	actual := releaseAssetNames(release)
	expected := expectedRemoteAssetNames(specs)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("GitHub Release assets do not match the release bundle")
	}
	return nil
}

func validateResumableDraftRelease(
	release githubRelease,
	version releaseVersion,
	specs []releaseAssetSpec,
) error {
	if !release.IsDraft {
		return fmt.Errorf("%s is not a Draft Release", version.tag())
	}
	if release.TagName != version.tag() {
		return fmt.Errorf("draft Release tag does not match %s", version.tag())
	}
	if release.IsPrerelease != (version.channel() == "beta") {
		return fmt.Errorf("draft Release channel does not match %s", version.channel())
	}
	expected := make(map[string]struct{})
	for _, name := range expectedRemoteAssetNames(specs) {
		expected[name] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, asset := range release.Assets {
		if _, ok := expected[asset.Name]; !ok {
			return fmt.Errorf("draft Release contains unexpected asset %s", asset.Name)
		}
		if _, exists := seen[asset.Name]; exists {
			return fmt.Errorf("draft Release contains duplicate asset %s", asset.Name)
		}
		if asset.ID <= 0 {
			return fmt.Errorf("draft Release asset %s is missing its API ID", asset.Name)
		}
		seen[asset.Name] = struct{}{}
	}
	return nil
}

func validateDraftRelease(
	release githubRelease,
	version releaseVersion,
	specs []releaseAssetSpec,
) error {
	if err := validateResumableDraftRelease(release, version, specs); err != nil {
		return err
	}
	return validateRemoteAssetNames(release, specs)
}

func loadGitHubRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
) (githubRelease, error) {
	release, found, err := findGitHubRelease(ctx, runner, repository, tag)
	if err != nil {
		return githubRelease{}, err
	}
	if !found {
		return githubRelease{}, fmt.Errorf("GitHub Release %s was not found in %s", tag, repository)
	}
	return release, nil
}

func ensureCommitExists(
	ctx context.Context,
	runner commandRunner,
	repository string,
	commit string,
) error {
	if _, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/commits/"+commit,
		"--silent",
	); err != nil {
		return fmt.Errorf("commit %s is not available in %s: %w", commit, repository, err)
	}
	return nil
}

func readTagCommit(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
) (string, bool, error) {
	output, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+tag,
	)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect Git tag %s in %s: %w", tag, repository, err)
	}
	var reference githubTagReference
	if err := json.Unmarshal(output, &reference); err != nil {
		return "", false, fmt.Errorf("decode Git tag reference: %w", err)
	}
	if reference.Object.Type == "tag" {
		output, err = runner.run(
			ctx,
			"gh",
			"api",
			"repos/"+repository+"/git/tags/"+reference.Object.SHA,
		)
		if err != nil {
			return "", false, err
		}
		if err := json.Unmarshal(output, &reference); err != nil {
			return "", false, fmt.Errorf("decode annotated Git tag: %w", err)
		}
	}
	if reference.Object.Type != "commit" || !commitPattern.MatchString(reference.Object.SHA) {
		return "", false, fmt.Errorf("tag %s does not resolve to a Git commit", tag)
	}
	return reference.Object.SHA, true, nil
}

func ensureTagMatchesCommit(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
	commit string,
) error {
	actual, found, err := readTagCommit(ctx, runner, repository, tag)
	if err != nil {
		return err
	}
	if !found || actual != commit {
		return fmt.Errorf("tag %s does not point to built commit %s", tag, commit)
	}
	return nil
}

func ensureTagAvailable(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
	commit string,
) error {
	actual, found, err := readTagCommit(ctx, runner, repository, tag)
	if err != nil {
		return err
	}
	if found && actual != commit {
		return fmt.Errorf("tag %s does not point to built commit %s", tag, commit)
	}
	return nil
}

func ensureReleaseTag(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
	commit string,
) error {
	actual, found, err := readTagCommit(ctx, runner, repository, tag)
	if err != nil {
		return err
	}
	if found {
		if actual != commit {
			return fmt.Errorf("tag %s does not point to built commit %s", tag, commit)
		}
		return nil
	}
	// A release tag is the durable identity of public artifacts. Create it once and never move it.
	if _, err := runner.run(
		ctx,
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
	); err != nil {
		if matchErr := ensureTagMatchesCommit(ctx, runner, repository, tag, commit); matchErr == nil {
			return nil
		}
		return fmt.Errorf("create Git tag %s in %s: %w", tag, repository, err)
	}
	return ensureTagMatchesCommit(ctx, runner, repository, tag, commit)
}

func ensureNewestRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
) error {
	output, err := runner.run(
		ctx,
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
	)
	if err != nil {
		return err
	}
	var releases []struct {
		TagName string `json:"tagName"`
	}
	if err := json.Unmarshal(output, &releases); err != nil {
		return fmt.Errorf("decode GitHub Release list: %w", err)
	}
	for _, release := range releases {
		existing, err := parseReleaseVersion(strings.TrimPrefix(release.TagName, "v"))
		if err != nil {
			continue
		}
		if version.compare(existing) <= 0 {
			return fmt.Errorf(
				"%s must be newer than published release %s in %s",
				version,
				existing,
				repository,
			)
		}
	}
	return nil
}

func checkGitHubRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	commit string,
	definitions []releasePlatformDefinition,
) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("release commit must be a full lowercase Git SHA")
	}
	specs, err := combinedAssetSpecs(definitions)
	if err != nil {
		return err
	}
	if _, err := runner.run(ctx, "gh", "auth", "status"); err != nil {
		return err
	}
	if err := ensureCommitExists(ctx, runner, repository, commit); err != nil {
		return err
	}
	if err := ensureNewestRelease(ctx, runner, repository, version); err != nil {
		return err
	}
	release, found, err := findGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return err
	}
	if found {
		if err := validateResumableDraftRelease(release, version, specs); err != nil {
			return err
		}
		return ensureTagMatchesCommit(ctx, runner, repository, version.tag(), commit)
	}
	return ensureTagAvailable(ctx, runner, repository, version.tag(), commit)
}

func downloadReleaseAsset(
	ctx context.Context,
	runner commandRunner,
	repository string,
	asset githubAsset,
) ([]byte, error) {
	if asset.ID <= 0 {
		return nil, fmt.Errorf("GitHub Release asset %s is missing its API ID", asset.Name)
	}
	return runner.run(
		ctx,
		"gh",
		"api",
		"-H",
		"Accept: application/octet-stream",
		fmt.Sprintf("repos/%s/releases/assets/%d", repository, asset.ID),
	)
}

func downloadReleaseAssets(
	ctx context.Context,
	runner commandRunner,
	repository string,
	release githubRelease,
	directory string,
	specs []releaseAssetSpec,
) error {
	if err := validateRemoteAssetNames(release, specs); err != nil {
		return err
	}
	assets := make(map[string]githubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset
	}
	for _, name := range expectedRemoteAssetNames(specs) {
		content, err := downloadReleaseAsset(ctx, runner, repository, assets[name])
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			return fmt.Errorf("write downloaded GitHub Release asset %s: %w", name, err)
		}
	}
	return verifyRemoteDirectoryLayout(directory, specs)
}

func verifyRemoteAssets(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	localDirectory string,
	specs []releaseAssetSpec,
) error {
	release, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return err
	}
	if err := validateDraftRelease(release, version, specs); err != nil {
		return err
	}
	downloadDirectory, err := os.MkdirTemp("", "profiledeck-release-download-*")
	if err != nil {
		return fmt.Errorf("create release download directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(downloadDirectory)
	}()
	if err := downloadReleaseAssets(
		ctx,
		runner,
		repository,
		release,
		downloadDirectory,
		specs,
	); err != nil {
		return err
	}
	localChecksums, err := os.ReadFile(filepath.Join(localDirectory, "SHA256SUMS"))
	if err != nil {
		return fmt.Errorf("read local SHA256SUMS: %w", err)
	}
	remoteChecksums, err := os.ReadFile(filepath.Join(downloadDirectory, "SHA256SUMS"))
	if err != nil {
		return fmt.Errorf("read downloaded SHA256SUMS: %w", err)
	}
	if !bytes.Equal(localChecksums, remoteChecksums) {
		return fmt.Errorf("downloaded SHA256SUMS does not match the local release")
	}
	if _, err := verifyChecksums(downloadDirectory, specs); err != nil {
		return fmt.Errorf("verify downloaded release assets: %w", err)
	}
	finalRelease, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return err
	}
	return validateDraftRelease(finalRelease, version, specs)
}

func createDraftRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	directory string,
	metadata releaseBundleMetadata,
	definitions []releasePlatformDefinition,
) (githubRelease, error) {
	// The verified aggregate manifest is the publication allowlist for release assets.
	specs, err := bundleAssetSpecs(metadata, definitions)
	if err != nil {
		return githubRelease{}, err
	}
	if err := checkGitHubRelease(
		ctx,
		runner,
		repository,
		version,
		metadata.Commit,
		definitions,
	); err != nil {
		return githubRelease{}, err
	}
	if err := ensureReleaseTag(ctx, runner, repository, version.tag(), metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	release, found, err := findGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if !found {
		if _, err := runner.run(ctx, "gh", draftReleaseArgs(repository, version)...); err != nil {
			return githubRelease{}, err
		}
		release, err = loadGitHubRelease(ctx, runner, repository, version.tag())
		if err != nil {
			return githubRelease{}, err
		}
	}
	if err := validateResumableDraftRelease(release, version, specs); err != nil {
		return githubRelease{}, err
	}
	if err := verifyExistingDraftAssets(
		ctx,
		runner,
		repository,
		release,
		directory,
	); err != nil {
		return githubRelease{}, err
	}
	if err := uploadMissingDraftAssets(
		ctx,
		runner,
		repository,
		version,
		release,
		directory,
		specs,
	); err != nil {
		return githubRelease{}, err
	}
	release, err = loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if err := validateDraftRelease(release, version, specs); err != nil {
		return githubRelease{}, err
	}
	if err := verifyRemoteAssets(ctx, runner, repository, version, directory, specs); err != nil {
		return githubRelease{}, err
	}
	if err := ensureTagMatchesCommit(ctx, runner, repository, version.tag(), metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func verifyExistingDraftAssets(
	ctx context.Context,
	runner commandRunner,
	repository string,
	release githubRelease,
	directory string,
) error {
	for _, asset := range release.Assets {
		local, err := os.ReadFile(filepath.Join(directory, asset.Name))
		if err != nil {
			return fmt.Errorf("read local release asset %s: %w", asset.Name, err)
		}
		remote, err := downloadReleaseAsset(ctx, runner, repository, asset)
		if err != nil {
			return err
		}
		if !bytes.Equal(local, remote) {
			return fmt.Errorf("existing Draft Release asset %s does not match the release bundle", asset.Name)
		}
	}
	return nil
}

func uploadMissingDraftAssets(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	release githubRelease,
	directory string,
	specs []releaseAssetSpec,
) error {
	existing := make(map[string]struct{}, len(release.Assets))
	for _, asset := range release.Assets {
		existing[asset.Name] = struct{}{}
	}
	for _, name := range expectedRemoteAssetNames(specs) {
		if _, found := existing[name]; found {
			continue
		}
		if _, err := runner.run(
			ctx,
			"gh",
			"release",
			"upload",
			version.tag(),
			filepath.Join(directory, name),
			"--repo",
			repository,
		); err != nil {
			return fmt.Errorf("upload Draft Release asset %s: %w", name, err)
		}
	}
	return nil
}

func draftReleaseArgs(repository string, version releaseVersion) []string {
	args := []string{
		"release",
		"create",
		version.tag(),
		"--repo",
		repository,
		"--verify-tag",
		"--draft",
		"--generate-notes",
		"--title",
		"ProfileDeck " + version.String(),
	}
	if version.channel() == "beta" {
		args = append(args, "--prerelease", "--latest=false")
	}
	return args
}
