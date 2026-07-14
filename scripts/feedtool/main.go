package main

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	desktopupdate "github.com/strahe/profiledeck/desktop/update"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: feedtool <public-key|create|verify> [flags]")
	}
	var err error
	switch os.Args[1] {
	case "public-key":
		err = publicKey(os.Args[2:])
	case "create":
		err = createFeed(os.Args[2:])
	case "verify":
		err = verifyFeed(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
}

func publicKey(arguments []string) error {
	flags := flag.NewFlagSet("public-key", flag.ContinueOnError)
	privateKeyPath := flags.String("private-key", "", "PKCS#8 Ed25519 private key")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	privateKey, err := loadPrivateKey(*privateKeyPath)
	if err != nil {
		return err
	}
	key, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return errors.New("private key did not produce an Ed25519 public key")
	}
	fmt.Println(base64.StdEncoding.EncodeToString(key))
	return nil
}

func createFeed(arguments []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	privateKeyPath := flags.String("private-key", "", "PKCS#8 Ed25519 private key")
	version := flags.String("version", "", "release version without v prefix")
	artifactPath := flags.String("artifact", "", "release artifact path")
	artifactURL := flags.String("artifact-url", "", "public GitHub Release asset URL")
	output := flags.String("output", "feed.json", "output feed path")
	allowTestSource := flags.Bool("allow-test-source", false, "allow a non-GitHub URL for local integration tests")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	privateKey, err := loadPrivateKey(*privateKeyPath)
	if err != nil {
		return err
	}
	digest, size, err := hashFile(*artifactPath)
	if err != nil {
		return err
	}
	signature, err := desktopupdate.SignPrehashed(privateKey, digest)
	if err != nil {
		return err
	}
	manifest := desktopupdate.FeedManifest{
		SchemaVersion: desktopupdate.FeedSchemaVersion,
		Version:       strings.TrimSpace(*version),
		Channel:       desktopupdate.UpdateChannel,
		ReleaseTag:    "v" + strings.TrimSpace(*version),
		Artifact: desktopupdate.ManifestArtifact{
			URL:           strings.TrimSpace(*artifactURL),
			Filename:      filepath.Base(*artifactPath),
			Platform:      desktopupdate.UpdatePlatform,
			Arch:          desktopupdate.UpdateArch,
			Size:          size,
			DigestAlgo:    desktopupdate.DigestAlgorithm,
			Digest:        digest,
			SignatureAlgo: desktopupdate.SignatureAlgorithm,
			Signature:     signature,
		},
	}
	validate := desktopupdate.ValidateManifest
	if *allowTestSource {
		validate = desktopupdate.ValidateTestManifest
	}
	if _, err := validate(manifest, "0.0.0-alpha.0"); err != nil {
		return fmt.Errorf("validate generated manifest: %w", err)
	}
	raw, err := desktopupdate.MarshalSignedFeed(manifest, privateKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*output, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("created signed feed for %s (%d bytes)\n", manifest.Version, size)
	return nil
}

func verifyFeed(arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	publicKeyValue := flags.String("public-key", "", "public key file or raw base64 key")
	feedPath := flags.String("feed", "feed.json", "signed feed path")
	artifactPath := flags.String("artifact", "", "downloaded artifact path")
	currentVersion := flags.String("current-version", "0.0.0-alpha.0", "version used for rollback validation")
	allowTestSource := flags.Bool("allow-test-source", false, "allow a non-GitHub URL for local integration tests")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	publicKeyRaw, err := loadPublicKey(*publicKeyValue)
	if err != nil {
		return err
	}
	feed, err := os.ReadFile(*feedPath)
	if err != nil {
		return err
	}
	manifest, err := desktopupdate.VerifySignedFeed(feed, publicKeyRaw)
	if err != nil {
		return err
	}
	validate := desktopupdate.ValidateManifest
	if *allowTestSource {
		validate = desktopupdate.ValidateTestManifest
	}
	if _, err := validate(manifest, *currentVersion); err != nil {
		return err
	}
	digest, size, err := hashFile(*artifactPath)
	if err != nil {
		return err
	}
	if size != manifest.Artifact.Size || !equalBytes(digest, manifest.Artifact.Digest) {
		return errors.New("artifact digest or size does not match signed feed")
	}
	publicKey, err := desktopupdate.ParsePublicKey(publicKeyRaw)
	if err != nil {
		return err
	}
	if err := ed25519.VerifyWithOptions(
		publicKey,
		digest,
		manifest.Artifact.Signature,
		&ed25519.Options{Hash: crypto.SHA512},
	); err != nil {
		return errors.New("artifact signature did not verify")
	}
	fmt.Printf("verified signed feed and artifact for %s\n", manifest.Version)
	return nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("private key path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return desktopupdate.ParsePrivateKey(raw)
}

func loadPublicKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("public key is required")
	}
	if raw, err := os.ReadFile(value); err == nil {
		return raw, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func hashFile(path string) ([]byte, int64, error) {
	if strings.TrimSpace(path) == "" {
		return nil, 0, errors.New("artifact path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	hash := sha512.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return nil, 0, err
	}
	return hash.Sum(nil), size, nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "feedtool:", message)
	os.Exit(1)
}
