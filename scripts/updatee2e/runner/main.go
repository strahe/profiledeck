package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

const (
	oldVersion = "0.1.0-beta.1"
	newVersion = "0.1.0-beta.2"
)

type e2ePlatform struct {
	artifactName string
	targetName   string
	payloadKind  string
	archive      func(root, target, output string) error
	finalize     func(root, target, executable string) error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "update-e2e: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	contract, err := releaseartifact.NewContract(newVersion)
	if err != nil {
		return fmt.Errorf("create update release contract: %w", err)
	}
	platform, err := hostPlatform(contract)
	if err != nil {
		return err
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
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate update signing key: %w", err)
	}
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKey)
	for _, directory := range []string{serveDirectory, installedDirectory, configDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create update test directory: %w", err)
		}
	}

	newApp := platform.targetPath(filepath.Join(workDirectory, "new"))
	if err := buildBundle(root, platform, newApp, newVersion, "", configDirectory, markerPath, publicKeyBase64); err != nil {
		return err
	}
	artifactPath := filepath.Join(serveDirectory, platform.artifactName)
	if err := platform.archive(root, newApp, artifactPath); err != nil {
		return err
	}
	privateKeyPath := filepath.Join(workDirectory, "updater-private.pem")
	publicKeyPath := filepath.Join(workDirectory, "updater-public.pem")
	if err := writeUpdaterKeys(privateKeyPath, publicKeyPath, privateKey, publicKey); err != nil {
		return err
	}
	if err := writeAndVerifyManifest(
		root,
		serveDirectory,
		artifactPath,
		privateKeyPath,
		publicKeyPath,
		contract,
	); err != nil {
		return err
	}
	server, baseURL, err := startReleaseServer(serveDirectory, contract)
	if err != nil {
		return err
	}
	defer func() {
		_ = server.Shutdown(context.Background())
	}()

	installedApp := platform.targetPath(installedDirectory)
	if err := buildBundle(
		root,
		platform,
		installedApp,
		oldVersion,
		baseURL,
		configDirectory,
		markerPath,
		publicKeyBase64,
	); err != nil {
		return err
	}
	installedExecutable, err := platform.executablePath(installedApp)
	if err != nil {
		return err
	}
	if err := runCommand(root, installedExecutable); err != nil {
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

func hostPlatform(contract releaseartifact.Contract) (e2ePlatform, error) {
	target, err := releaseartifact.ResolveUpdateTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return e2ePlatform{}, err
	}
	artifactName, err := contract.AssetName(target.Role)
	if err != nil {
		return e2ePlatform{}, err
	}
	switch target.Role {
	case releaseartifact.RoleMacOSUpdater:
		return e2ePlatform{
			artifactName: artifactName,
			targetName:   target.PayloadName,
			payloadKind:  target.PayloadKind,
			archive:      archiveMacOSUpdate,
			finalize:     finalizeMacOSBundle,
		}, nil
	case releaseartifact.RoleLinuxUpdater:
		return e2ePlatform{
			artifactName: artifactName,
			targetName:   target.PayloadName,
			payloadKind:  target.PayloadKind,
			archive:      archiveLinuxUpdate,
			finalize:     finalizeLinuxExecutable,
		}, nil
	default:
		return e2ePlatform{}, fmt.Errorf("update restart integration test does not support role %q", target.Role)
	}
}

func (platform e2ePlatform) targetPath(parent string) string {
	return filepath.Join(parent, platform.targetName)
}

