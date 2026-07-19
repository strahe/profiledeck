package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	identityPattern = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"([^"]*Developer ID Application:[^"]*)"\s*$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type developerIDIdentity struct {
	fingerprint string
	name        string
}

func formatDeveloperIDIdentity(identity developerIDIdentity, output string) (string, error) {
	switch output {
	case "fingerprint":
		// Signing by fingerprint prevents duplicate certificate names from becoming ambiguous.
		return identity.fingerprint, nil
	case "name":
		return identity.name, nil
	default:
		return "", fmt.Errorf("identity output must be fingerprint or name")
	}
}

func parseDeveloperIDIdentities(output string) []developerIDIdentity {
	identitiesByFingerprint := make(map[string]developerIDIdentity)
	for _, line := range strings.Split(output, "\n") {
		match := identityPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		fingerprint := strings.ToUpper(match[1])
		identitiesByFingerprint[fingerprint] = developerIDIdentity{
			fingerprint: fingerprint,
			name:        match[2],
		}
	}
	identities := make([]developerIDIdentity, 0, len(identitiesByFingerprint))
	for _, identity := range identitiesByFingerprint {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].name == identities[j].name {
			return identities[i].fingerprint < identities[j].fingerprint
		}
		return identities[i].name < identities[j].name
	})
	return identities
}

func inspectDeveloperIDIdentities(
	ctx context.Context,
	runner commandRunner,
	keychain string,
) ([]developerIDIdentity, error) {
	args := []string{"find-identity", "-v", "-p", "codesigning"}
	if keychain != "" {
		args = append(args, keychain)
	}
	output, err := runner.run(ctx, "security", args...)
	if err != nil {
		return nil, fmt.Errorf("could not inspect Developer ID signing identities")
	}
	return parseDeveloperIDIdentities(string(output)), nil
}

func selectDeveloperIDIdentity(
	identities []developerIDIdentity,
	requested string,
) (developerIDIdentity, error) {
	if requested != "" {
		var matches []developerIDIdentity
		for _, identity := range identities {
			if strings.EqualFold(identity.fingerprint, requested) || identity.name == requested {
				matches = append(matches, identity)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			return developerIDIdentity{}, fmt.Errorf(
				"SIGN_IDENTITY does not match a valid Developer ID Application identity; use the full certificate name or 40-character SHA-1 fingerprint from security find-identity -v -p codesigning",
			)
		default:
			return developerIDIdentity{}, fmt.Errorf(
				"SIGN_IDENTITY matches %d Developer ID Application identities; use the 40-character SHA-1 fingerprint from security find-identity -v -p codesigning",
				len(matches),
			)
		}
	}
	if len(identities) != 1 {
		return developerIDIdentity{}, developerIDIdentityCountError(len(identities))
	}
	return identities[0], nil
}

func developerIDIdentityCountError(count int) error {
	return fmt.Errorf(
		"expected one Developer ID Application identity, found %d; set SIGN_IDENTITY to the 40-character SHA-1 fingerprint from security find-identity -v -p codesigning when multiple identities are installed",
		count,
	)
}

func promptDeveloperIDIdentity(
	identities []developerIDIdentity,
	input io.Reader,
	output io.Writer,
) (developerIDIdentity, error) {
	if len(identities) <= 1 {
		return selectDeveloperIDIdentity(identities, "")
	}
	write := func(format string, args ...any) error {
		if _, err := fmt.Fprintf(output, format, args...); err != nil {
			return fmt.Errorf("could not display signing identity choices")
		}
		return nil
	}
	if err := write("Multiple Developer ID Application identities were found:\n\n"); err != nil {
		return developerIDIdentity{}, err
	}
	for i, identity := range identities {
		fingerprint := identity.fingerprint
		if len(fingerprint) > 14 {
			fingerprint = fingerprint[:8] + "..." + fingerprint[len(fingerprint)-6:]
		}
		if err := write("%d. %s\n   SHA-1: %s\n", i+1, identity.name, fingerprint); err != nil {
			return developerIDIdentity{}, err
		}
	}
	if err := write("\n"); err != nil {
		return developerIDIdentity{}, err
	}

	scanner := bufio.NewScanner(input)
	for {
		if err := write("Select an identity [1-%d], or q to cancel: ", len(identities)); err != nil {
			return developerIDIdentity{}, err
		}
		if !scanner.Scan() {
			if scanner.Err() != nil {
				return developerIDIdentity{}, fmt.Errorf("could not read signing identity selection")
			}
			return developerIDIdentity{}, fmt.Errorf("signing identity selection was cancelled")
		}
		value := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(value, "q") {
			return developerIDIdentity{}, fmt.Errorf("signing identity selection was cancelled")
		}
		selection, err := strconv.Atoi(value)
		if err == nil && selection >= 1 && selection <= len(identities) {
			return identities[selection-1], nil
		}
		if err := write("Enter a number from 1 to %d, or q to cancel.\n", len(identities)); err != nil {
			return developerIDIdentity{}, err
		}
	}
}

func discoverIdentity(
	ctx context.Context,
	runner commandRunner,
	requested string,
	keychain string,
) (developerIDIdentity, error) {
	identities, err := inspectDeveloperIDIdentities(ctx, runner, keychain)
	if err != nil {
		return developerIDIdentity{}, err
	}
	return selectDeveloperIDIdentity(identities, requested)
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
	keychain string,
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
	if _, err := discoverIdentity(ctx, runner, identity, keychain); err != nil {
		return err
	}
	notaryArgs := []string{"notarytool", "history"}
	notaryArgs = append(notaryArgs, notaryCredentialArgs(notaryProfile, keychain)...)
	if _, err := runner.run(ctx, "xcrun", notaryArgs...); err != nil {
		return fmt.Errorf(
			"could not validate the notary profile; verify the profile and Keychain configuration",
		)
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
		"Release preflight passed for %s (build %d); signing and notarization credentials are ready.\n",
		version,
		buildNumber,
	)
	return nil
}
