package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "releasetool: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "contract":
		flags := newFlagSet("contract")
		versionValue := flags.String("version", "", "release version")
		platforms := flags.String("platforms", macOSPlatform, "comma-separated release platforms")
		field := flags.String("field", "", "single contract field")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		contract, err := buildReleaseContract(version, *platforms)
		if err != nil {
			return err
		}
		if *field != "" {
			return writeContractField(stdout, contract, *field)
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(contract)

	case "version-check":
		flags := newFlagSet("version-check")
		versionValue := flags.String("version", "", "candidate release version")
		tagsPath := flags.String("tags", "", "published tag list path or - for stdin")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, err := parseReleaseVersion(*versionValue)
		if err != nil {
			return err
		}
		reader, closeReader, err := openTagReader(*tagsPath)
		if err != nil {
			return err
		}
		defer closeReader()
		if err := requireNewerVersion(version, reader); err != nil {
			return err
		}
		return writeResult(stdout, "%s is newer than every published release.\n", version)

	case "handoff":
		flags := newFlagSet("handoff")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "release build number")
		commit := flags.String("commit", "", "release Git commit")
		builtAtValue := flags.String("built-at", "", "RFC3339 build time")
		platform := flags.String("platform", "", "release platform")
		input := flags.String("input", "", "raw platform asset directory")
		output := flags.String("output", "", "final platform handoff directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		version, buildNumber, builtAt, err := parseBuildIdentity(*versionValue, *buildValue, *builtAtValue)
		if err != nil {
			return err
		}
		if err := createPlatformHandoff(*input, *output, *platform, version, buildNumber, *commit, builtAt); err != nil {
			return err
		}
		return writeResult(stdout, "%s release handoff is ready: %s\n", *platform, *output)

	case "assemble":
		flags := newFlagSet("assemble")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "release build number")
		commit := flags.String("commit", "", "release Git commit")
		output := flags.String("output", "", "final release bundle directory")
		var handoffs repeatedFlag
		flags.Var(&handoffs, "handoff", "platform=handoff-directory (repeatable)")
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
		inputs, err := parsePlatformInputs(handoffs, version)
		if err != nil {
			return err
		}
		if err := assembleRelease(*output, version, buildNumber, *commit, inputs); err != nil {
			return err
		}
		return writeResult(stdout, "Release bundle is ready: %s\n", *output)

	case "verify-bundle":
		flags := newFlagSet("verify-bundle")
		versionValue := flags.String("version", "", "release version")
		buildValue := flags.String("build-number", "", "release build number")
		commit := flags.String("commit", "", "release Git commit")
		platforms := flags.String("platforms", "", "comma-separated release platforms")
		directory := flags.String("directory", "", "release bundle directory")
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
		if _, err := verifyReleaseBundle(*directory, version, buildNumber, *commit, definitions); err != nil {
			return err
		}
		return writeResult(stdout, "Release bundle verified: %s\n", *directory)

	case "update-public-key":
		flags := newFlagSet("update-public-key")
		privateKey := flags.String("private-key", "", "PKCS#8 Ed25519 private key")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		publicKey, err := updatePublicKeyBase64(*privateKey)
		if err != nil {
			return err
		}
		return writeResult(stdout, "%s\n", publicKey)

	case "sign-update":
		flags := newFlagSet("sign-update")
		privateKey := flags.String("private-key", "", "PKCS#8 Ed25519 private key")
		artifact := flags.String("artifact", "", "update artifact")
		output := flags.String("output", "", "signature sidecar")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return signUpdateArtifact(*privateKey, *artifact, *output)

	case "verify-update-signature":
		flags := newFlagSet("verify-update-signature")
		publicKey := flags.String("public-key", "", "base64 Ed25519 public key")
		artifact := flags.String("artifact", "", "update artifact")
		signature := flags.String("signature", "", "signature sidecar")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return verifyUpdateArtifactSignature(*publicKey, *artifact, *signature)

	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseBuildIdentity(versionValue, buildValue, builtAtValue string) (releaseVersion, int, time.Time, error) {
	version, err := parseReleaseVersion(versionValue)
	if err != nil {
		return releaseVersion{}, 0, time.Time{}, err
	}
	buildNumber, err := parseBuildNumber(buildValue)
	if err != nil {
		return releaseVersion{}, 0, time.Time{}, err
	}
	builtAt, err := time.Parse(time.RFC3339, builtAtValue)
	if err != nil || builtAt.Format(time.RFC3339) != builtAtValue {
		return releaseVersion{}, 0, time.Time{}, fmt.Errorf("built-at must use canonical RFC3339")
	}
	return version, buildNumber, builtAt, nil
}

func openTagReader(path string) (io.Reader, func(), error) {
	if path == "-" {
		return bufio.NewReader(os.Stdin), func() {}, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil, fmt.Errorf("tags path is required; use - for stdin")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open published tag list: %w", err)
	}
	return file, func() { _ = file.Close() }, nil
}

type repeatedFlag []string

func (value *repeatedFlag) String() string { return strings.Join(*value, ",") }

func (value *repeatedFlag) Set(entry string) error {
	*value = append(*value, entry)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func writeResult(writer io.Writer, format string, arguments ...any) error {
	if _, err := fmt.Fprintf(writer, format, arguments...); err != nil {
		return fmt.Errorf("write command result: %w", err)
	}
	return nil
}
