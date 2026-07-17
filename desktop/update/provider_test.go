package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	githubprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestChannelForVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version string
		want    releaseChannel
	}{
		{version: "1.2.3", want: releaseChannelStable},
		{version: "1.2.3-beta.1", want: releaseChannelPrerelease},
		{version: "1.2.3-beta.12", want: releaseChannelPrerelease},
		{version: "dev", want: releaseChannelInvalid},
		{version: "v1.2.3", want: releaseChannelInvalid},
		{version: "1.2.3-alpha.1", want: releaseChannelInvalid},
		{version: "1.2.3-beta.0", want: releaseChannelInvalid},
		{version: "1.2.3+build", want: releaseChannelInvalid},
		{version: "01.2.3", want: releaseChannelInvalid},
		{version: "1.02.3", want: releaseChannelInvalid},
		{version: "1.2.03", want: releaseChannelInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			if got := channelForVersion(test.version); got != test.want {
				t.Fatalf("channelForVersion(%q) = %d, want %d", test.version, got, test.want)
			}
		})
	}
}

func TestUniversalAssetMatcher(t *testing.T) {
	t.Parallel()
	request := updater.CheckRequest{Platform: UpdatePlatform, Arch: "arm64"}
	assets := []githubprovider.ReleaseAsset{
		{Name: "ProfileDeck_1.2.3_macos_universal.dmg"},
		{Name: ChecksumAsset},
		{Name: "ProfileDeck_1.2.3_macos_universal.zip"},
		{Name: "ProfileDeck_1.2.3_darwin_arm64.zip"},
	}
	if got := universalAssetMatcher(request, assets); got != 2 {
		t.Fatalf("universalAssetMatcher() = %d, want 2", got)
	}
	if got := universalAssetMatcher(
		request,
		append(assets, githubprovider.ReleaseAsset{Name: "ProfileDeck_1.2.4_macos_universal.zip"}),
	); got != -1 {
		t.Fatalf("ambiguous matcher result = %d, want -1", got)
	}
	if got := universalAssetMatcher(
		updater.CheckRequest{Platform: "windows", Arch: "amd64"},
		assets,
	); got != -1 {
		t.Fatalf("non-macOS matcher result = %d, want -1", got)
	}
}

func TestGitHubProviderSelectsStableAndPrereleaseEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		currentVersion    string
		releaseVersion    string
		releasePrerelease bool
		wantPathSuffix    string
		wantChannel       string
	}{
		{
			name:           "stable",
			currentVersion: "1.0.0",
			releaseVersion: "1.1.0",
			wantPathSuffix: "/releases/latest",
			wantChannel:    "stable",
		},
		{
			name:              "beta",
			currentVersion:    "1.1.0-beta.1",
			releaseVersion:    "1.1.0-beta.2",
			releasePrerelease: true,
			wantPathSuffix:    "/releases?per_page=10",
			wantChannel:       "prerelease",
		},
		{
			name:           "beta to stable",
			currentVersion: "1.1.0-beta.2",
			releaseVersion: "1.1.0",
			wantPathSuffix: "/releases?per_page=10",
			wantChannel:    "stable",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newGitHubFixture(t, githubFixtureConfig{
				Version:    test.releaseVersion,
				Prerelease: test.releasePrerelease,
				Checksum:   true,
			})
			provider := fixture.provider(t, test.currentVersion)
			release, err := provider.Check(context.Background(), updater.CheckRequest{
				CurrentVersion: test.currentVersion,
				Platform:       UpdatePlatform,
				Arch:           "arm64",
			})
			if err != nil {
				t.Fatal(err)
			}
			if release == nil || release.Version != test.releaseVersion || release.Channel != test.wantChannel {
				t.Fatalf("unexpected release: %#v", release)
			}
			if got := fixture.apiPath(); !strings.HasSuffix(got, test.wantPathSuffix) {
				t.Fatalf("GitHub API path = %q, want suffix %q", got, test.wantPathSuffix)
			}
			if release.Notes != "Release notes from GitHub" {
				t.Fatalf("release notes = %q", release.Notes)
			}
		})
	}
}

