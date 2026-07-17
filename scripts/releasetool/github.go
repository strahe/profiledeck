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
	ID              int64         `json:"id"`
	Body            string        `json:"body"`
	IsDraft         bool          `json:"isDraft"`
	IsPrerelease    bool          `json:"isPrerelease"`
	TagName         string        `json:"tagName"`
	TargetCommitish string        `json:"targetCommitish"`
	URL             string        `json:"url"`
	Assets          []githubAsset `json:"assets"`
}

type githubAPIRelease struct {
	ID              int64         `json:"id"`
	Body            string        `json:"body"`
	Draft           bool          `json:"draft"`
	Prerelease      bool          `json:"prerelease"`
	TagName         string        `json:"tag_name"`
	TargetCommitish string        `json:"target_commitish"`
	HTMLURL         string        `json:"html_url"`
	Assets          []githubAsset `json:"assets"`
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
				ID:              release.ID,
				Body:            release.Body,
				IsDraft:         release.Draft,
				IsPrerelease:    release.Prerelease,
				TagName:         release.TagName,
				TargetCommitish: release.TargetCommitish,
				URL:             release.HTMLURL,
				Assets:          release.Assets,
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
	for _, release := range releases {
		if release.TagName == tag {
			return release, true, nil
		}
	}
	return githubRelease{}, false, nil
}

func releaseAssetNames(release githubRelease) []string {
	names := make([]string, 0, len(release.Assets))
	for _, asset := range release.Assets {
		names = append(names, asset.Name)
	}
	sort.Strings(names)
	return names
}

func expectedRemoteAssetNames(version releaseVersion) []string {
	names := append(expectedAssetNames(version), "SHA256SUMS")
	sort.Strings(names)
	return names
}

func validateRemoteAssetNames(release githubRelease, version releaseVersion) error {
	actual := releaseAssetNames(release)
	expected := expectedRemoteAssetNames(version)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("GitHub Release assets must be exactly the updater ZIP, installer DMG, and SHA256SUMS")
	}
	return nil
}

func validateDraftRelease(
	release githubRelease,
	version releaseVersion,
	commit string,
	requireBody bool,
) error {
	if !release.IsDraft {
		return fmt.Errorf("%s is not a Draft Release", version.tag())
	}
	if release.TagName != version.tag() {
		return fmt.Errorf("draft Release tag does not match %s", version.tag())
	}
	if release.TargetCommitish != commit {
		return fmt.Errorf("draft Release target does not match the built commit")
	}
	if release.IsPrerelease != (version.channel() == "beta") {
		return fmt.Errorf("draft Release channel does not match %s", version.channel())
	}
	if requireBody && strings.TrimSpace(release.Body) == "" {
		return fmt.Errorf("write the GitHub Release notes before publishing")
	}
	return validateRemoteAssetNames(release, version)
}

func validatePublishedRelease(release githubRelease, version releaseVersion, commit string) error {
	if release.IsDraft {
		return fmt.Errorf("%s remained a Draft Release", version.tag())
	}
	if release.TagName != version.tag() || release.TargetCommitish != commit {
		return fmt.Errorf("published Release tag or target commit does not match")
	}
	if release.IsPrerelease != (version.channel() == "beta") {
		return fmt.Errorf("published Release channel does not match %s", version.channel())
	}
	return validateRemoteAssetNames(release, version)
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

func ensureTagAbsent(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
) error {
	if _, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+tag,
		"--silent",
	); err == nil {
		return fmt.Errorf("tag %s already exists in %s", tag, repository)
	} else if !strings.Contains(err.Error(), "HTTP 404") {
		return fmt.Errorf("inspect Git tag %s in %s: %w", tag, repository, err)
	}
	return nil
}

func ensureTagMatchesCommit(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
	commit string,
) error {
	output, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+tag,
	)
	if err != nil {
		return err
	}
	var reference githubTagReference
	if err := json.Unmarshal(output, &reference); err != nil {
		return fmt.Errorf("decode Git tag reference: %w", err)
	}
	if reference.Object.Type == "tag" {
		output, err = runner.run(
			ctx,
			"gh",
			"api",
			"repos/"+repository+"/git/tags/"+reference.Object.SHA,
		)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(output, &reference); err != nil {
			return fmt.Errorf("decode annotated Git tag: %w", err)
		}
	}
	if reference.Object.Type != "commit" || reference.Object.SHA != commit {
		return fmt.Errorf("tag %s does not point to built commit %s", tag, commit)
	}
	return nil
}

