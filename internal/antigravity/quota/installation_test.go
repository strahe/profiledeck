package quota

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type fakeInstallationRunner struct {
	verifyErr   error
	signingInfo string
	bundleID    string
	version     string
	versionErr  error
	calls       []string
}

func (runner *fakeInstallationRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, name+" "+strings.Join(args, " "))
	if name == "/usr/bin/codesign" && len(args) > 0 && args[0] == "--verify" {
		return nil, runner.verifyErr
	}
	if name == "/usr/bin/codesign" {
		return []byte(runner.signingInfo), nil
	}
	if len(args) > 1 && args[1] == "CFBundleIdentifier" {
		return []byte(runner.bundleID), nil
	}
	if runner.versionErr != nil {
		return nil, runner.versionErr
	}
	return []byte(runner.version), nil
}

func TestInstallationResolverUsesVerifiedLocalBundleVersion(t *testing.T) {
	bundlePath := t.TempDir() + "/Antigravity.app"
	if err := os.Mkdir(bundlePath, 0o755); err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	runner := &fakeInstallationRunner{
		signingInfo: "Executable=/Applications/Antigravity.app/Contents/MacOS/Electron\nIdentifier=com.google.antigravity\nTeamIdentifier=EQHXZ8M8AV\n",
		bundleID:    officialBundleID,
		version:     "2.2.1",
	}
	resolver := &installationResolver{candidates: []string{bundlePath}, runner: runner, stat: os.Stat}
	userAgent, err := resolver.UserAgent(context.Background())
	if err != nil {
		t.Fatalf("UserAgent: %v", err)
	}
	if userAgent != "vscode/1.X.X (Antigravity/2.2.1)" {
		t.Fatalf("unexpected user agent %q", userAgent)
	}
	wantCalls := []string{
		"/usr/bin/codesign --verify --deep --strict " + bundlePath,
		"/usr/bin/codesign -dv --verbose=4 " + bundlePath,
		"/usr/bin/plutil -extract CFBundleIdentifier raw -o - " + bundlePath + "/Contents/Info.plist",
		"/usr/bin/plutil -extract CFBundleShortVersionString raw -o - " + bundlePath + "/Contents/Info.plist",
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("unexpected verification commands:\n got %#v\nwant %#v", runner.calls, wantCalls)
	}
}

func TestInstallationResolverRejectsUnverifiedOrInvalidBundles(t *testing.T) {
	tests := []struct {
		name   string
		runner fakeInstallationRunner
	}{
		{
			name: "signature failure",
			runner: fakeInstallationRunner{
				verifyErr: errors.New("signature rejected"), signingInfo: "Identifier=com.google.antigravity\nTeamIdentifier=EQHXZ8M8AV",
				bundleID: officialBundleID, version: "2.2.1",
			},
		},
		{
			name: "wrong signed identifier",
			runner: fakeInstallationRunner{
				signingInfo: "Identifier=example.antigravity\nTeamIdentifier=EQHXZ8M8AV", bundleID: officialBundleID, version: "2.2.1",
			},
		},
		{
			name: "wrong signer",
			runner: fakeInstallationRunner{
				signingInfo: "Identifier=com.google.antigravity\nTeamIdentifier=NOTGOOGLE", bundleID: officialBundleID, version: "2.2.1",
			},
		},
		{
			name: "wrong bundle id",
			runner: fakeInstallationRunner{
				signingInfo: "Identifier=com.google.antigravity\nTeamIdentifier=EQHXZ8M8AV", bundleID: "example.antigravity", version: "2.2.1",
			},
		},
		{
			name: "missing version",
			runner: fakeInstallationRunner{
				signingInfo: "Identifier=com.google.antigravity\nTeamIdentifier=EQHXZ8M8AV", bundleID: officialBundleID,
				versionErr: errors.New("missing"),
			},
		},
		{
			name: "invalid version",
			runner: fakeInstallationRunner{
				signingInfo: "Identifier=com.google.antigravity\nTeamIdentifier=EQHXZ8M8AV", bundleID: officialBundleID, version: "2.2.1 ProfileDeck",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundlePath := t.TempDir() + "/Antigravity.app"
			if err := os.Mkdir(bundlePath, 0o755); err != nil {
				t.Fatalf("create bundle: %v", err)
			}
			resolver := &installationResolver{candidates: []string{bundlePath}, runner: &test.runner, stat: os.Stat}
			userAgent, err := resolver.UserAgent(context.Background())
			if err == nil || userAgent != "" {
				t.Fatalf("expected unavailable result, userAgent=%q err=%v", userAgent, err)
			}
			if KindOf(err) != ErrorUnavailable || strings.Contains(err.Error(), "ProfileDeck") {
				t.Fatalf("unexpected public error %q", err)
			}
		})
	}
}

func TestInstallationResolverDoesNotInventVersionWhenBundleIsMissing(t *testing.T) {
	runner := &fakeInstallationRunner{version: "99.99.99"}
	resolver := &installationResolver{candidates: []string{t.TempDir() + "/missing.app"}, runner: runner, stat: os.Stat}
	if userAgent, err := resolver.UserAgent(context.Background()); err == nil || userAgent != "" || len(runner.calls) != 0 {
		t.Fatalf("expected missing bundle to be unavailable without inspection, userAgent=%q err=%v calls=%#v", userAgent, err, runner.calls)
	}
}
