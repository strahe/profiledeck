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
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		identity, err := discoverIdentity(ctx, runner, *requested)
		if err != nil {
			return err
		}
		fmt.Println(identity)
		return nil

	case "preflight":
		options, err := parseReleaseOptions("preflight", args[1:], true)
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(options.releasesDirectory, options.version)
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
		)

	case "prepare":
		version, releasesDirectory, err := parseWorkspaceOptions("prepare", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version)
		if err != nil {
			return err
		}
		return workspace.prepare()

	case "cleanup":
		version, releasesDirectory, err := parseWorkspaceOptions("cleanup", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version)
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
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		_, err := notarize(ctx, runner, *input, *profile)
		return err

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
		if _, err := writeChecksums(*directory, version); err != nil {
			return err
		}
		return writeMetadata(*directory, version, buildNumber, *commit, builtAt)

	case "verify":
		options, err := parseReleaseOptions("verify", args[1:], false)
		if err != nil {
			return err
		}
		directory := options.directory
		if directory == "" {
			workspace, err := newReleaseWorkspace(options.releasesDirectory, options.version)
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
		version, releasesDirectory, err := parseWorkspaceOptions("commit", args[1:])
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(releasesDirectory, version)
		if err != nil {
			return err
		}
		metadata, err := readMetadata(workspace.artifacts, version)
		if err != nil {
			return err
		}
		if err := verifyDirectoryLayout(workspace.artifacts, version); err != nil {
			return err
		}
		if err := workspace.commit(); err != nil {
			return err
		}
		fmt.Printf(
			"macOS release %s (build %d) is ready: %s\n",
			version,
			metadata.BuildNumber,
			workspace.final,
		)
		return nil

	case "draft":
		flags := newFlagSet("draft")
		versionValue := flags.String("version", "", "release version")
		repository := flags.String("repo", "", "GitHub owner/repository")
		releasesDirectory := flags.String("releases-dir", ".task/releases", "temporary releases directory")
		candidatePath := flags.String("candidate", "bin/ProfileDeck.dmg", "retained release candidate DMG")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		workspace, err := newReleaseWorkspace(*releasesDirectory, version)
		if err != nil {
			return err
		}
		metadata, err := readMetadata(workspace.final, version)
		if err != nil {
			return err
		}
		if _, err := verifyLocalRelease(
			ctx,
			runner,
			workspace.final,
			version,
			metadata.BuildNumber,
			false,
		); err != nil {
			return err
		}
		release, err := createDraftRelease(
			ctx,
			runner,
			*repository,
			version,
			workspace.final,
			metadata.Commit,
		)
		if err != nil {
			return err
		}
		if err := promoteCandidateDMG(
			filepath.Join(workspace.final, installerDMGName(version)),
			*candidatePath,
		); err != nil {
			return err
		}
		if err := workspace.removeFinal(); err != nil {
			return err
		}
		fmt.Printf("Draft Release is ready for review: %s\n", release.URL)
		fmt.Printf("Release candidate is ready: %s\n", filepath.Clean(*candidatePath))
		return nil

	case "copy-draft":
		flags := newFlagSet("copy-draft")
		versionValue := flags.String("version", "", "release version")
		sourceRepository := flags.String("source-repo", "", "source GitHub owner/repository")
		repository := flags.String("repo", "", "target GitHub owner/repository")
		candidatePath := flags.String("candidate", "bin/ProfileDeck.dmg", "retained release candidate DMG")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		release, err := copyDraftRelease(
			ctx,
			runner,
			*sourceRepository,
			*repository,
			version,
			*candidatePath,
		)
		if err != nil {
			return err
		}
		fmt.Printf("Draft Release is ready for review: %s\n", release.URL)
		return nil

	case "publish":
		flags := newFlagSet("publish")
		versionValue := flags.String("version", "", "release version")
		repository := flags.String("repo", "", "GitHub owner/repository")
		candidatePath := flags.String("candidate", "bin/ProfileDeck.dmg", "retained release candidate DMG")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		release, err := publishRelease(
			ctx,
			runner,
			*repository,
			version,
			*candidatePath,
		)
		if err != nil {
			return err
		}
		fmt.Printf("Release published: %s\n", release.URL)
		return nil

	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type releaseOptions struct {
	version           releaseVersion
	buildNumber       int
	releasesDirectory string
	directory         string
	identity          string
	notaryProfile     string
}

func parseReleaseOptions(name string, args []string, includeSigning bool) (releaseOptions, error) {
	flags := newFlagSet(name)
	versionValue := flags.String("version", "", "release version")
	buildValue := flags.String("build-number", "", "bundle build number")
	releasesDirectory := flags.String("releases-dir", ".task/releases", "release output root")
	directory := flags.String("directory", "", "release artifact directory")
	identity := flags.String("identity", "", "Developer ID identity")
	notaryProfile := flags.String("notary-profile", "", "notarytool Keychain profile")
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
		releasesDirectory: *releasesDirectory,
		directory:         cleanDirectory,
		identity:          *identity,
		notaryProfile:     *notaryProfile,
	}, nil
}

func parseWorkspaceOptions(name string, args []string) (releaseVersion, string, error) {
	flags := newFlagSet(name)
	versionValue := flags.String("version", "", "release version")
	releasesDirectory := flags.String("releases-dir", ".task/releases", "release output root")
	if err := flags.Parse(args); err != nil {
		return releaseVersion{}, "", err
	}
	version, err := parseReleaseVersion(*versionValue)
	return version, *releasesDirectory, err
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}
