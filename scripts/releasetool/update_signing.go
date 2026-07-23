package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func loadUpdatePrivateKey(path string) (ed25519.PrivateKey, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("update signing private key path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect update signing private key: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("update signing private key must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("update signing private key must not be accessible to group or other users")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read update signing private key: %w", err)
	}
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, errors.New("update signing private key is not valid PKCS#8")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("update signing private key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func updatePublicKeyBase64(privateKeyPath string) (string, error) {
	privateKey, err := loadUpdatePrivateKey(privateKeyPath)
	if err != nil {
		return "", err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return "", errors.New("update signing private key did not produce an Ed25519 public key")
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func signUpdateArtifact(privateKeyPath, artifactPath, outputPath string) error {
	privateKey, err := loadUpdatePrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	digest, err := sha256File(artifactPath)
	if err != nil {
		return err
	}
	if filepath.Clean(outputPath) != filepath.Clean(artifactPath)+".sig" {
		return errors.New("update signature output must be the artifact path plus .sig")
	}
	signature := ed25519.Sign(privateKey, digest)
	content := base64.StdEncoding.AppendEncode(nil, signature)
	content = append(content, '\n')
	if err := os.WriteFile(outputPath, content, 0o644); err != nil {
		return fmt.Errorf("write update artifact signature: %w", err)
	}
	return nil
}

func verifyUpdateArtifactSignature(publicKeyBase64, artifactPath, signaturePath string) error {
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("update public key is invalid")
	}
	rawSignature, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("read update artifact signature: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(rawSignature)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("update artifact signature is invalid")
	}
	digest, err := sha256File(artifactPath)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), digest, signature) {
		return errors.New("update artifact signature did not verify")
	}
	return nil
}

func sha256File(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect update artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 {
		return nil, errors.New("update artifact must be a non-empty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open update artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("hash update artifact: %w", err)
	}
	return hash.Sum(nil), nil
}
