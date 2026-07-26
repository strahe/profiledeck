// Package update owns the Wails-specific Desktop update runtime.
package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	endpointprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	githubprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

const (
	UpdateRepository = "strahe/profiledeck"

	ErrorFeedUnavailable            = "feed_unavailable"
	ErrorFeedInvalid                = "feed_invalid"
	ErrorArtifactVerificationFailed = "artifact_verification_failed"

	manifestURLMetadata = "profiledeck.manifest.url"
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

type githubProviderOptions struct {
	Repository string
	BaseURL    string
	HTTPClient *http.Client
}

// channelGitHubProvider switches channels without reinitializing Wails'
// process-scoped updater.
type channelGitHubProvider struct {
	mu      sync.RWMutex
	channel string
	stable  *manifestGitHubProvider
	beta    *manifestGitHubProvider
}

// manifestGitHubProvider uses Wails' GitHub provider only to discover a
// release manifest, then delegates manifest parsing and artifact downloads to
// Wails' endpoint provider.
type manifestGitHubProvider struct {
	locator       *githubprovider.Provider
	client        *http.Client
	channel       string
	trustedSource bool
}

func newGitHubProvider(version string, options githubProviderOptions) (*channelGitHubProvider, error) {
	parsed, err := releaseartifact.ParseVersion(strings.TrimSpace(version))
	if err != nil {
		return nil, err
	}
	return newChannelGitHubProvider(parsed.Channel(), options)
}

func newChannelGitHubProvider(channel string, options githubProviderOptions) (*channelGitHubProvider, error) {
	channel, err := normalizeChannel(channel)
	if err != nil {
		return nil, err
	}
	stable, err := newManifestGitHubProvider(releaseartifact.ChannelStable, options)
	if err != nil {
		return nil, err
	}
	beta, err := newManifestGitHubProvider(releaseartifact.ChannelBeta, options)
	if err != nil {
		return nil, err
	}
	return &channelGitHubProvider{channel: channel, stable: stable, beta: beta}, nil
}

func newManifestGitHubProvider(channel string, options githubProviderOptions) (*manifestGitHubProvider, error) {
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
	locator, err := githubprovider.New(githubprovider.Config{
		Repository:   repository,
		Prerelease:   channel == releaseartifact.ChannelBeta,
		BaseURL:      options.BaseURL,
		AssetMatcher: manifestAssetMatcher,
		HTTPClient:   client,
	})
	if err != nil {
		return nil, err
	}
	return &manifestGitHubProvider{
		locator:       locator,
		client:        client,
		channel:       channel,
		trustedSource: repository == UpdateRepository && strings.TrimSpace(options.BaseURL) == "",
	}, nil
}

func (provider *channelGitHubProvider) Name() string { return "github-manifest" }

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
	if provider.channel == releaseartifact.ChannelBeta {
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
	// Both channel delegates share the same endpoint download contract.
	return provider.stable.Download(ctx, release, destination, onProgress)
}

func (provider *manifestGitHubProvider) Check(
	ctx context.Context,
	request updater.CheckRequest,
) (*updater.Release, error) {
	target, err := releaseartifact.ResolveUpdateTarget(request.Platform, request.Arch)
	if err != nil {
		return nil, updateError(ErrorFeedInvalid, errors.New("unsupported updater platform"))
	}
	if _, err := releaseartifact.ParseVersion(strings.TrimSpace(request.CurrentVersion)); err != nil {
		return nil, updateError(ErrorFeedInvalid, errors.New("running version is not releasable"))
	}

	discovered, err := provider.locator.Check(ctx, request)
	if err != nil {
		return nil, githubCheckError(err)
	}
	if discovered == nil {
		return nil, nil
	}
	version, err := releaseartifact.ParseVersion(discovered.Version)
	if err != nil {
		return nil, updateError(ErrorFeedInvalid, errors.New("release version is not supported"))
	}
	discoveredChannel, err := productChannelFromGitHub(discovered.Channel)
	if err != nil {
		return nil, updateError(ErrorFeedInvalid, err)
	}
	if discoveredChannel != version.Channel() {
		return nil, updateError(
			ErrorFeedInvalid,
			errors.New("GitHub release channel does not match its version"),
		)
	}
	if provider.channel == releaseartifact.ChannelStable && discoveredChannel != releaseartifact.ChannelStable {
		return nil, updateError(ErrorFeedInvalid, errors.New("stable channel received a beta release"))
	}

	manifestURL, _ := discovered.Metadata["github.asset.url"].(string)
	if provider.trustedSource &&
		!trustedGitHubAssetURL(manifestURL, version.String(), releaseartifact.ManifestName) {
		return nil, updateError(ErrorFeedInvalid, errors.New("release manifest URL is not trusted"))
	}
	endpoint, err := endpointprovider.New(endpointprovider.Config{
		URL:        manifestURL,
		Channel:    version.Channel(),
		HTTPClient: provider.client,
	})
	if err != nil {
		return nil, updateError(ErrorFeedInvalid, err)
	}
	release, err := endpoint.Check(ctx, request)
	if err != nil {
		return nil, endpointCheckError(err)
	}
	if release == nil {
		return nil, updateError(ErrorFeedInvalid, errors.New("release manifest does not describe the discovered release"))
	}
	if release.Version != version.String() {
		return nil, updateError(ErrorFeedInvalid, errors.New("release manifest version does not match its GitHub release"))
	}
	if release.Channel != version.Channel() {
		return nil, updateError(ErrorFeedInvalid, errors.New("release manifest channel does not match its version"))
	}
	expectedName, err := releaseartifact.UpdaterName(release.Version, target.Platform, target.Arch)
	if err != nil {
		return nil, updateError(ErrorFeedInvalid, err)
	}
	if release.Artifact.Filename != expectedName ||
		release.Artifact.Filetype != target.Filetype ||
		release.Artifact.Platform != target.Platform ||
		release.Artifact.Arch != target.Arch {
		return nil, updateError(ErrorFeedInvalid, errors.New("release artifact does not match this ProfileDeck build"))
	}
	if release.Verification == nil ||
		release.Verification.DigestAlgo != "sha512" ||
		len(release.Verification.Digest) != 64 ||
		release.Verification.SignatureAlgo != "ed25519ph" ||
		len(release.Verification.Signature) != 64 {
		return nil, updateError(
			ErrorArtifactVerificationFailed,
			errors.New("release artifact is missing Wails digest or signature verification"),
		)
	}
	artifactURL, _ := release.Metadata["endpoint.artifact.url"].(string)
	if provider.trustedSource &&
		!trustedGitHubAssetURL(artifactURL, version.String(), expectedName) {
		return nil, updateError(ErrorFeedInvalid, errors.New("release artifact URL is not trusted"))
	}

	release.Name = discovered.Name
	release.Notes = discovered.Notes
	release.PublishedAt = discovered.PublishedAt
	release.Metadata[manifestURLMetadata] = manifestURL
	return release, nil
}

func (provider *manifestGitHubProvider) Download(
	ctx context.Context,
	release *updater.Release,
	destination io.Writer,
	onProgress func(written, total int64),
) error {
	if release == nil || release.Metadata == nil {
		return updateError(ErrorFeedInvalid, errors.New("release is missing manifest metadata"))
	}
	manifestURL, _ := release.Metadata[manifestURLMetadata].(string)
	if strings.TrimSpace(manifestURL) == "" {
		return updateError(ErrorFeedInvalid, errors.New("release is missing manifest URL"))
	}
	endpoint, err := endpointprovider.New(endpointprovider.Config{
		URL:        manifestURL,
		HTTPClient: provider.client,
	})
	if err != nil {
		return updateError(ErrorFeedInvalid, err)
	}
	if err := endpoint.Download(ctx, release, destination, onProgress); err != nil {
		return updateError(ErrorFeedUnavailable, err)
	}
	return nil
}

func manifestAssetMatcher(_ updater.CheckRequest, assets []githubprovider.ReleaseAsset) int {
	match := -1
	for index, asset := range assets {
		if asset.Name != releaseartifact.ManifestName {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	return match
}

func githubCheckError(err error) error {
	// The pinned Wails providers do not export typed errors for these cases.
	message := err.Error()
	switch {
	case strings.Contains(message, " has no asset for "),
		strings.Contains(message, "github: decode release"),
		strings.Contains(message, "github: decode releases list"):
		return updateError(ErrorFeedInvalid, err)
	default:
		return updateError(ErrorFeedUnavailable, err)
	}
}

func endpointCheckError(err error) error {
	// Keep the pinned Wails message contract isolated in this adapter.
	message := err.Error()
	switch {
	case strings.Contains(message, "fetch manifest"),
		strings.Contains(message, "manifest request failed"):
		return updateError(ErrorFeedUnavailable, err)
	case strings.Contains(message, "signature"),
		strings.Contains(message, "digest"):
		return updateError(ErrorArtifactVerificationFailed, err)
	default:
		return updateError(ErrorFeedInvalid, err)
	}
}

func trustedGitHubAssetURL(assetURL, version, filename string) bool {
	parsedURL, err := url.Parse(assetURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "github.com" ||
		parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return false
	}
	expectedPath := fmt.Sprintf(
		"/%s/releases/download/v%s/%s",
		UpdateRepository,
		version,
		filename,
	)
	return parsedURL.EscapedPath() == expectedPath
}

func normalizeChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case releaseartifact.ChannelStable:
		return releaseartifact.ChannelStable, nil
	case releaseartifact.ChannelBeta:
		return releaseartifact.ChannelBeta, nil
	default:
		return "", errors.New("update channel must be stable or beta")
	}
}

func productChannelFromGitHub(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case releaseartifact.ChannelStable:
		return releaseartifact.ChannelStable, nil
	case "prerelease":
		return releaseartifact.ChannelBeta, nil
	default:
		return "", errors.New("release channel is not supported")
	}
}
