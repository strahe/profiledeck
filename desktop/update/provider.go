// Package update owns the Wails-specific Desktop update runtime.
package update

import (
	"context"
	"encoding/base64"
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
	SignatureSuffix  = ".sig"
	SignatureAlgo    = "ed25519"
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
// then requires immutable asset naming, a checksum, and a detached signature.
type verifiedGitHubProvider struct {
	delegate      *githubprovider.Provider
	client        *http.Client
	channel       string
	trustedSource bool
	checkMu       sync.Mutex
	assetMatcher  *releaseAssetMatcher
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
	assetMatcher := &releaseAssetMatcher{}
	delegate, err := githubprovider.New(githubprovider.Config{
		Repository:    repository,
		Prerelease:    channel == ChannelBeta,
		BaseURL:       options.BaseURL,
		AssetMatcher:  assetMatcher.Match,
		ChecksumAsset: ChecksumAsset,
		HTTPClient:    client,
	})
	if err != nil {
		return nil, err
	}
	return &verifiedGitHubProvider{
		delegate:      delegate,
		client:        client,
		channel:       channel,
		trustedSource: repository == UpdateRepository && strings.TrimSpace(options.BaseURL) == "",
		assetMatcher:  assetMatcher,
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
	provider.checkMu.Lock()
	defer provider.checkMu.Unlock()
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
	assetURL, _ := release.Metadata["github.asset.url"].(string)
	signatureURL, ok := provider.assetMatcher.SignatureURL(assetURL)
	if !ok {
		return nil, updateError(
			ErrorArtifactVerificationFailed,
			errors.New("release asset is missing its unique detached signature"),
		)
	}
	if provider.trustedSource {
		if !trustedGitHubAssetURL(assetURL, release.Version, release.Artifact.Filename) ||
			!trustedGitHubAssetURL(signatureURL, release.Version, release.Artifact.Filename+SignatureSuffix) {
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
	signature, err := provider.fetchSignature(ctx, signatureURL)
	if err != nil {
		return nil, updateError(ErrorArtifactVerificationFailed, err)
	}
	release.Verification.SignatureAlgo = SignatureAlgo
	release.Verification.Signature = signature
	return release, nil
}

func (provider *verifiedGitHubProvider) fetchSignature(ctx context.Context, signatureURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signatureURL, nil)
	if err != nil {
		return nil, errors.New("release asset signature URL is invalid")
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "ProfileDeck-Updater/1")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, errors.New("release asset signature is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("release asset signature is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	if err != nil || len(raw) > 1024 {
		return nil, errors.New("release asset signature is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(signature) != 64 {
		return nil, errors.New("release asset signature is invalid")
	}
	return signature, nil
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

// releaseAssetMatcher binds the selected archive to the one same-release
// detached-signature asset. A reachable sibling URL is not sufficient evidence
// that the signature was published as part of the release.
type releaseAssetMatcher struct {
	artifactURL   string
	signatureURL  string
	signatureSeen int
}

func (matcher *releaseAssetMatcher) Match(
	request updater.CheckRequest,
	assets []githubprovider.ReleaseAsset,
) int {
	matcher.artifactURL = ""
	matcher.signatureURL = ""
	matcher.signatureSeen = 0

	index := universalAssetMatcher(request, assets)
	if index < 0 {
		return index
	}
	matcher.artifactURL = assets[index].URL
	signatureName := assets[index].Name + SignatureSuffix
	for _, asset := range assets {
		if asset.Name != signatureName {
			continue
		}
		matcher.signatureSeen++
		matcher.signatureURL = asset.URL
	}
	return index
}

func (matcher *releaseAssetMatcher) SignatureURL(artifactURL string) (string, bool) {
	if matcher.artifactURL != artifactURL ||
		matcher.signatureSeen != 1 ||
		strings.TrimSpace(matcher.signatureURL) == "" {
		return "", false
	}
	return matcher.signatureURL, true
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
