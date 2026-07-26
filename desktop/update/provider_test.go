package update

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	githubprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

func TestManifestAssetMatcherRequiresOneManifest(t *testing.T) {
	t.Parallel()
	request := updater.CheckRequest{}
	if got := manifestAssetMatcher(request, []githubprovider.ReleaseAsset{
		{Name: "ProfileDeck_1.2.3_macos_universal.zip"},
		{Name: releaseartifact.ManifestName},
	}); got != 1 {
		t.Fatalf("manifest match = %d, want 1", got)
	}
	if got := manifestAssetMatcher(request, []githubprovider.ReleaseAsset{
		{Name: releaseartifact.ManifestName},
		{Name: releaseartifact.ManifestName},
	}); got != -1 {
		t.Fatalf("duplicate manifest match = %d, want -1", got)
	}
	if got := manifestAssetMatcher(request, nil); got != -1 {
		t.Fatalf("missing manifest match = %d, want -1", got)
	}
}

func TestGitHubManifestProviderStableAndBeta(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		version    string
		current    string
		prerelease bool
		platform   string
		arch       string
		filetype   string
	}{
		{
			name: "stable macOS", version: "1.0.1", current: "1.0.0",
			platform: "darwin", arch: "arm64", filetype: "zip",
		},
		{
			name: "beta Linux", version: "1.0.1-beta.2", current: "1.0.1-beta.1",
			prerelease: true, platform: "linux", arch: "amd64", filetype: "gz",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newManifestFixture(t, manifestFixtureConfig{
				Version: test.version, Prerelease: test.prerelease,
				Platform: test.platform, Arch: test.arch,
			})
			provider := fixture.provider(t, test.current)
			release, err := provider.Check(context.Background(), updater.CheckRequest{
				CurrentVersion: test.current, Platform: test.platform, Arch: test.arch,
			})
			if err != nil {
				t.Fatal(err)
			}
			expectedName, _ := releaseartifact.UpdaterName(test.version, test.platform, test.arch)
			if release == nil ||
				release.Version != test.version ||
				release.Artifact.Filename != expectedName ||
				release.Artifact.Filetype != test.filetype ||
				release.Name != "GitHub release "+test.version ||
				release.Notes != "Release notes" {
				t.Fatalf("unexpected release: %#v", release)
			}
			expectedChannel := releaseartifact.ChannelStable
			if test.prerelease {
				expectedChannel = releaseartifact.ChannelBeta
			}
			if release.Channel != expectedChannel {
				t.Fatalf("channel = %q, want %q", release.Channel, expectedChannel)
			}
			if release.Verification == nil ||
				release.Verification.DigestAlgo != "sha512" ||
				release.Verification.SignatureAlgo != "ed25519ph" {
				t.Fatalf("unexpected verification: %#v", release.Verification)
			}

			var downloaded bytes.Buffer
			if err := provider.Download(context.Background(), release, &downloaded, func(int64, int64) {}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(downloaded.Bytes(), fixture.artifact) {
				t.Fatalf("download = %q, want %q", downloaded.Bytes(), fixture.artifact)
			}
			if err := ed25519.VerifyWithOptions(
				fixture.publicKey,
				release.Verification.Digest,
				release.Verification.Signature,
				&ed25519.Options{Hash: crypto.SHA512},
			); err != nil {
				t.Fatalf("Wails manifest signature did not verify: %v", err)
			}
		})
	}
}

