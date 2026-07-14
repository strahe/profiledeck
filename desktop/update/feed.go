// Package update owns the Wails-specific Desktop update runtime.
package update

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/mod/semver"
)

const (
	FeedSchemaVersion = 1
	UpdateChannel     = "alpha"
	UpdatePlatform    = "darwin"
	UpdateArch        = "arm64"
	DefaultFeedURL    = "https://raw.githubusercontent.com/strahe/profiledeck/main/updates/alpha.json"

	DigestAlgorithm    = "sha512"
	SignatureAlgorithm = "ed25519ph"

	ErrorFeedUnavailable            = "feed_unavailable"
	ErrorFeedInvalid                = "feed_invalid"
	ErrorFeedSignatureMissing       = "feed_signature_missing"
	ErrorFeedSignatureInvalid       = "feed_signature_invalid"
	ErrorFeedRollback               = "feed_rollback"
	ErrorArtifactSignatureMissing   = "artifact_signature_missing"
	ErrorArtifactVerificationFailed = "artifact_verification_failed"
)

type FeedEnvelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	Payload       []byte `json:"payload"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        []byte `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     []byte `json:"signature"`
}

type FeedManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Version       string           `json:"version"`
	Channel       string           `json:"channel"`
	ReleaseTag    string           `json:"releaseTag"`
	Artifact      ManifestArtifact `json:"artifact"`
}

type ManifestArtifact struct {
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	Size          int64  `json:"size"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        []byte `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     []byte `json:"signature"`
}

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

func ParsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	if len(raw) == ed25519.PublicKeySize {
		return append(ed25519.PublicKey(nil), raw...), nil
	}
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	}
	parsed, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse update public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("update public key is not Ed25519")
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func ParsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse update private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("update private key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func SignPrehashed(privateKey ed25519.PrivateKey, digest []byte) ([]byte, error) {
	if len(digest) != sha512.Size {
		return nil, fmt.Errorf("ed25519ph requires a %d-byte SHA-512 digest", sha512.Size)
	}
	return privateKey.Sign(rand.Reader, digest, &ed25519.Options{Hash: crypto.SHA512})
}

func NewSignedEnvelope(payload []byte, privateKey ed25519.PrivateKey) (FeedEnvelope, error) {
	digest := sha512.Sum512(payload)
	signature, err := SignPrehashed(privateKey, digest[:])
	if err != nil {
		return FeedEnvelope{}, err
	}
	return FeedEnvelope{
		SchemaVersion: FeedSchemaVersion,
		Payload:       append([]byte(nil), payload...),
		DigestAlgo:    DigestAlgorithm,
		Digest:        append([]byte(nil), digest[:]...),
		SignatureAlgo: SignatureAlgorithm,
		Signature:     signature,
	}, nil
}

func MarshalSignedFeed(manifest FeedManifest, privateKey ed25519.PrivateKey) ([]byte, error) {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	envelope, err := NewSignedEnvelope(payload, privateKey)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(envelope, "", "  ")
}

func verifyFeed(raw []byte, publicKey ed25519.PublicKey) (FeedManifest, error) {
	var envelope FeedEnvelope
	if err := decodeStrict(raw, &envelope); err != nil {
		return FeedManifest{}, updateError(ErrorFeedInvalid, err)
	}
	if envelope.SchemaVersion != FeedSchemaVersion || envelope.DigestAlgo != DigestAlgorithm {
		return FeedManifest{}, updateError(ErrorFeedInvalid, errors.New("unsupported feed envelope"))
	}
	if envelope.SignatureAlgo == "" || len(envelope.Signature) == 0 {
		return FeedManifest{}, updateError(ErrorFeedSignatureMissing, errors.New("feed signature is required"))
	}
	if envelope.SignatureAlgo != SignatureAlgorithm || len(envelope.Digest) != sha512.Size || len(envelope.Signature) != ed25519.SignatureSize {
		return FeedManifest{}, updateError(ErrorFeedSignatureInvalid, errors.New("unsupported feed signature"))
	}
	digest := sha512.Sum512(envelope.Payload)
	if !bytes.Equal(digest[:], envelope.Digest) {
		return FeedManifest{}, updateError(ErrorFeedSignatureInvalid, errors.New("feed digest mismatch"))
	}
	if err := ed25519.VerifyWithOptions(publicKey, digest[:], envelope.Signature, &ed25519.Options{Hash: crypto.SHA512}); err != nil {
		return FeedManifest{}, updateError(ErrorFeedSignatureInvalid, errors.New("feed signature did not verify"))
	}

	var manifest FeedManifest
	if err := decodeStrict(envelope.Payload, &manifest); err != nil {
		return FeedManifest{}, updateError(ErrorFeedInvalid, err)
	}
	return manifest, nil
}

