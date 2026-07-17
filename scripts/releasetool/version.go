package main

import (
	"fmt"
	"regexp"
	"strconv"
)

var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-beta\.([1-9][0-9]*))?$`)

type releaseVersion struct {
	major int
	minor int
	patch int
	beta  int
}

func parseReleaseVersion(value string) (releaseVersion, error) {
	matches := releaseVersionPattern.FindStringSubmatch(value)
	if matches == nil {
		return releaseVersion{}, fmt.Errorf("version must be X.Y.Z or X.Y.Z-beta.N")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	beta := 0
	if matches[4] != "" {
		beta, _ = strconv.Atoi(matches[4])
	}
	return releaseVersion{major: major, minor: minor, patch: patch, beta: beta}, nil
}

func (version releaseVersion) String() string {
	base := fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
	if version.beta > 0 {
		return fmt.Sprintf("%s-beta.%d", base, version.beta)
	}
	return base
}

func (version releaseVersion) short() string {
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
}

func (version releaseVersion) channel() string {
	if version.beta > 0 {
		return "beta"
	}
	return "stable"
}

func (version releaseVersion) tag() string {
	return "v" + version.String()
}

func (version releaseVersion) compare(other releaseVersion) int {
	for _, pair := range [][2]int{
		{version.major, other.major},
		{version.minor, other.minor},
		{version.patch, other.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	switch {
	case version.beta == other.beta:
		return 0
	case version.beta == 0:
		return 1
	case other.beta == 0:
		return -1
	case version.beta < other.beta:
		return -1
	default:
		return 1
	}
}

func parseBuildNumber(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || strconv.Itoa(number) != value {
		return 0, fmt.Errorf("build number must be a positive integer")
	}
	return number, nil
}

func updaterZIPName(version releaseVersion) string {
	return fmt.Sprintf("ProfileDeck_%s_macos_universal.zip", version)
}

func installerDMGName(version releaseVersion) string {
	return fmt.Sprintf("ProfileDeck_%s_macos_universal.dmg", version)
}