func TestGitHubManifestProviderSwitchesChannels(t *testing.T) {
	t.Parallel()
	fixture := newManifestFixture(t, manifestFixtureConfig{
		Version: "1.0.1-beta.2", Prerelease: true, Platform: "darwin", Arch: "amd64",
	})
	provider, err := newChannelGitHubProvider(releaseartifact.ChannelStable, githubProviderOptions{
		Repository: "test/profiledeck", BaseURL: fixture.server.URL, HTTPClient: fixture.server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := updater.CheckRequest{CurrentVersion: "1.0.0-beta.1", Platform: "darwin", Arch: "amd64"}
	if _, err := provider.Check(context.Background(), request); ErrorCode(err) != ErrorFeedInvalid {
		t.Fatalf("stable channel error = %v, want %s", err, ErrorFeedInvalid)
	}
	if err := provider.SetChannel(releaseartifact.ChannelBeta); err != nil {
		t.Fatal(err)
	}
	if release, err := provider.Check(context.Background(), request); err != nil || release == nil {
		t.Fatalf("beta channel release = %#v, err=%v", release, err)
	}
}

func TestGitHubManifestProviderRejectsInvalidReleaseContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*manifestFixtureConfig)
		code   string
	}{
		{
			name: "mismatched version",
			mutate: func(config *manifestFixtureConfig) {
				config.ManifestVersion = "1.0.2"
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "mismatched channel",
			mutate: func(config *manifestFixtureConfig) {
				config.ManifestChannel = releaseartifact.ChannelBeta
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "beta version published as stable",
			mutate: func(config *manifestFixtureConfig) {
				config.Version = "1.0.1-beta.1"
				config.ManifestVersion = config.Version
				config.Prerelease = false
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "stable version published as prerelease",
			mutate: func(config *manifestFixtureConfig) {
				config.Prerelease = true
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "wrong artifact",
			mutate: func(config *manifestFixtureConfig) {
				config.ArtifactName = "ProfileDeck_1.0.1_darwin_arm64.zip"
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "missing signature",
			mutate: func(config *manifestFixtureConfig) {
				config.MissingSignature = true
			},
			code: ErrorArtifactVerificationFailed,
		},
		{
			name: "malformed digest",
			mutate: func(config *manifestFixtureConfig) {
				config.Digest = "not-base64"
			},
			code: ErrorArtifactVerificationFailed,
		},
		{
			name: "missing manifest",
			mutate: func(config *manifestFixtureConfig) {
				config.ManifestAssets = -1
			},
			code: ErrorFeedInvalid,
		},
		{
			name: "duplicate manifest",
			mutate: func(config *manifestFixtureConfig) {
				config.ManifestAssets = 2
			},
			code: ErrorFeedInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := manifestFixtureConfig{
				Version: "1.0.1", Platform: "darwin", Arch: "arm64", ManifestAssets: 1,
			}
			test.mutate(&config)
			fixture := newManifestFixture(t, config)
			_, err := fixture.provider(t, "1.0.0").Check(context.Background(), updater.CheckRequest{
				CurrentVersion: "1.0.0", Platform: "darwin", Arch: "arm64",
			})
			if ErrorCode(err) != test.code {
				t.Fatalf("error = %v (%s), want %s", err, ErrorCode(err), test.code)
			}
		})
	}
}

func TestPinnedWailsProviderErrorsKeepStableUserClassifications(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		mapf func(error) error
		code string
	}{
		{
			name: "missing GitHub manifest",
			err:  errors.New("github: release v1.2.3 has no asset for linux/amd64"),
			mapf: githubCheckError,
			code: ErrorFeedInvalid,
		},
		{
			name: "GitHub unavailable",
			err:  errors.New("github: request failed"),
			mapf: githubCheckError,
			code: ErrorFeedUnavailable,
		},
		{
			name: "manifest unavailable",
			err:  errors.New("endpoint: fetch manifest: offline"),
			mapf: endpointCheckError,
			code: ErrorFeedUnavailable,
		},
		{
			name: "manifest verification failed",
			err:  errors.New("artifact digest is not valid base64"),
			mapf: endpointCheckError,
			code: ErrorArtifactVerificationFailed,
		},
		{
			name: "manifest invalid",
			err:  errors.New("endpoint: manifest missing version"),
			mapf: endpointCheckError,
			code: ErrorFeedInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if code := ErrorCode(test.mapf(test.err)); code != test.code {
				t.Fatalf("error code = %q, want %q", code, test.code)
			}
		})
	}
}

func TestGitHubManifestProviderDoesNotDowngrade(t *testing.T) {
	t.Parallel()
	fixture := newManifestFixture(t, manifestFixtureConfig{
		Version: "1.0.1", Platform: "darwin", Arch: "arm64", ManifestAssets: 1,
	})
	release, err := fixture.provider(t, "1.0.2").Check(context.Background(), updater.CheckRequest{
		CurrentVersion: "1.0.2", Platform: "darwin", Arch: "arm64",
	})
	if err != nil || release != nil {
		t.Fatalf("downgrade result = %#v, err=%v", release, err)
	}
}

func TestTrustedGitHubAssetURLIsExact(t *testing.T) {
	t.Parallel()
	valid := "https://github.com/strahe/profiledeck/releases/download/v1.2.3/updates.json"
	if !trustedGitHubAssetURL(valid, "1.2.3", "updates.json") {
		t.Fatal("exact GitHub release URL was rejected")
	}
	for _, value := range []string{
		"http://github.com/strahe/profiledeck/releases/download/v1.2.3/updates.json",
		"https://github.com/strahe/profiledeck/releases/download/v1.2.4/updates.json",
		"https://github.com/strahe/profiledeck/releases/download/v1.2.3/updates.json?token=x",
		"https://evil.example/strahe/profiledeck/releases/download/v1.2.3/updates.json",
	} {
		if trustedGitHubAssetURL(value, "1.2.3", "updates.json") {
			t.Fatalf("untrusted URL accepted: %s", value)
		}
	}
}

type manifestFixtureConfig struct {
	Version          string
	ManifestVersion  string
	ManifestChannel  string
	Prerelease       bool
	Platform         string
	Arch             string
	ArtifactName     string
	Digest           string
	MissingSignature bool
	ManifestAssets   int
}

type manifestFixture struct {
	server     *httptest.Server
	artifact   []byte
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	config     manifestFixtureConfig
}

func newManifestFixture(t *testing.T, config manifestFixtureConfig) *manifestFixture {
	t.Helper()
	if config.ManifestAssets == 0 {
		config.ManifestAssets = 1
	} else if config.ManifestAssets < 0 {
		config.ManifestAssets = 0
	}
	if config.ManifestVersion == "" {
		config.ManifestVersion = config.Version
	}
	if config.ManifestChannel == "" {
		version, err := releaseartifact.ParseVersion(config.ManifestVersion)
		if err == nil {
			config.ManifestChannel = version.Channel()
		}
	}
	if config.ArtifactName == "" {
		config.ArtifactName, _ = releaseartifact.UpdaterName(config.ManifestVersion, config.Platform, config.Arch)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &manifestFixture{
		artifact: []byte("signed ProfileDeck update"), publicKey: publicKey,
		privateKey: privateKey, config: config,
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *manifestFixture) provider(t *testing.T, current string) *channelGitHubProvider {
	t.Helper()
	provider, err := newGitHubProvider(current, githubProviderOptions{
		Repository: "test/profiledeck",
		BaseURL:    fixture.server.URL,
		HTTPClient: fixture.server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func (fixture *manifestFixture) serveHTTP(response http.ResponseWriter, request *http.Request) {
	switch {
	case strings.HasSuffix(request.URL.Path, "/releases/latest"):
		fixture.writeGitHubRelease(response, false)
	case strings.HasSuffix(request.URL.Path, "/releases"):
		fixture.writeGitHubRelease(response, true)
	case request.URL.Path == "/downloads/"+releaseartifact.ManifestName:
		fixture.writeManifest(response)
	case request.URL.Path == "/downloads/"+fixture.config.ArtifactName:
		response.Header().Set("Content-Length", fmt.Sprint(len(fixture.artifact)))
		_, _ = response.Write(fixture.artifact)
	default:
		http.NotFound(response, request)
	}
}

func (fixture *manifestFixture) writeGitHubRelease(response http.ResponseWriter, list bool) {
	assets := make([]map[string]any, 0, fixture.config.ManifestAssets)
	for index := 0; index < fixture.config.ManifestAssets; index++ {
		assets = append(assets, map[string]any{
			"id": index + 1, "name": releaseartifact.ManifestName,
			"content_type": "application/json", "size": 1,
			"browser_download_url": fixture.server.URL + "/downloads/" + releaseartifact.ManifestName,
		})
	}
	release := map[string]any{
		"tag_name": "v" + fixture.config.Version,
		"name":     "GitHub release " + fixture.config.Version,
		"body":     "Release notes",
		"draft":    false, "prerelease": fixture.config.Prerelease,
		"published_at": "2026-01-02T03:04:05Z",
		"html_url":     fixture.server.URL + "/release",
		"assets":       assets,
	}
	response.Header().Set("Content-Type", "application/json")
	if list {
		_ = json.NewEncoder(response).Encode([]any{release})
		return
	}
	_ = json.NewEncoder(response).Encode(release)
}

func (fixture *manifestFixture) writeManifest(response http.ResponseWriter) {
	digest := sha512.Sum512(fixture.artifact)
	digestText := base64.StdEncoding.EncodeToString(digest[:])
	if fixture.config.Digest != "" {
		digestText = fixture.config.Digest
	}
	signature, _ := fixture.privateKey.Sign(nil, digest[:], &ed25519.Options{Hash: crypto.SHA512})
	signatureAlgo := "ed25519ph"
	signatureText := base64.StdEncoding.EncodeToString(signature)
	if fixture.config.MissingSignature {
		signatureAlgo = ""
		signatureText = ""
	}
	manifest := map[string]any{
		"schemaVersion": 1,
		"version":       fixture.config.ManifestVersion,
		"channel":       fixture.config.ManifestChannel,
		"publishedAt":   "2026-01-02T03:04:05Z",
		"artifacts": []map[string]any{{
			"url": fixture.config.ArtifactName, "platform": fixture.config.Platform,
			"arch": fixture.config.Arch, "size": len(fixture.artifact),
			"digestAlgo": "sha512", "digest": digestText,
			"signatureAlgo": signatureAlgo, "signature": signatureText,
		}},
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(manifest)
}