func VerifySignedFeed(raw, publicKeyRaw []byte) (FeedManifest, error) {
	publicKey, err := ParsePublicKey(publicKeyRaw)
	if err != nil {
		return FeedManifest{}, updateError(ErrorFeedSignatureInvalid, err)
	}
	return verifyFeed(raw, publicKey)
}

func ValidateManifest(manifest FeedManifest, currentVersion string) (*updater.Release, error) {
	return validateManifest(manifest, currentVersion, false)
}

func ValidateTestManifest(manifest FeedManifest, currentVersion string) (*updater.Release, error) {
	return validateManifest(manifest, currentVersion, true)
}

func validateManifest(manifest FeedManifest, currentVersion string, allowTestSource bool) (*updater.Release, error) {
	if manifest.SchemaVersion != FeedSchemaVersion || manifest.Channel != UpdateChannel {
		return nil, updateError(ErrorFeedInvalid, errors.New("unsupported update manifest"))
	}
	if !validVersion(manifest.Version) || !validVersion(currentVersion) {
		return nil, updateError(ErrorFeedInvalid, errors.New("invalid update version"))
	}
	comparison := semver.Compare("v"+manifest.Version, "v"+currentVersion)
	if comparison < 0 {
		return nil, updateError(ErrorFeedRollback, errors.New("feed version is older than the running version"))
	}
	if comparison == 0 {
		return nil, nil
	}

	artifact := manifest.Artifact
	expectedTag := "v" + manifest.Version
	expectedFilename := fmt.Sprintf("ProfileDeck_%s_darwin_arm64.zip", manifest.Version)
	if manifest.ReleaseTag != expectedTag || artifact.Filename != expectedFilename {
		return nil, updateError(ErrorFeedInvalid, errors.New("release tag, version, and filename do not match"))
	}
	if artifact.Platform != UpdatePlatform || artifact.Arch != UpdateArch || artifact.Size <= 0 {
		return nil, updateError(ErrorFeedInvalid, errors.New("unsupported artifact target"))
	}
	if !allowTestSource {
		expectedURL := fmt.Sprintf("https://github.com/strahe/profiledeck/releases/download/%s/%s", expectedTag, expectedFilename)
		if artifact.URL != expectedURL {
			return nil, updateError(ErrorFeedInvalid, errors.New("artifact is not a ProfileDeck GitHub Release asset"))
		}
	} else if strings.TrimSpace(artifact.URL) == "" {
		return nil, updateError(ErrorFeedInvalid, errors.New("artifact URL is required"))
	}
	if artifact.DigestAlgo != DigestAlgorithm || len(artifact.Digest) != sha512.Size {
		return nil, updateError(ErrorFeedInvalid, errors.New("artifact SHA-512 digest is required"))
	}
	if artifact.SignatureAlgo == "" || len(artifact.Signature) == 0 {
		return nil, updateError(ErrorArtifactSignatureMissing, errors.New("artifact signature is required"))
	}
	if artifact.SignatureAlgo != SignatureAlgorithm || len(artifact.Signature) != ed25519.SignatureSize {
		return nil, updateError(ErrorArtifactVerificationFailed, errors.New("unsupported artifact signature"))
	}

	return &updater.Release{
		Version: manifest.Version,
		Channel: manifest.Channel,
		Artifact: updater.Artifact{
			Filename: artifact.Filename,
			Filetype: "zip",
			Size:     artifact.Size,
			Platform: artifact.Platform,
			Arch:     artifact.Arch,
		},
		Verification: &updater.Verification{
			DigestAlgo:    artifact.DigestAlgo,
			Digest:        append([]byte(nil), artifact.Digest...),
			SignatureAlgo: artifact.SignatureAlgo,
			Signature:     append([]byte(nil), artifact.Signature...),
		},
		Metadata: map[string]any{"url": artifact.URL},
	}, nil
}

func validVersion(version string) bool {
	return version != "" && !strings.HasPrefix(version, "v") && semver.IsValid("v"+version)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