func (platform e2ePlatform) executablePath(target string) (string, error) {
	switch platform.payloadKind {
	case releaseartifact.PayloadExecutable:
		return target, nil
	case releaseartifact.PayloadAppBundle:
		return filepath.Join(
			target,
			"Contents",
			"MacOS",
			releaseartifact.DesktopExecutableName,
		), nil
	default:
		return "", fmt.Errorf("unsupported update payload kind %q", platform.payloadKind)
	}
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
	platform e2ePlatform,
	target string,
	version string,
	baseURL string,
	configDirectory string,
	markerPath string,
	publicKeyBase64 string,
) error {
	executable, err := platform.executablePath(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		return fmt.Errorf("create application bundle: %w", err)
	}
	ldflags := strings.Join([]string{
		"-X main.version=" + version,
		"-X main.githubBaseURL=" + baseURL,
		"-X main.configDir=" + configDirectory,
		"-X main.marker=" + markerPath,
		"-X main.updatePublicKeyBase64=" + publicKeyBase64,
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
	if err := os.Chmod(executable, 0o755); err != nil {
		return fmt.Errorf("make update test executable runnable: %w", err)
	}
	return platform.finalize(root, target, executable)
}

func finalizeMacOSBundle(root, target, executable string) error {
	if err := writeInfoPlist(
		filepath.Join(root, "build", "darwin", "Info.plist.tmpl"),
		filepath.Join(target, "Contents", "Info.plist"),
	); err != nil {
		return err
	}
	if err := runCommand(root, "codesign", "--force", "--sign", "-", "--timestamp=none", executable); err != nil {
		return err
	}
	return runCommand(root, "codesign", "--force", "--sign", "-", "--timestamp=none", target)
}

func finalizeLinuxExecutable(_, _, _ string) error {
	return nil
}

func archiveMacOSUpdate(root, target, output string) error {
	return runCommand(
		root,
		"ditto",
		"-c",
		"-k",
		"--norsrc",
		"--noextattr",
		"--noqtn",
		"--noacl",
		"--keepParent",
		target,
		output,
	)
}

func archiveLinuxUpdate(_, target, output string) error {
	source, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("open Linux update executable: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("inspect Linux update executable: %w", err)
	}
	file, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create Linux update archive: %w", err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	header := &tar.Header{
		Name: filepath.Base(target), Mode: 0o755, Size: info.Size(), Typeflag: tar.TypeReg,
	}
	if err := archive.WriteHeader(header); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Linux update header: %w", err)
	}
	if _, err := io.Copy(archive, source); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Linux update executable: %w", err)
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("close Linux update archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("close Linux update compression: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Linux update file: %w", err)
	}
	return nil
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

func writeUpdaterKeys(
	privatePath string,
	publicPath string,
	privateKey ed25519.PrivateKey,
	publicKey ed25519.PublicKey,
) error {
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("marshal updater private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("marshal updater public key: %w", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: privateDER,
	}), 0o600); err != nil {
		return fmt.Errorf("write updater private key: %w", err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type: "PUBLIC KEY", Bytes: publicDER,
	}), 0o600); err != nil {
		return fmt.Errorf("write updater public key: %w", err)
	}
	return nil
}

func writeAndVerifyManifest(
	root string,
	serveDirectory string,
	artifactPath string,
	privateKeyPath string,
	publicKeyPath string,
	contract releaseartifact.Contract,
) error {
	wails := strings.TrimSpace(os.Getenv("PROFILEDECK_WAILS3"))
	if wails == "" {
		wails = "wails3"
	}
	manifestPath := filepath.Join(serveDirectory, releaseartifact.ManifestName)
	if err := runCommand(
		root,
		wails,
		"updater", "manifest",
		"-version", contract.Version,
		"-channel", contract.Channel,
		"-key", privateKeyPath,
		"-output", manifestPath,
		artifactPath,
	); err != nil {
		return err
	}
	return runCommand(
		root,
		wails,
		"updater", "verify",
		"-manifest", manifestPath,
		"-publickey", publicKeyPath,
		"-dir", serveDirectory,
	)
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

func startReleaseServer(root string, contract releaseartifact.Contract) (*http.Server, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("start update server: %w", err)
	}
	baseURL := "http://" + listener.Addr().String()
	handler := http.NewServeMux()
	handler.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir(root))))
	handler.HandleFunc("/repos/test/profiledeck/releases/latest", func(response http.ResponseWriter, _ *http.Request) {
		writeRelease(response, releasePayload(root, baseURL, contract))
	})
	handler.HandleFunc("/repos/test/profiledeck/releases", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode([]any{releasePayload(root, baseURL, contract)})
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

func releasePayload(root, baseURL string, contract releaseartifact.Contract) map[string]any {
	manifestInfo, err := os.Stat(filepath.Join(root, releaseartifact.ManifestName))
	if err != nil {
		panic(err)
	}
	return map[string]any{
		"tag_name":     contract.Tag,
		"name":         contract.Product + " " + contract.Version,
		"body":         "Update restart integration test",
		"prerelease":   contract.Channel == releaseartifact.ChannelBeta,
		"draft":        false,
		"published_at": time.Now().UTC().Format(time.RFC3339),
		"html_url":     baseURL + "/release",
		"assets": []map[string]any{
			releaseAsset(baseURL, releaseartifact.ManifestName, "application/json", manifestInfo.Size(), 1),
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
