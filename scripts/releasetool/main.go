package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "releasetool: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("a command is required")
	}
	runner := systemCommandRunner{}
	switch args[0] {
	case "identity":
		flags := newFlagSet("identity")
		requested := flags.String("requested", "", "Developer ID identity override")
		keychain := flags.String("keychain", "", "Keychain containing the signing identity")
		output := flags.String("output", "fingerprint", "identity output: fingerprint or name")
		interactive := flags.Bool("interactive", false, "prompt when multiple signing identities are available")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		identities, err := inspectDeveloperIDIdentities(ctx, runner, cleanOptionalPath(*keychain))
		if err != nil {
			return err
		}
		var identity developerIDIdentity
		if *interactive && *requested == "" && len(identities) > 1 {
			// The controlling terminal keeps prompts out of stdout and prevents automation from waiting on stdin.
			terminal, openErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if openErr != nil {
				return developerIDIdentityCountError(len(identities))
			}
			defer terminal.Close()
			identity, err = promptDeveloperIDIdentity(identities, terminal, terminal)
		} else {
			identity, err = selectDeveloperIDIdentity(identities, *requested)
		}
		if err != nil {
			return err
		}
		value, err := formatDeveloperIDIdentity(identity, *output)
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil

	case "preflight":
		options, err := parseReleaseOptions("preflight", args[1:], true)
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(
			options.releasesDirectory,
			options.version,
			options.platform,
		)
		if err != nil {
			return err
		}
		return preflight(
			ctx,
			runner,
			options.version,
			options.buildNumber,
			workspace,
			options.identity,
			options.notaryProfile,
			options.keychain,
		)

	case "prepare":
		version, releasesDirectory, platform, err := parseWorkspaceOptions("prepare", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version, platform)
		if err != nil {
			return err
		}
		return workspace.prepare()

	case "cleanup":
		version, releasesDirectory, platform, err := parseWorkspaceOptions("cleanup", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version, platform)
		if err != nil {
			return err
		}
		return workspace.cleanup()

	case "plist":
		flags := newFlagSet("plist")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "bundle build number")
		templatePath := flags.String("template", "", "Info.plist template path")
		outputPath := flags.String("output", "", "Info.plist output path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		buildNumber, err := parseBuildNumber(*buildValue)
		if err != nil {
			return err
		}
		if *templatePath == "" || *outputPath == "" {
			return fmt.Errorf("template and output paths for Info.plist are required")
		}
		return writeInfoPlist(*templatePath, *outputPath, version, buildNumber)

	case "notarize":
		flags := newFlagSet("notarize")
		input := flags.String("input", "", "artifact to submit")
		profile := flags.String("profile", "", "notarytool Keychain profile")
		keychain := flags.String("keychain", "", "Keychain containing the notary profile")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		_, err := notarize(ctx, runner, *input, *profile, cleanOptionalPath(*keychain))
		return err

	case "github-check":
		flags := newFlagSet("github-check")
		versionValue := flags.String("version", "", "release version")
		repository := flags.String("repo", "", "GitHub owner/repository")
		commit := flags.String("commit", "", "release Git commit")
		platforms := flags.String("platforms", macOSPlatform, "comma-separated release platforms")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		definitions, err := parseReleasePlatforms(*platforms, version)
		if err != nil {
			return err
		}
		if err := checkGitHubRelease(
			ctx,
			runner,
			*repository,
			version,
			*commit,
			definitions,
		); err != nil {
			return err
		}
		fmt.Printf("GitHub release state is ready for %s at %s\n", version.tag(), *commit)
		return nil

	case "source-check":
		flags := newFlagSet("source-check")
		commit := flags.String("commit", "", "built Git commit")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return verifySourceState(ctx, runner, *commit)

	case "manifest":
		flags := newFlagSet("manifest")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "bundle build number")
		commit := flags.String("commit", "", "built Git commit")
		builtAtValue := flags.String("built-at", "", "RFC3339 build time")
		directory := flags.String("directory", "", "artifact directory")
		platform := flags.String("platform", macOSPlatform, "release platform")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		buildNumber, err := parseBuildNumber(*buildValue)
		if err != nil {
			return err
		}
		builtAt, err := time.Parse(time.RFC3339, *builtAtValue)
		if err != nil {
			return fmt.Errorf("built-at must use RFC3339: %w", err)
		}
		specs, err := platformAssetSpecs(*platform, version)
		if err != nil {
			return err
		}
		if _, err := writeChecksums(*directory, specs); err != nil {
			return err
		}
		return writeMetadata(*directory, *platform, version, buildNumber, *commit, builtAt)

	case "verify":
		options, err := parseReleaseOptions("verify", args[1:], false)
		if err != nil {
			return err
		}
		directory := options.directory
		if directory == "" {
			workspace, err := newReleaseWorkspace(
				options.releasesDirectory,
				options.version,
				options.platform,
			)
			if err != nil {
				return err
			}
			directory = workspace.artifacts
		}
		_, err = verifyLocalRelease(
			ctx,
			runner,
			directory,
			options.version,
			options.buildNumber,
			true,
		)
		if err == nil {
			fmt.Printf("Verified macOS release artifacts in %s\n", directory)
		}
		return err

	case "commit":
		version, releasesDirectory, platform, err := parseWorkspaceOptions("commit", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version, platform)
		if err != nil {
			return err
		}
		metadata, err := readMetadata(workspace.artifacts, platform, version)
		if err != nil {
			return err
		}
		if err := verifyPlatformDirectoryLayout(workspace.artifacts, platform, version); err != nil {
			return err
		}
		if err := workspace.commit(); err != nil {
			return err
		}
		fmt.Printf(
			"%s release %s (build %d) is ready: %s\n",
			platform,
			version,
			metadata.BuildNumber,
			workspace.final,
		)
		return nil

	case "assemble":
		flags := newFlagSet("assemble")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "release build number")
		commit := flags.String("commit", "", "release Git commit")
		platforms := flags.String("platforms", macOSPlatform, "comma-separated release platforms")
		releasesDirectory := flags.String("releases-dir", ".task/releases", "release output root")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		buildNumber, err := parseBuildNumber(*buildValue)
		if err != nil {
			return err
		}
		definitions, err := parseReleasePlatforms(*platforms, version)
		if err != nil {
			return err
		}
		directory, err := assembleRelease(
			*releasesDirectory,
			version,
			buildNumber,
			*commit,
			definitions,
		)
		if err != nil {
			return err
		}
		fmt.Printf("Release bundle is ready: %s\n", directory)
		return nil

	case "draft":
		flags := newFlagSet("draft")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "release build number")
		repository := flags.String("repo", "", "GitHub owner/repository")
		commit := flags.String("commit", "", "release Git commit")
		platforms := flags.String("platforms", macOSPlatform, "comma-separated release platforms")
		releasesDirectory := flags.String("releases-dir", ".task/releases", "temporary releases directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		buildNumber, err := parseBuildNumber(*buildValue)
		if err != nil {
			return err
		}
		definitions, err := parseReleasePlatforms(*platforms, version)
		if err != nil {
			return err
		}
		bundleDirectory, err := releaseBundlePath(*releasesDirectory, version)
		if err != nil {
			return err
		}
		metadata, err := verifyReleaseBundle(
			bundleDirectory,
			version,
			buildNumber,
			*commit,
			definitions,
		)
		if err != nil {
			return err
		}
		release, err := createDraftRelease(
			ctx,
			runner,
			*repository,
			version,
			bundleDirectory,
			metadata,
			definitions,
		)
		if err != nil {
			return err
		}
		if err := removeReleaseBundle(*releasesDirectory, version); err != nil {
			return err
		}
		fmt.Printf("Draft Release is ready for review: %s\n", release.URL)
		return nil

	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type releaseOptions struct {
	version           releaseVersion
	buildNumber       int
	platform          string
	releasesDirectory string
	directory         string
	identity          string
	notaryProfile     string
	keychain          string
}

func parseReleaseOptions(name string, args []string, includeSigning bool) (releaseOptions, error) {
	flags := newFlagSet(name)
	versionValue := flags.String("version", "", "release version")
	buildValue := flags.String("build-number", "", "bundle build number")
	releasesDirectory := flags.String("releases-dir", ".task/releases", "release output root")
	directory := flags.String("directory", "", "release artifact directory")
	identity := flags.String("identity", "", "Developer ID identity")
	notaryProfile := flags.String("notary-profile", "", "notarytool Keychain profile")
	keychain := flags.String("keychain", "", "Keychain containing release credentials")
	platform := flags.String("platform", macOSPlatform, "release platform")
	if err := flags.Parse(args); err != nil {
		return releaseOptions{}, err
	}
	version, err := parseReleaseVersion(*versionValue)
	if err != nil {
		return releaseOptions{}, err
	}
	buildNumber, err := parseBuildNumber(*buildValue)
	if err != nil {
		return releaseOptions{}, err
	}
	if _, err := platformAssetSpecs(*platform, version); err != nil {
		return releaseOptions{}, err
	}
	if includeSigning && (*identity == "" || *notaryProfile == "") {
		return releaseOptions{}, fmt.Errorf("developer ID identity and notary profile are required")
	}
	cleanDirectory := ""
	if *directory != "" {
		cleanDirectory = filepath.Clean(*directory)
	}
	return releaseOptions{
		version:           version,
		buildNumber:       buildNumber,
		platform:          *platform,
		releasesDirectory: *releasesDirectory,
		directory:         cleanDirectory,
		identity:          *identity,
		notaryProfile:     *notaryProfile,
		keychain:          cleanOptionalPath(*keychain),
	}, nil
}

func cleanOptionalPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func parseWorkspaceOptions(
	name string,
	args []string,
) (releaseVersion, string, string, error) {
	flags := newFlagSet(name)
	versionValue := flags.String("version", "", "release version")
	releasesDirectory := flags.String("releases-dir", ".task/releases", "release output root")
	platform := flags.String("platform", macOSPlatform, "release platform")
	if err := flags.Parse(args); err != nil {
		return releaseVersion{}, "", "", err
	}
	version, err := parseReleaseVersion(*versionValue)
	if err != nil {
		return releaseVersion{}, "", "", err
	}
	if _, err := platformAssetSpecs(*platform, version); err != nil {
		return releaseVersion{}, "", "", err
	}
	return version, *releasesDirectory, *platform, nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}