func ensureTagReadyForPublish(
	ctx context.Context,
	runner commandRunner,
	repository string,
	tag string,
	commit string,
) error {
	if _, err := runner.run(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/git/ref/tags/"+tag,
		"--silent",
	); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return fmt.Errorf("inspect Git tag %s in %s: %w", tag, repository, err)
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

func verifyRemoteAssets(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	localDirectory string,
) error {
	release, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return err
	}
	if err := validateRemoteAssetNames(release, version); err != nil {
		return err
	}
	downloadDirectory, err := os.MkdirTemp("", "profiledeck-release-download-*")
	if err != nil {
		return fmt.Errorf("create release download directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(downloadDirectory)
	}()
	assets := make(map[string]githubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset
	}
	for _, name := range expectedRemoteAssetNames(version) {
		asset := assets[name]
		if asset.ID <= 0 {
			return fmt.Errorf("GitHub Release asset %s is missing its API ID", name)
		}
		content, err := runner.run(
			ctx,
			"gh",
			"api",
			"-H",
			"Accept: application/octet-stream",
			fmt.Sprintf("repos/%s/releases/assets/%d", repository, asset.ID),
		)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(downloadDirectory, name), content, 0o600); err != nil {
			return fmt.Errorf("write downloaded GitHub Release asset %s: %w", name, err)
		}
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
	if _, err := verifyChecksums(downloadDirectory, version); err != nil {
		return fmt.Errorf("verify downloaded release assets: %w", err)
	}
	return nil
}

func createDraftRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	directory string,
	metadata releaseMetadata,
) (githubRelease, error) {
	if err := validateRepository(repository); err != nil {
		return githubRelease{}, err
	}
	if _, err := runner.run(ctx, "gh", "auth", "status"); err != nil {
		return githubRelease{}, err
	}
	if err := ensureCommitExists(ctx, runner, repository, metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	if err := ensureNewestRelease(ctx, runner, repository, version); err != nil {
		return githubRelease{}, err
	}
	existing, found, err := findGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if found {
		if err := validateDraftRelease(existing, version, metadata.Commit, false); err != nil {
			return githubRelease{}, fmt.Errorf("release %s already exists: %w", version.tag(), err)
		}
		if err := verifyRemoteAssets(ctx, runner, repository, version, directory); err != nil {
			return githubRelease{}, err
		}
		return existing, nil
	}
	if err := ensureTagAbsent(ctx, runner, repository, version.tag()); err != nil {
		return githubRelease{}, err
	}
	args := []string{
		"release",
		"create",
		version.tag(),
		filepath.Join(directory, updaterZIPName(version)),
		filepath.Join(directory, installerDMGName(version)),
		filepath.Join(directory, "SHA256SUMS"),
		"--repo",
		repository,
		"--target",
		metadata.Commit,
		"--draft",
		"--generate-notes",
		"--title",
		"ProfileDeck " + version.String(),
	}
	if version.channel() == "beta" {
		args = append(args, "--prerelease", "--latest=false")
	}
	if err := runVisible(ctx, "gh", args...); err != nil {
		return githubRelease{}, err
	}
	release, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if err := validateDraftRelease(release, version, metadata.Commit, false); err != nil {
		return githubRelease{}, err
	}
	if err := verifyRemoteAssets(ctx, runner, repository, version, directory); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func publishRelease(
	ctx context.Context,
	runner commandRunner,
	repository string,
	version releaseVersion,
	directory string,
	metadata releaseMetadata,
) (githubRelease, error) {
	if err := validateRepository(repository); err != nil {
		return githubRelease{}, err
	}
	if _, err := runner.run(ctx, "gh", "auth", "status"); err != nil {
		return githubRelease{}, err
	}
	release, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if err := validateDraftRelease(release, version, metadata.Commit, true); err != nil {
		return githubRelease{}, err
	}
	if err := ensureCommitExists(ctx, runner, repository, metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	if err := ensureNewestRelease(ctx, runner, repository, version); err != nil {
		return githubRelease{}, err
	}
	if err := ensureTagReadyForPublish(
		ctx,
		runner,
		repository,
		version.tag(),
		metadata.Commit,
	); err != nil {
		return githubRelease{}, err
	}
	if err := verifyRemoteAssets(ctx, runner, repository, version, directory); err != nil {
		return githubRelease{}, err
	}
	if release.ID <= 0 {
		return githubRelease{}, fmt.Errorf("draft Release is missing its API ID")
	}
	prerelease := version.channel() == "beta"
	makeLatest := "true"
	if version.channel() == "beta" {
		makeLatest = "false"
	}
	if _, err := runner.run(
		ctx,
		"gh",
		"api",
		fmt.Sprintf("repos/%s/releases/%d", repository, release.ID),
		"--method",
		"PATCH",
		"-F",
		"draft=false",
		"-F",
		fmt.Sprintf("prerelease=%t", prerelease),
		"-f",
		"make_latest="+makeLatest,
	); err != nil {
		return githubRelease{}, err
	}
	published, err := loadGitHubRelease(ctx, runner, repository, version.tag())
	if err != nil {
		return githubRelease{}, err
	}
	if err := validatePublishedRelease(published, version, metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	if err := ensureTagMatchesCommit(ctx, runner, repository, version.tag(), metadata.Commit); err != nil {
		return githubRelease{}, err
	}
	if err := verifyRemoteAssets(ctx, runner, repository, version, directory); err != nil {
		return githubRelease{}, err
	}
	return published, nil
}
