package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var (
	identityPattern = regexp.MustCompile(`"([^"]*Developer ID Application:[^"]*)"`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func parseDeveloperIDIdentities(output string) []string {
	var identities []string
	for _, line := range strings.Split(output, "\n") {
		match := identityPattern.FindStringSubmatch(line)
		if match != nil {
			identities = append(identities, match[1])
		}
	}
	sort.Strings(identities)
	return identities
}

func discoverIdentity(ctx context.Context, runner commandRunner, requested string) (string, error) {
	output, err := runner.run(ctx, "security", "find-identity", "-v", "-p", "codesigning")
	if err != nil {
		return "", err
	}
	identities := parseDeveloperIDIdentities(string(output))
	if requested != "" {
		for _, identity := range identities {
			if identity == requested {
				return identity, nil
			}
		}
		return "", fmt.Errorf("SIGN_IDENTITY is not a valid Developer ID Application identity")
	}
	if len(identities) != 1 {
		return "", fmt.Errorf(
			"expected one Developer ID Application identity, found %d; set SIGN_IDENTITY when multiple identities are installed",
			len(identities),
		)
	}
	return identities[0], nil
}

func verifySourceState(ctx context.Context, runner commandRunner, expectedCommit string) error {
	if !commitPattern.MatchString(expectedCommit) {
		return fmt.Errorf("expected commit must be a full lowercase Git SHA")
	}
	head, err := runner.run(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != expectedCommit {
		return fmt.Errorf("git HEAD changed while building the release")
	}
	status, err := runner.run(ctx, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return fmt.Errorf("the release build changed tracked or untracked source files")
	}
	return nil
}

func preflight(
	ctx context.Context,
	runner commandRunner,
	version releaseVersion,
	buildNumber int,
	workspace releaseWorkspace,
	identity string,
	notaryProfile string,
) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("macOS release builds must run on macOS")
	}
	if buildNumber < 1 {
		return fmt.Errorf("build number must be a positive integer")
	}
	if notaryProfile == "" {
		return fmt.Errorf("notary profile is required")
	}
	// Wails invokes this preflight, so resolving another copy from PATH could validate the wrong executable.
	if err := requireCommands(
		"git",
		"go",
		"npm",
		"security",
		"codesign",
		"xcrun",
		"ditto",
		"diskutil",
		"hdiutil",
		"plutil",
		"spctl",
	); err != nil {
		return err
	}
	status, err := runner.run(ctx, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return fmt.Errorf("the Git working tree must be clean before building a release")
	}
	for _, path := range []string{workspace.stage, workspace.final} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("release path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect release path: %w", err)
		}
	}
	for _, tool := range []string{"notarytool", "stapler", "clang"} {
		if _, err := runner.run(ctx, "xcrun", "--find", tool); err != nil {
			return err
		}
	}
	if _, err := discoverIdentity(ctx, runner, identity); err != nil {
		return err
	}
	if _, err := runner.run(
		ctx,
		"xcrun",
		"notarytool",
		"history",
		"--keychain-profile",
		notaryProfile,
	); err != nil {
		return fmt.Errorf("validate notary profile %q: %w", notaryProfile, err)
	}
	targets, err := runner.run(ctx, "go", "tool", "dist", "list")
	if err != nil {
		return err
	}
	targetSet := "\n" + string(targets) + "\n"
	for _, target := range []string{"darwin/arm64", "darwin/amd64"} {
		if !strings.Contains(targetSet, "\n"+target+"\n") {
			return fmt.Errorf("go cannot build %s", target)
		}
	}
	fmt.Printf(
		"Release preflight passed: %s (build %d), %s, notary profile %s\n",
		version,
		buildNumber,
		identity,
		notaryProfile,
	)
	return nil
}
