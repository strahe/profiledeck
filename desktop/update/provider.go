// Package update owns the Wails-specific Desktop update runtime.
package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	githubprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"github.com/strahe/profiledeck/internal/settings"
)

const (
	UpdateRepository = "strahe/profiledeck"
	UpdatePlatform   = "darwin"
	ChecksumAsset    = "SHA256SUMS"
	ChannelStable    = settings.DesktopUpdateChannelStable
	ChannelBeta      = settings.DesktopUpdateChannelBeta

	ErrorFeedUnavailable            = "feed_unavailable"
	ErrorFeedInvalid                = "feed_invalid"
	ErrorArtifactVerificationFailed = "artifact_verification_failed"
)

var (
	stableVersionPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)
	betaVersionPattern   = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-beta\.[1-9][0-9]*$`)
	artifactNamePattern  = regexp.MustCompile(`^ProfileDeck_(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-beta\.[1-9][0-9]*)?_macos_universal\.zip$`)
)

type codedError struct {
	code  string
	cause error
}

func (err *codedError) Error() string {
	if err.cause == nil {
		return err.code
	}
	return fmt.Sprintf("%s: %v", err.code, err.cause)
}

func (err *codedError) Unwrap() error { return err.cause }

func updateError(code string, cause error) error {
	return &codedError{code: code, cause: cause}
}

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return "update_failed"
}

type releaseChannel int

const (
	releaseChannelInvalid releaseChannel = iota
	releaseChannelStable
	releaseChannelPrerelease
)

func channelForVersion(version string) releaseChannel {
	version = strings.TrimSpace(version)
	switch {
	case stableVersionPattern.MatchString(version):
		return releaseChannelStable
	case betaVersionPattern.MatchString(version):
		return releaseChannelPrerelease
	default:
		return releaseChannelInvalid
	}
}

func artifactName(version string) string {
	return fmt.Sprintf("ProfileDeck_%s_macos_universal.zip", version)
}

type githubProviderOptions struct {
	Repository string
	BaseURL    string
	HTTPClient *http.Client
}

// channelGitHubProvider lets the Desktop switch update channels without
// reinitializing Wails' process-scoped updater.
type channelGitHubProvider struct {
	mu      sync.RWMutex
	channel string
	stable  *verifiedGitHubProvider
	beta    *verifiedGitHubProvider
}

// verifiedGitHubProvider delegates GitHub API and download behaviour to Wails,
// then enforces ProfileDeck's immutable asset naming and checksum contract.
type verifiedGitHubProvider struct {
	delegate      *githubprovider.Provider
	channel       string
	trustedSource bool
}

func newGitHubProvider(version string, options githubProviderOptions) (*channelGitHubProvider, error) {
	buildChannel := channelForVersion(version)
	if buildChannel == releaseChannelInvalid {
		return nil, errors.New("release version must be X.Y.Z or X.Y.Z-beta.N")
	}
	return newChannelGitHubProvider(channelName(buildChannel), options)
}

func newChannelGitHubProvider(channel string, options githubProviderOptions) (*channelGitHubProvider, error) {
	channel, err := normalizeChannel(channel)
	if err != nil {
		return nil, err
	}
	stable, err := newVerifiedGitHubProvider(ChannelStable, options)
	if err != nil {
		return nil, err
	}
	beta, err := newVerifiedGitHubProvider(ChannelBeta, options)
	if err != nil {
		return nil, err
	}
	return &channelGitHubProvider{channel: channel, stable: stable, beta: beta}, nil
}

func newVerifiedGitHubProvider(channel string, options githubProviderOptions) (*verifiedGitHubProvider, error) {
	repository := strings.TrimSpace(options.Repository)
	if repository == "" {
		repository = UpdateRepository
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Minute,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				if request.URL.Scheme != "https" {
					return errors.New("update redirect must use HTTPS")
				}
				return nil
			},
		}
	}
	delegate, err := githubprovider.New(githubprovider.Config{
		Repository:    repository,
		Prerelease:    channel == ChannelBeta,
		BaseURL:       options.BaseURL,
		AssetMatcher:  universalAssetMatcher,
		ChecksumAsset: ChecksumAsset,
		HTTPClient:    client,
	})
	if err != nil {
		return nil, err
	}
	return &verifiedGitHubProvider{
		delegate:      delegate,
		channel:       channel,
		trustedSource: repository == UpdateRepository && strings.TrimSpace(options.BaseURL) == "",
	}, nil
}

func (provider *channelGitHubProvider) Name() string {
	return provider.stable.Name()
}

func (provider *channelGitHubProvider) Channel() string {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	return provider.channel
}

func (provider *channelGitHubProvider) SetChannel(channel string) error {
	channel, err := normalizeChannel(channel)
	if err != nil {
		return err
	}
	provider.mu.Lock()
	provider.channel = channel
	provider.mu.Unlock()
	return nil
}

func (provider *channelGitHubProvider) Check(
	ctx context.Context,
	request updater.CheckRequest,
) (*updater.Release, error) {
	provider.mu.RLock()
	delegate := provider.stable
	if provider.channel == ChannelBeta {
		delegate = provider.beta
	}
	provider.mu.RUnlock()
	return delegate.Check(ctx, request)
}

func (provider *channelGitHubProvider) Download(
	ctx context.Context,
	release *updater.Release,
	destination io.Writer,
	onProgress func(written, total int64),
) error {
	// Both delegates share the same repository, asset matcher, and download
	// contract; prerelease selection affects Check only.
	return provider.stable.Download(ctx, release, destination, onProgress)
}

func (provider *verifiedGitHubProvider) Name() string {
	return provider.delegate.Name()
}

func (provider *verifiedGitHubProvider) Check(
	ctx context.Context,
	request updater.CheckRequest,
) (*updater.Release, error) {
	if request.Platform != UpdatePlatform {
		return nil, updateError(ErrorFeedInvalid, errors.New("unsupported updater platform"))
	}
	if channelForVersion(request.CurrentVersion) == releaseChannelInvalid {
		return nil, updateError(ErrorFeedInvalid, errors.New("running version is not releasable"))
	}
	release, err := provider.delegate.Check(ctx, request)
	if err != nil {
		return nil, githubCheckError(err)
	}
	if release == nil {
		return nil, nil
	}
	if channelForVersion(release.Version) == releaseChannelInvalid {
		return nil, updateError(ErrorFeedInvalid, errors.New("release version is not supported"))
	}
	if provider.channel == ChannelStable && release.Channel != "stable" {
		return nil, updateError(ErrorFeedInvalid, errors.New("stable build received a prerelease"))
	}
	if release.Channel != "stable" && release.Channel != "prerelease" {
		return nil, updateError(ErrorFeedInvalid, errors.New("release channel is not supported"))
	}
	if release.Artifact.Filename != artifactName(release.Version) ||
		release.Artifact.Filetype != "zip" ||
		release.Artifact.Platform != UpdatePlatform {
		return nil, updateError(ErrorFeedInvalid, errors.New("release asset does not match the ProfileDeck macOS package"))
	}
	if provider.trustedSource {
		assetURL, _ := release.Metadata["github.asset.url"].(string)
		parsedURL, err := url.Parse(assetURL)
		expectedPath := fmt.Sprintf(
			"/%s/releases/download/v%s/%s",
			UpdateRepository,
			release.Version,
			release.Artifact.Filename,
		)
		if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "github.com" ||
			parsedURL.EscapedPath() != expectedPath {
			return nil, updateError(ErrorFeedInvalid, errors.New("release asset URL is not trusted"))
		}
	}
	if release.Verification == nil ||
		release.Verification.DigestAlgo != "sha256" ||
		len(release.Verification.Digest) != 32 {
		return nil, updateError(
			ErrorArtifactVerificationFailed,
			errors.New("release asset is missing its SHA-256 checksum"),
		)
	}
	return release, nil
}

func githubCheckError(err error) error {
	message := err.Error()
	switch {
	case strings.Contains(message, "github: load checksum sidecar:"):
		return updateError(ErrorArtifactVerificationFailed, err)
	case strings.Contains(message, " has no asset for "),
		strings.Contains(message, "github: decode release"):
		return updateError(ErrorFeedInvalid, err)
	default:
		return updateError(ErrorFeedUnavailable, err)
	}
}

func (provider *verifiedGitHubProvider) Download(
	ctx context.Context,
	release *updater.Release,
	destination io.Writer,
	onProgress func(written, total int64),
) error {
	if err := provider.delegate.Download(ctx, release, destination, onProgress); err != nil {
		return updateError(ErrorFeedUnavailable, err)
	}
	return nil
}

func universalAssetMatcher(request updater.CheckRequest, assets []githubprovider.ReleaseAsset) int {
	if request.Platform != UpdatePlatform {
		return -1
	}
	match := -1
	for index, asset := range assets {
		if !artifactNamePattern.MatchString(asset.Name) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	return match
}

func channelName(channel releaseChannel) string {
	if channel == releaseChannelPrerelease {
		return ChannelBeta
	}
	return ChannelStable
}

func normalizeChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case ChannelStable:
		return ChannelStable, nil
	case ChannelBeta:
		return ChannelBeta, nil
	default:
		return "", errors.New("update channel must be stable or beta")
	}
}
