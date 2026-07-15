package quota

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	officialBundleID = "com.google.antigravity"
	// The Team ID is part of the current official macOS signing identity.
	officialTeamID = "EQHXZ8M8AV"
)

var shortVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type installationResolver struct {
	candidates []string
	runner     commandRunner
	stat       func(string) (os.FileInfo, error)
}

func NewInstallationResolver() userAgentProvider {
	return &installationResolver{
		candidates: defaultBundleCandidates(), runner: osCommandRunner{}, stat: os.Stat,
	}
}

func (resolver *installationResolver) UserAgent(ctx context.Context) (string, error) {
	version, err := resolver.localVersion(ctx)
	if err != nil {
		return "", &Error{Kind: ErrorUnavailable}
	}
	return "vscode/1.X.X (Antigravity/" + version + ")", nil
}

func (resolver *installationResolver) localVersion(ctx context.Context) (string, error) {
	if resolver == nil || resolver.runner == nil || resolver.stat == nil {
		return "", errors.New("antigravity installation is unavailable")
	}
	for _, bundlePath := range resolver.candidates {
		info, err := resolver.stat(bundlePath)
		if err != nil || !info.IsDir() {
			continue
		}
		if version, err := resolver.inspectBundle(ctx, bundlePath); err == nil {
			return version, nil
		}
	}
	return "", errors.New("antigravity installation is unavailable")
}

func (resolver *installationResolver) inspectBundle(ctx context.Context, bundlePath string) (string, error) {
	if _, err := resolver.runner.Run(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundlePath); err != nil {
		return "", errors.New("antigravity signature is invalid")
	}
	signingInfo, err := resolver.runner.Run(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", bundlePath)
	if err != nil {
		return "", errors.New("antigravity signature is unavailable")
	}
	identifier, teamID := signingIdentity(string(signingInfo))
	if identifier != officialBundleID || teamID != officialTeamID {
		return "", errors.New("antigravity signer is invalid")
	}
	infoPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	bundleID, err := resolver.plistValue(ctx, infoPath, "CFBundleIdentifier")
	if err != nil || bundleID != officialBundleID {
		return "", errors.New("antigravity bundle identifier is invalid")
	}
	version, err := resolver.plistValue(ctx, infoPath, "CFBundleShortVersionString")
	if err != nil || !shortVersionPattern.MatchString(version) {
		return "", errors.New("antigravity version is invalid")
	}
	return version, nil
}

func (resolver *installationResolver) plistValue(ctx context.Context, infoPath, key string) (string, error) {
	raw, err := resolver.runner.Run(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", infoPath)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errors.New("property is empty")
	}
	return value, nil
}

func signingIdentity(raw string) (identifier, teamID string) {
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Identifier="):
			identifier = strings.TrimSpace(strings.TrimPrefix(line, "Identifier="))
		case strings.HasPrefix(line, "TeamIdentifier="):
			teamID = strings.TrimSpace(strings.TrimPrefix(line, "TeamIdentifier="))
		}
	}
	return identifier, teamID
}

func defaultBundleCandidates() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	result := []string{"/Applications/Antigravity.app"}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		result = append(result, filepath.Join(home, "Applications", "Antigravity.app"))
	}
	return result
}
