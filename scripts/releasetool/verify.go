package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func plistValue(
	ctx context.Context,
	runner commandRunner,
	path string,
	key string,
) (string, error) {
	output, err := runner.run(ctx, "plutil", "-extract", key, "raw", "-o", "-", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func verifyApp(
	ctx context.Context,
	runner commandRunner,
	appPath string,
	version releaseVersion,
	buildNumber int,
) error {
	executablePath := filepath.Join(appPath, "Contents", "MacOS", "profiledeck-desktop")
	info, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("ProfileDeck executable is missing: %w", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("ProfileDeck executable is not executable")
	}
	architectureOutput, err := runner.run(ctx, "xcrun", "lipo", "-archs", executablePath)
	if err != nil {
		return err
	}
	architectures := strings.Fields(string(architectureOutput))
	sort.Strings(architectures)
	if strings.Join(architectures, " ") != "arm64 x86_64" {
		return fmt.Errorf("ProfileDeck executable architectures are %q, want arm64 and x86_64", architectures)
	}
	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	expectedValues := map[string]string{
		"CFBundleIdentifier":            "io.github.strahe.profiledeck",
		"CFBundleExecutable":            "profiledeck-desktop",
		"CFBundleIconFile":              "icons",
		"CFBundleIconName":              "appicon",
		"CFBundleShortVersionString":    version.short(),
		"CFBundleVersion":               fmt.Sprintf("%d", buildNumber),
		"LSMinimumSystemVersion":        "14.0",
		"CFBundleInfoDictionaryVersion": "6.0",
	}
	for key, expected := range expectedValues {
		actual, err := plistValue(ctx, runner, plistPath, key)
		if err != nil {
			return fmt.Errorf("read %s from Info.plist: %w", key, err)
		}
		if actual != expected {
			return fmt.Errorf("bundle Info.plist %s is %q, want %q", key, actual, expected)
		}
	}
	for _, name := range []string{"Assets.car", "icons.icns"} {
		path := filepath.Join(appPath, "Contents", "Resources", name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("application icon resource %s is missing: %w", name, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("application icon resource %s is empty", name)
		}
	}
	if _, err := runner.run(
		ctx,
		"codesign",
		"--verify",
		"--deep",
		"--strict",
		"--verbose=2",
		appPath,
	); err != nil {
		return err
	}
	details, err := runner.run(ctx, "codesign", "--display", "--verbose=4", appPath)
	if err != nil {
		return err
	}
	signature := string(details)
	for _, required := range []string{
		"Authority=Developer ID Application:",
		"runtime",
		"Timestamp=",
	} {
		if !strings.Contains(signature, required) {
			return fmt.Errorf("application signature is missing %q", required)
		}
	}
	if _, err := runner.run(ctx, "xcrun", "stapler", "validate", appPath); err != nil {
		return err
	}
	if _, err := runner.run(
		ctx,
		"spctl",
		"--assess",
		"--type",
		"execute",
		"--verbose=4",
		appPath,
	); err != nil {
		return err
	}
	return nil
}

func verifyDMG(
	ctx context.Context,
	runner commandRunner,
	dmgPath string,
	version releaseVersion,
	buildNumber int,
	temporaryRoot string,
) error {
	if _, err := runner.run(ctx, "hdiutil", "verify", dmgPath); err != nil {
		return err
	}
	if _, err := runner.run(ctx, "codesign", "--verify", "--verbose=2", dmgPath); err != nil {
		return err
	}
	details, err := runner.run(ctx, "codesign", "--display", "--verbose=4", dmgPath)
	if err != nil {
		return err
	}
	signature := string(details)
	for _, required := range []string{"Authority=Developer ID Application:", "Timestamp="} {
		if !strings.Contains(signature, required) {
			return fmt.Errorf("DMG signature is missing %q", required)
		}
	}
	if _, err := runner.run(ctx, "xcrun", "stapler", "validate", dmgPath); err != nil {
		return err
	}
	if _, err := runner.run(
		ctx,
		"spctl",
		"--assess",
		"--type",
		"open",
		"--context",
		"context:primary-signature",
		"--verbose=4",
		dmgPath,
	); err != nil {
		return err
	}
	mountPath := filepath.Join(temporaryRoot, "dmg")
	if err := os.Mkdir(mountPath, 0o700); err != nil {
		return fmt.Errorf("create DMG mount point: %w", err)
	}
	mounted := false
	defer func() {
		if mounted {
			_, _ = runner.run(context.Background(), "diskutil", "eject", mountPath)
		}
	}()
	if _, err := runner.run(
		ctx,
		"diskutil",
		"image",
		"attach",
		"--readOnly",
		"--nobrowse",
		"--mountPoint",
		mountPath,
		dmgPath,
	); err != nil {
		return err
	}
	mounted = true
	applicationsPath := filepath.Join(mountPath, "Applications")
	target, err := os.Readlink(applicationsPath)
	if err != nil || target != "/Applications" {
		return fmt.Errorf("DMG is missing its Applications shortcut")
	}
	if err := verifyApp(
		ctx,
		runner,
		filepath.Join(mountPath, "ProfileDeck.app"),
		version,
		buildNumber,
	); err != nil {
		return err
	}
	if _, err := runner.run(ctx, "diskutil", "eject", mountPath); err != nil {
		return err
	}
	mounted = false
	return nil
}

func verifyLocalRelease(
	ctx context.Context,
	runner commandRunner,
	directory string,
	version releaseVersion,
	buildNumber int,
	verifyAppleArtifacts bool,
) (releaseMetadata, error) {
	if err := verifyDirectoryLayout(directory, version); err != nil {
		return releaseMetadata{}, err
	}
	metadata, err := readMetadata(directory, version)
	if err != nil {
		return releaseMetadata{}, err
	}
	if metadata.BuildNumber != buildNumber {
		return releaseMetadata{}, fmt.Errorf(
			"release metadata build number is %d, want %d",
			metadata.BuildNumber,
			buildNumber,
		)
	}
	zipPath := filepath.Join(directory, updaterZIPName(version))
	if err := verifyZIPLayout(zipPath); err != nil {
		return releaseMetadata{}, err
	}
	if !verifyAppleArtifacts {
		return metadata, nil
	}
	temporaryRoot, err := os.MkdirTemp("", "profiledeck-release-verify-*")
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("create verification directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(temporaryRoot)
	}()
	if _, err := runner.run(ctx, "ditto", "-x", "-k", zipPath, temporaryRoot); err != nil {
		return releaseMetadata{}, err
	}
	entries, err := os.ReadDir(temporaryRoot)
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("read extracted updater ZIP: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != "ProfileDeck.app" || !entries[0].IsDir() {
		return releaseMetadata{}, fmt.Errorf("updater ZIP must contain exactly one top-level ProfileDeck.app")
	}
	if err := verifyApp(
		ctx,
		runner,
		filepath.Join(temporaryRoot, "ProfileDeck.app"),
		version,
		buildNumber,
	); err != nil {
		return releaseMetadata{}, err
	}
	if err := verifyDMG(
		ctx,
		runner,
		filepath.Join(directory, installerDMGName(version)),
		version,
		buildNumber,
		temporaryRoot,
	); err != nil {
		return releaseMetadata{}, err
	}
	return metadata, nil
}
