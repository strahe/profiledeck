package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

type releaseHistoryEntry struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func runNotesStartTag(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := newFlagSet("notes-start-tag")
	version := flags.String("version", "", "release version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("notes-start-tag does not accept positional arguments")
	}
	current, err := releaseartifact.NewContract(*version)
	if err != nil {
		return err
	}

	history, err := decodeReleaseHistory(stdin)
	if err != nil {
		return err
	}
	startTag, err := selectNotesStartTag(current, history)
	if err != nil {
		return err
	}
	if startTag == "" {
		return nil
	}
	if _, err := fmt.Fprintln(stdout, startTag); err != nil {
		return fmt.Errorf("write release notes start tag: %w", err)
	}
	return nil
}

func decodeReleaseHistory(reader io.Reader) ([]releaseHistoryEntry, error) {
	if reader == nil {
		return nil, nil
	}
	decoder := json.NewDecoder(reader)
	var history []releaseHistoryEntry
	for {
		var entry releaseHistoryEntry
		if err := decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				return history, nil
			}
			return nil, fmt.Errorf("decode release history: %w", err)
		}
		history = append(history, entry)
	}
}

func selectNotesStartTag(
	current releaseartifact.Contract,
	history []releaseHistoryEntry,
) (string, error) {
	stableTag := ""
	betaTag := ""
	for _, entry := range history {
		if entry.Draft || !strings.HasPrefix(entry.TagName, "v") {
			continue
		}
		candidate, err := releaseartifact.NewContract(strings.TrimPrefix(entry.TagName, "v"))
		if err != nil || candidate.Tag != entry.TagName {
			continue
		}
		candidateIsPrerelease := candidate.Channel == releaseartifact.ChannelBeta
		if entry.Prerelease != candidateIsPrerelease {
			return "", fmt.Errorf(
				"published release %s prerelease status does not match its version",
				entry.TagName,
			)
		}
		if semver.Compare(candidate.Tag, current.Tag) >= 0 {
			continue
		}

		switch candidate.Channel {
		case releaseartifact.ChannelStable:
			stableTag = laterTag(stableTag, candidate.Tag)
		case releaseartifact.ChannelBeta:
			if current.Channel == releaseartifact.ChannelBeta &&
				candidate.ShortVersion == current.ShortVersion {
				betaTag = laterTag(betaTag, candidate.Tag)
			}
		}
	}
	if current.Channel == releaseartifact.ChannelBeta && betaTag != "" {
		return betaTag, nil
	}
	return stableTag, nil
}

func laterTag(current, candidate string) string {
	if current == "" || semver.Compare(candidate, current) > 0 {
		return candidate
	}
	return current
}
