package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateSigningRoundTripAndTamperRejection(t *testing.T) {
	t.Parallel()
	privateKeyPath, publicKey := writeUpdateSigningKey(t)
	artifactPath := filepath.Join(t.TempDir(), "ProfileDeck_1.2.3_macos_universal.zip")
	if err := os.WriteFile(artifactPath, []byte("signed update payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	signaturePath := artifactPath + ".sig"

	if err := signUpdateArtifact(privateKeyPath, artifactPath, signaturePath); err != nil {
		t.Fatalf("sign update: %v", err)
	}
	derivedPublicKey, err := updatePublicKeyBase64(privateKeyPath)
	if err != nil {
		t.Fatalf("derive public key: %v", err)
	}
	if derivedPublicKey != publicKey {
		t.Fatalf("derived public key = %q, want %q", derivedPublicKey, publicKey)
	}
	if err := verifyUpdateArtifactSignature(publicKey, artifactPath, signaturePath); err != nil {
		t.Fatalf("verify update: %v", err)
	}

	if err := os.WriteFile(artifactPath, []byte("tampered update payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateArtifactSignature(publicKey, artifactPath, signaturePath); err == nil || !strings.Contains(err.Error(), "did not verify") {
		t.Fatalf("verify tampered update error = %v, want signature rejection", err)
	}
}

func TestSignUpdateRejectsUnexpectedSidecarPath(t *testing.T) {
	t.Parallel()
	privateKeyPath, _ := writeUpdateSigningKey(t)
	artifactPath := filepath.Join(t.TempDir(), "update.zip")
	if err := os.WriteFile(artifactPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := signUpdateArtifact(privateKeyPath, artifactPath, filepath.Join(t.TempDir(), "other.sig"))
	if err == nil || !strings.Contains(err.Error(), "artifact path plus .sig") {
		t.Fatalf("sign update error = %v, want sidecar path rejection", err)
	}
}

func TestLoadUpdatePrivateKeyRejectsBroadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix private-key permission bits")
	}
	privateKeyPath, _ := writeUpdateSigningKey(t)
	if err := os.Chmod(privateKeyPath, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := loadUpdatePrivateKey(privateKeyPath); err == nil || !strings.Contains(err.Error(), "group or other") {
		t.Fatalf("load update key error = %v, want private permission rejection", err)
	}
}

func writeUpdateSigningKey(t *testing.T) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "update-signing-key.p8")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, base64.StdEncoding.EncodeToString(publicKey)
}