func TestGitHubProviderSwitchesChannelsWithoutReinitialization(t *testing.T) {
	t.Parallel()
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:  "1.1.0",
		Checksum: true,
	})
	provider := fixture.provider(t, "1.0.0")
	request := updater.CheckRequest{
		CurrentVersion: "1.0.0",
		Platform:       UpdatePlatform,
		Arch:           "arm64",
	}
	if _, err := provider.Check(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := fixture.apiPath(); !strings.HasSuffix(got, "/releases/latest") {
		t.Fatalf("stable API path = %q", got)
	}
	if err := provider.SetChannel(ChannelBeta); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Check(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := fixture.apiPath(); !strings.HasSuffix(got, "/releases?per_page=10") {
		t.Fatalf("beta API path = %q", got)
	}
}

func TestGitHubProviderStableBuildRejectsPrerelease(t *testing.T) {
	t.Parallel()
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:    "1.1.0-beta.1",
		Prerelease: true,
		Checksum:   true,
	})
	provider := fixture.provider(t, "1.0.0")
	_, err := provider.Check(context.Background(), updater.CheckRequest{
		CurrentVersion: "1.0.0",
		Platform:       UpdatePlatform,
		Arch:           "arm64",
	})
	if ErrorCode(err) != ErrorFeedInvalid {
		t.Fatalf("error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestGitHubProviderDoesNotDowngrade(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		currentVersion string
		releaseVersion string
		prerelease     bool
	}{
		{
			name:           "stable",
			currentVersion: "1.1.0",
			releaseVersion: "1.0.9",
		},
		{
			name:           "prerelease",
			currentVersion: "1.1.0-beta.2",
			releaseVersion: "1.1.0-beta.1",
			prerelease:     true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newGitHubFixture(t, githubFixtureConfig{
				Version:    test.releaseVersion,
				Prerelease: test.prerelease,
				Checksum:   true,
			})
			provider := fixture.provider(t, test.currentVersion)
			release, err := provider.Check(context.Background(), updater.CheckRequest{
				CurrentVersion: test.currentVersion,
				Platform:       UpdatePlatform,
				Arch:           "arm64",
			})
			if err != nil || release != nil {
				t.Fatalf("downgrade check returned release=%#v err=%v", release, err)
			}
		})
	}
}

func TestGitHubProviderRequiresMatchingChecksum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		checksum     bool
		checksumBody string
	}{
		{name: "missing asset"},
		{name: "missing filename", checksum: true, checksumBody: strings.Repeat("0", 64) + "  other.zip\n"},
		{name: "malformed digest", checksum: true, checksumBody: "not-a-sha256  ProfileDeck_1.0.1-beta.2_macos_universal.zip\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newGitHubFixture(t, githubFixtureConfig{
				Version:      "1.0.1-beta.2",
				Prerelease:   true,
				Checksum:     test.checksum,
				ChecksumBody: test.checksumBody,
			})
			provider := fixture.provider(t, "1.0.1-beta.1")
			_, err := provider.Check(context.Background(), updater.CheckRequest{
				CurrentVersion: "1.0.1-beta.1",
				Platform:       UpdatePlatform,
				Arch:           "amd64",
			})
			if ErrorCode(err) != ErrorArtifactVerificationFailed {
				t.Fatalf("error = %v, code = %q", err, ErrorCode(err))
			}
		})
	}
}

func TestGitHubProviderRejectsMissingUniversalAsset(t *testing.T) {
	t.Parallel()
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:      "1.0.1",
		Checksum:     true,
		ArtifactName: "ProfileDeck_1.0.1_macos_universal.dmg",
	})
	provider := fixture.provider(t, "1.0.0")
	_, err := provider.Check(context.Background(), updater.CheckRequest{
		CurrentVersion: "1.0.0",
		Platform:       UpdatePlatform,
		Arch:           "arm64",
	})
	if ErrorCode(err) != ErrorFeedInvalid {
		t.Fatalf("error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestGitHubProviderRejectsMismatchedArtifactVersion(t *testing.T) {
	t.Parallel()
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:      "1.0.2-beta.1",
		Prerelease:   true,
		Checksum:     true,
		ArtifactName: artifactName("1.0.1-beta.9"),
	})
	provider := fixture.provider(t, "1.0.1-beta.8")
	_, err := provider.Check(context.Background(), updater.CheckRequest{
		CurrentVersion: "1.0.1-beta.8",
		Platform:       UpdatePlatform,
		Arch:           "arm64",
	})
	if ErrorCode(err) != ErrorFeedInvalid {
		t.Fatalf("error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestGitHubProviderDownloadsChecksumVerifiedArtifact(t *testing.T) {
	t.Parallel()
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:    "1.0.1-beta.2",
		Prerelease: true,
		Checksum:   true,
	})
	provider := fixture.provider(t, "1.0.1-beta.1")
	release, err := provider.Check(context.Background(), updater.CheckRequest{
		CurrentVersion: "1.0.1-beta.1",
		Platform:       UpdatePlatform,
		Arch:           "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	var downloaded bytes.Buffer
	if err := provider.Download(context.Background(), release, &downloaded, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded.Bytes(), fixture.artifact) {
		t.Fatalf("downloaded artifact = %q", downloaded.Bytes())
	}
	digest := sha256.Sum256(downloaded.Bytes())
	if !bytes.Equal(release.Verification.Digest, digest[:]) {
		t.Fatalf("release digest = %x, want %x", release.Verification.Digest, digest)
	}
}

func TestGitHubProviderDigestMismatchFailsClosed(t *testing.T) {
	t.Parallel()
	version := "1.0.1-beta.2"
	fixture := newGitHubFixture(t, githubFixtureConfig{
		Version:      version,
		Prerelease:   true,
		Checksum:     true,
		ChecksumBody: strings.Repeat("0", 64) + "  " + artifactName(version) + "\n",
	})
	provider := fixture.provider(t, "1.0.1-beta.1")
	engine := updater.New(silentUpdaterHost{})
	if err := engine.Init(updater.Config{
		CurrentVersion: "1.0.1-beta.1",
		Providers:      []updater.Provider{provider},
		Platform:       UpdatePlatform,
		Arch:           "arm64",
		Window:         updater.WindowNone,
	}); err != nil {
		t.Fatal(err)
	}
	if release, err := engine.Check(context.Background()); err != nil || release == nil {
		t.Fatalf("check release: release=%#v err=%v", release, err)
	}
	err := engine.DownloadAndInstall(context.Background())
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("DownloadAndInstall() error = %v, want digest mismatch", err)
	}
}

