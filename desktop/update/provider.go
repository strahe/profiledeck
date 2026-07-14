package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

const maxFeedBytes = 1024 * 1024

type ProviderConfig struct {
	FeedURL         string
	PublicKey       []byte
	HTTPClient      *http.Client
	AllowTestSource bool
}

type StrictProvider struct {
	feedURL         string
	publicKey       ed25519.PublicKey
	client          *http.Client
	allowTestSource bool
	errorMu         sync.RWMutex
	lastErrorCode   string
}

func NewStrictProvider(config ProviderConfig) (*StrictProvider, error) {
	feedURL := strings.TrimSpace(config.FeedURL)
	if feedURL == "" {
		return nil, errors.New("update feed URL is required")
	}
	// Production builds pin the signed channel location so configuration cannot redirect update checks.
	if !config.AllowTestSource && feedURL != DefaultFeedURL {
		return nil, errors.New("production update feed URL is not trusted")
	}
	publicKey, err := ParsePublicKey(config.PublicKey)
	if err != nil {
		return nil, err
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Minute,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL.Scheme != "https" {
					return errors.New("update redirect must use HTTPS")
				}
				return nil
			},
		}
	}
	return &StrictProvider{
		feedURL: feedURL, publicKey: publicKey, client: client, allowTestSource: config.AllowTestSource,
	}, nil
}

func (provider *StrictProvider) Name() string { return "profiledeck-github" }

func (provider *StrictProvider) Check(ctx context.Context, request updater.CheckRequest) (release *updater.Release, finalErr error) {
	provider.setLastErrorCode("")
	defer func() {
		if finalErr != nil {
			provider.setLastErrorCode(ErrorCode(finalErr))
		}
	}()
	if request.Platform != UpdatePlatform || request.Arch != UpdateArch {
		return nil, updateError(ErrorFeedInvalid, errors.New("unsupported updater target"))
	}
	if !validVersion(request.CurrentVersion) {
		return nil, updateError(ErrorFeedInvalid, errors.New("running version is not valid SemVer"))
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.feedURL, nil)
	if err != nil {
		return nil, updateError(ErrorFeedUnavailable, err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "ProfileDeck-Updater/1")
	response, err := provider.client.Do(httpRequest)
	if err != nil {
		return nil, updateError(ErrorFeedUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, updateError(ErrorFeedUnavailable, fmt.Errorf("feed returned HTTP %d", response.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil {
		return nil, updateError(ErrorFeedUnavailable, err)
	}
	if len(raw) > maxFeedBytes {
		return nil, updateError(ErrorFeedInvalid, errors.New("feed is too large"))
	}
	manifest, err := verifyFeed(raw, provider.publicKey)
	if err != nil {
		return nil, err
	}
	return validateManifest(manifest, request.CurrentVersion, provider.allowTestSource)
}

func (provider *StrictProvider) Download(
	ctx context.Context,
	release *updater.Release,
	destination io.Writer,
	onProgress func(written, total int64),
) (finalErr error) {
	provider.setLastErrorCode("")
	defer func() {
		if finalErr != nil {
			provider.setLastErrorCode(ErrorCode(finalErr))
		}
	}()
	if release == nil || release.Verification == nil {
		return updateError(ErrorArtifactSignatureMissing, errors.New("verified release metadata is required"))
	}
	verification := release.Verification
	if verification.DigestAlgo != DigestAlgorithm || len(verification.Digest) != 64 ||
		verification.SignatureAlgo != SignatureAlgorithm || len(verification.Signature) != ed25519.SignatureSize {
		return updateError(ErrorArtifactSignatureMissing, errors.New("artifact signature is required"))
	}
	artifactURL, ok := release.Metadata["url"].(string)
	if !ok || strings.TrimSpace(artifactURL) == "" {
		return updateError(ErrorFeedInvalid, errors.New("artifact URL is missing"))
	}
	if !provider.allowTestSource {
		expected := fmt.Sprintf(
			"https://github.com/strahe/profiledeck/releases/download/v%s/ProfileDeck_%s_darwin_arm64.zip",
			release.Version, release.Version,
		)
		if artifactURL != expected {
			return updateError(ErrorFeedInvalid, errors.New("artifact URL changed after feed verification"))
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return updateError(ErrorFeedUnavailable, err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "ProfileDeck-Updater/1")
	response, err := provider.client.Do(request)
	if err != nil {
		return updateError(ErrorFeedUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return updateError(ErrorFeedUnavailable, fmt.Errorf("artifact returned HTTP %d", response.StatusCode))
	}
	if response.ContentLength >= 0 && response.ContentLength != release.Artifact.Size {
		return updateError(ErrorArtifactVerificationFailed, errors.New("artifact size does not match the signed feed"))
	}

	written := int64(0)
	buffer := make([]byte, 128*1024)
	limited := io.LimitReader(response.Body, release.Artifact.Size+1)
	for {
		count, readErr := limited.Read(buffer)
		if count > 0 {
			writeCount, writeErr := destination.Write(buffer[:count])
			written += int64(writeCount)
			onProgress(written, release.Artifact.Size)
			if writeErr != nil {
				return updateError(ErrorFeedUnavailable, writeErr)
			}
			if writeCount != count {
				return updateError(ErrorFeedUnavailable, io.ErrShortWrite)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return updateError(ErrorFeedUnavailable, readErr)
		}
	}
	if written != release.Artifact.Size {
		return updateError(ErrorArtifactVerificationFailed, errors.New("artifact size does not match the signed feed"))
	}
	return nil
}

func (provider *StrictProvider) LastErrorCode() string {
	provider.errorMu.RLock()
	defer provider.errorMu.RUnlock()
	return provider.lastErrorCode
}

func (provider *StrictProvider) setLastErrorCode(code string) {
	provider.errorMu.Lock()
	provider.lastErrorCode = code
	provider.errorMu.Unlock()
}
