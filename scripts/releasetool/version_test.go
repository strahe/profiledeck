package main

import "testing"

func TestParseReleaseVersion(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"0.1.0",
		"1.2.3",
		"1.2.3-beta.1",
		"10.20.30-beta.42",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			version, err := parseReleaseVersion(value)
			if err != nil {
				t.Fatalf("parseReleaseVersion(%q): %v", value, err)
			}
			if version.String() != value {
				t.Fatalf("version.String() = %q, want %q", version, value)
			}
		})
	}
	for _, value := range []string{
		"",
		"dev",
		"v1.2.3",
		"1.2",
		"1.2.3-alpha.1",
		"1.2.3-beta.0",
		"01.2.3",
		"1.02.3",
		"1.2.03",
	} {
		if _, err := parseReleaseVersion(value); err == nil {
			t.Fatalf("parseReleaseVersion(%q) succeeded, want error", value)
		}
	}
}

func TestReleaseVersionOrdering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		candidate string
		existing  string
		want      int
	}{
		{candidate: "1.0.0-beta.2", existing: "1.0.0-beta.1", want: 1},
		{candidate: "1.0.0", existing: "1.0.0-beta.9", want: 1},
		{candidate: "1.0.1-beta.1", existing: "1.0.0", want: 1},
		{candidate: "1.0.0-beta.1", existing: "1.0.0", want: -1},
		{candidate: "1.0.0", existing: "1.0.0", want: 0},
	}
	for _, test := range tests {
		candidate, _ := parseReleaseVersion(test.candidate)
		existing, _ := parseReleaseVersion(test.existing)
		if got := candidate.compare(existing); got != test.want {
			t.Fatalf("%s.compare(%s) = %d, want %d", candidate, existing, got, test.want)
		}
	}
}

func TestParseBuildNumber(t *testing.T) {
	t.Parallel()
	if number, err := parseBuildNumber("42"); err != nil || number != 42 {
		t.Fatalf("parseBuildNumber(42) = %d, %v", number, err)
	}
	for _, value := range []string{"", "0", "-1", "01", "1.0"} {
		if _, err := parseBuildNumber(value); err == nil {
			t.Fatalf("parseBuildNumber(%q) succeeded, want error", value)
		}
	}
}