type githubFixtureConfig struct {
	Version      string
	Prerelease   bool
	Checksum     bool
	ChecksumBody string
	ArtifactName string
}

type githubFixture struct {
	server      *httptest.Server
	artifact    []byte
	lastAPIPath chan string
}

func newGitHubFixture(t *testing.T, config githubFixtureConfig) *githubFixture {
	t.Helper()
	fixture := &githubFixture{
		artifact:    []byte("notarized universal application archive"),
		lastAPIPath: make(chan string, 1),
	}
	artifactFilename := config.ArtifactName
	if artifactFilename == "" {
		artifactFilename = artifactName(config.Version)
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/repos/test/profiledeck/releases"):
			select {
			case fixture.lastAPIPath <- request.URL.RequestURI():
			default:
			}
			payload := map[string]any{
				"tag_name":     "v" + config.Version,
				"name":         "ProfileDeck " + config.Version,
				"body":         "Release notes from GitHub",
				"prerelease":   config.Prerelease,
				"draft":        false,
				"published_at": "2026-07-16T12:00:00Z",
				"html_url":     fixture.server.URL + "/release",
				"assets": []map[string]any{
					{
						"id":                   1,
						"name":                 artifactFilename,
						"content_type":         "application/zip",
						"size":                 len(fixture.artifact),
						"browser_download_url": fixture.server.URL + "/downloads/" + artifactFilename,
					},
				},
			}
			if config.Checksum {
				payload["assets"] = append(payload["assets"].([]map[string]any), map[string]any{
					"id":                   2,
					"name":                 ChecksumAsset,
					"content_type":         "text/plain",
					"size":                 128,
					"browser_download_url": fixture.server.URL + "/downloads/" + ChecksumAsset,
				})
			}
			response.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(request.URL.Path, "/releases") {
				_ = json.NewEncoder(response).Encode([]any{payload})
			} else {
				_ = json.NewEncoder(response).Encode(payload)
			}
		case request.URL.Path == "/downloads/"+artifactFilename:
			response.Header().Set("Content-Type", "application/zip")
			_, _ = response.Write(fixture.artifact)
		case request.URL.Path == "/downloads/"+ChecksumAsset:
			body := config.ChecksumBody
			if body == "" {
				digest := sha256.Sum256(fixture.artifact)
				body = fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), artifactFilename)
			}
			_, _ = io.WriteString(response, body)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *githubFixture) provider(t *testing.T, currentVersion string) *channelGitHubProvider {
	t.Helper()
	provider, err := newGitHubProvider(currentVersion, githubProviderOptions{
		Repository: "test/profiledeck",
		BaseURL:    fixture.server.URL,
		HTTPClient: fixture.server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func (fixture *githubFixture) apiPath() string {
	select {
	case path := <-fixture.lastAPIPath:
		return path
	default:
		return ""
	}
}

type silentUpdaterHost struct{}

func (silentUpdaterHost) Emit(string, ...any) bool { return true }

func (silentUpdaterHost) OnEvent(string, func(any)) func() { return func() {} }

func (silentUpdaterHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }

func (silentUpdaterHost) Quit() {}
