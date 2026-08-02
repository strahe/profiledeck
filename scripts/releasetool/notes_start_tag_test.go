package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNotesStartTagSelectsTheReleaseLineBaseline(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		history string
		want    string
	}{
		{
			name:    "stable uses the highest earlier stable",
			version: "0.2.0",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.2.0-beta.2","prerelease":true}`,
				`{"tag_name":"v0.1.0","prerelease":false}`,
				`{"tag_name":"v0.1.1","prerelease":false}`,
				`{"tag_name":"v0.3.0","prerelease":false}`,
			),
			want: "v0.1.1",
		},
		{
			name:    "first beta uses the highest earlier stable",
			version: "0.2.0-beta.1",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.1.0-beta.7","prerelease":true}`,
				`{"tag_name":"v0.1.0","prerelease":false}`,
			),
			want: "v0.1.0",
		},
		{
			name:    "later beta uses the highest beta in the same release line",
			version: "0.2.0-beta.3",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.1.0-beta.7","prerelease":true}`,
				`{"tag_name":"v0.1.0","prerelease":false}`,
				`{"tag_name":"v0.2.0-beta.1","prerelease":true}`,
				`{"tag_name":"v0.2.0-beta.2","prerelease":true}`,
			),
			want: "v0.2.0-beta.2",
		},
		{
			name:    "beta numbering may skip",
			version: "0.2.0-beta.4",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.1.0","prerelease":false}`,
				`{"tag_name":"v0.2.0-beta.1","prerelease":true}`,
			),
			want: "v0.2.0-beta.1",
		},
		{
			name:    "draft and unrelated releases do not participate",
			version: "0.2.0-beta.2",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.2.0-beta.1","draft":true,"prerelease":false}`,
				`{"tag_name":"v0.1.0","prerelease":false}`,
				`{"tag_name":"nightly","prerelease":true}`,
				`{"tag_name":"v0.2.0-rc.1","prerelease":true}`,
			),
			want: "v0.1.0",
		},
		{
			name:    "stable without an earlier stable has no explicit baseline",
			version: "0.1.0",
			history: releaseHistoryJSON(
				`{"tag_name":"v0.1.0-beta.7","prerelease":true}`,
			),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			err := runWithInput(
				[]string{"notes-start-tag", "--version", test.version},
				strings.NewReader(test.history),
				&output,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(output.String()); got != test.want {
				t.Fatalf("notes start tag = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNotesStartTagRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		version string
		history string
	}{
		{
			name:    "invalid current version",
			version: "0.2",
		},
		{
			name:    "malformed history",
			version: "0.2.0",
			history: "{",
		},
		{
			name:    "stable version marked prerelease",
			version: "0.2.0",
			history: `{"tag_name":"v0.1.0","prerelease":true}`,
		},
		{
			name:    "beta version not marked prerelease",
			version: "0.2.0-beta.2",
			history: `{"tag_name":"v0.2.0-beta.1","prerelease":false}`,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := runWithInput(
				[]string{"notes-start-tag", "--version", test.version},
				strings.NewReader(test.history),
				&bytes.Buffer{},
			); err == nil {
				t.Fatal("invalid release history was accepted")
			}
		})
	}
}

func releaseHistoryJSON(entries ...string) string {
	return strings.Join(entries, "\n")
}
