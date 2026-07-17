package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	oldVersion = "0.1.0-beta.1"
	newVersion = "0.1.0-beta.2"
	artifact   = "ProfileDeck_0.1.0-beta.2_macos_universal.zip"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "update-e2e: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return errors.New("update restart integration test requires macOS arm64")
	}
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	workDirectory, err := os.MkdirTemp("", "profiledeck-update-e2e-*")
	if err != nil {
		return fmt.Errorf("create update test workspace: %w", err)
	}
	defer func() {
		if os.Getenv("PROFILEDECK_UPDATE_E2E_KEEP") == "1" {
			fmt.Fprintf(os.Stderr, "preserved update E2E workspace: %s\n", workDirectory)
			return
		}
		_ = os.RemoveAll(workDirectory)
	}()
	serveDirectory := filepath.Join(workDirectory, "serve")
	installedDirectory := filepath.Join(workDirectory, "installed")
	configDirectory := filepath.Join(workDirectory, "config")
	markerPath := filepath.Join(workDirectory, "result.txt")
	for _, directory := range []string{serveDirectory, installedDirectory, configDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create update test directory: %w", err)
		}
	}

	newApp := filepath.Join(workDirectory, "new", "ProfileDeck.app")
	if err := buildBundle(root, newApp, newVersion, "", configDirectory, markerPath); err != nil {
		return err
	}
	artifactPath := filepath.Join(serveDirectory, artifact)
	if err := runCommand(
		root,
		"ditto",
		"-c",
		"-k",
		"--norsrc",
		"--noextattr",
		"--noqtn",
		"--noacl",
		"--keepParent",
		newApp,
		artifactPath,
	); err != nil {
		return err
	}
	if err := writeChecksum(serveDirectory, artifactPath); err != nil {
		return err
	}
	server, baseURL, err := startReleaseServer(serveDirectory)
	if err != nil {
		return err
	}
	defer func() {
		_ = server.Shutdown(context.Background())
	}()

	installedApp := filepath.Join(installedDirectory, "ProfileDeck.app")
	if err := buildBundle(
		root,
		installedApp,
		oldVersion,
		baseURL,
		configDirectory,
		markerPath,
	); err != nil {
		return err
	}
	if err := runCommand(
		root,
		filepath.Join(installedApp, "Contents", "MacOS", "profiledeck-desktop"),
	); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(markerPath)
		if err == nil && len(content) > 0 {
			result := strings.TrimSpace(string(content))
			if result != "ok: "+newVersion {
				return errors.New(result)
			}
			if _, err := os.Stat(filepath.Join(configDirectory, "profiledeck", "backups")); err != nil {
				return fmt.Errorf("update backup directory is missing: %w", err)
			}
			fmt.Printf("Verified real %s to %s restart replacement\n", oldVersion, newVersion)
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read update result: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("updated application did not relaunch; workspace: %s", workDirectory)
}

func repositoryRoot() (string, error) {
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("locate repository root: %s: %w", output, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func buildBundle(
	root string,
	target string,
	version string,
	baseURL string,
	configDirectory string,
	markerPath string,
) error {
	executable := filepath.Join(target, "Contents", "MacOS", "profiledeck-desktop")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		return fmt.Errorf("create application bundle: %w", err)
	}
	ldflags := strings.Join([]string{
		"-X main.version=" + version,
		"-X main.githubBaseURL=" + baseURL,
		"-X main.configDir=" + configDirectory,
		"-X main.marker=" + markerPath,
	}, " ")
	if err := runCommand(
		root,
		"go",
		"build",
		"-tags",
		"updatee2e",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags",
		ldflags,
		"-o",
		executable,
		"./scripts/updatee2e/client",
	); err != nil {
		return err
	}
	if err := writeInfoPlist(
		filepath.Join(root, "build", "darwin", "Info.plist.tmpl"),
		filepath.Join(target, "Contents", "Info.plist"),
	); err != nil {
		return err
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		return fmt.Errorf("make update test executable runnable: %w", err)
	}
	if err := runCommand(root, "codesign", "--force", "--sign", "-", "--timestamp=none", executable); err != nil {
		return err
	}
	return runCommand(root, "codesign", "--force", "--sign", "-", "--timestamp=none", target)
}

func writeInfoPlist(templatePath, outputPath string) error {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read Info.plist template: %w", err)
	}
	rendered := strings.ReplaceAll(string(content), "@SHORT_VERSION@", "0.1.0")
	rendered = strings.ReplaceAll(rendered, "@BUILD_NUMBER@", "1")
	if err := os.WriteFile(outputPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write update test Info.plist: %w", err)
	}
	return nil
}

func writeChecksum(directory, artifactPath string) error {
	content, err := os.ReadFile(artifactPath)
	if err != nil {
		return fmt.Errorf("read update artifact: %w", err)
	}
	digest := sha256.Sum256(content)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), filepath.Base(artifactPath))
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(line), 0o600); err != nil {
		return fmt.Errorf("write update checksum: %w", err)
	}
	return nil
}

func runCommand(directory, name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func startReleaseServer(root string) (*http.Server, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("start update server: %w", err)
	}
	baseURL := "http://" + listener.Addr().String()
	handler := http.NewServeMux()
	handler.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir(root))))
	handler.HandleFunc("/repos/test/profiledeck/releases/latest", func(response http.ResponseWriter, _ *http.Request) {
		writeRelease(response, releasePayload(root, baseURL))
	})
	handler.HandleFunc("/repos/test/profiledeck/releases", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode([]any{releasePayload(root, baseURL)})
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "update-e2e: update server failed: %v\n", err)
		}
	}()
	return server, baseURL, nil
}

func releasePayload(root, baseURL string) map[string]any {
	artifactInfo, err := os.Stat(filepath.Join(root, artifact))
	if err != nil {
		panic(err)
	}
	checksumInfo, err := os.Stat(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		panic(err)
	}
	return map[string]any{
		"tag_name":     "v" + newVersion,
		"name":         "ProfileDeck " + newVersion,
		"body":         "Update restart integration test",
		"prerelease":   true,
		"draft":        false,
		"published_at": time.Now().UTC().Format(time.RFC3339),
		"html_url":     baseURL + "/release",
		"assets": []map[string]any{
			releaseAsset(baseURL, artifact, "application/zip", artifactInfo.Size(), 1),
			releaseAsset(baseURL, "SHA256SUMS", "text/plain", checksumInfo.Size(), 2),
		},
	}
}

func releaseAsset(
	baseURL string,
	name string,
	contentType string,
	size int64,
	id int64,
) map[string]any {
	return map[string]any{
		"id":                   id,
		"name":                 name,
		"content_type":         contentType,
		"size":                 size,
		"browser_download_url": strings.TrimRight(baseURL, "/") + "/downloads/" + name,
	}
}

func writeRelease(response http.ResponseWriter, payload map[string]any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(payload); err != nil {
		http.Error(response, "unable to encode release", http.StatusInternalServerError)
	}
}
