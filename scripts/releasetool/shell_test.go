package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSigningIdentityScriptSelectsAndProtectsOutput(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "security"), `#!/bin/bash
if [[ "${FAKE_SECURITY_FAIL-}" == "true" ]]; then
  echo "TOKEN=identity-secret" >&2
  exit 1
fi
if [[ "${FAKE_SECURITY_SINGLE-}" == "true" ]]; then
  echo '  1) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example One (AAAAAAAAAA)"'
  exit 0
fi
cat <<'EOF'
  1) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example One (AAAAAAAAAA)"
  2) BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB "Developer ID Application: Example Two (BBBBBBBBBB)"
     2 valid identities found
EOF
`)

	result := runTestScript(t, repo, bin, nil,
		"scripts/release/resolve-signing-identity.sh",
		"--requested", "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		"--output", "name",
	)
	if result.err != nil {
		t.Fatalf("resolve identity: %v: %s", result.err, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "Developer ID Application: Example Two (BBBBBBBBBB)" {
		t.Fatalf("identity output = %q", result.stdout)
	}

	result = runTestScript(t, repo, bin, []string{"FAKE_SECURITY_SINGLE=true"},
		"scripts/release/resolve-signing-identity.sh")
	if result.err != nil || strings.TrimSpace(result.stdout) != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("single identity selection: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}

	result = runTestScript(t, repo, bin, nil, "scripts/release/resolve-signing-identity.sh")
	if result.err == nil || !strings.Contains(result.stderr, "Expected one Developer ID Application identity") {
		t.Fatalf("multiple identities were accepted without a selection: err=%v stderr=%q", result.err, result.stderr)
	}

	result = runTestScript(t, repo, bin, []string{"FAKE_SECURITY_FAIL=true"},
		"scripts/release/resolve-signing-identity.sh",
		"--requested", "identity-secret",
	)
	if result.err == nil || strings.Contains(result.stderr, "identity-secret") {
		t.Fatalf("unsafe identity failure: err=%v stderr=%q", result.err, result.stderr)
	}
}

func TestNotarizeScriptAcceptsOnlyAcceptedStatusWithoutLeakingRawOutput(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "xcrun"), `#!/bin/bash
printf '%s\n' "${FAKE_NOTARY_PAYLOAD-}"
exit "${FAKE_NOTARY_EXIT-0}"
`)
	writeTestExecutable(t, filepath.Join(bin, "plutil"), `#!/bin/bash
printf '%s\n' "${FAKE_NOTARY_STATUS-Invalid}"
`)

	result := runTestScript(t, repo, bin, []string{"FAKE_NOTARY_STATUS=Accepted", "FAKE_NOTARY_PAYLOAD={\"status\":\"Accepted\"}"},
		"scripts/release/notarize-macos.sh", "submit", "--input", "ProfileDeck.dmg", "--profile", "apple-notary")
	if result.err != nil || !strings.Contains(result.stdout, "Notarization accepted") {
		t.Fatalf("accepted notarization: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}

	result = runTestScript(t, repo, bin, []string{
		"FAKE_NOTARY_STATUS=Invalid",
		"FAKE_NOTARY_PAYLOAD=Developer ID Application: Private Name; TOKEN=notary-secret; /private/path",
	}, "scripts/release/notarize-macos.sh", "submit", "--input", "ProfileDeck.dmg", "--profile", "apple-notary")
	if result.err == nil || strings.Contains(result.stderr, "notary-secret") || strings.Contains(result.stderr, "Private Name") {
		t.Fatalf("unsafe notarization failure: err=%v stderr=%q", result.err, result.stderr)
	}

	result = runTestScript(t, repo, bin, []string{
		"FAKE_NOTARY_EXIT=1",
		"FAKE_NOTARY_PAYLOAD=TOKEN=notary-command-secret; /private/path",
	}, "scripts/release/notarize-macos.sh", "submit", "--input", "ProfileDeck.dmg", "--profile", "apple-notary")
	if result.err == nil || strings.Contains(result.stderr, "notary-command-secret") || strings.Contains(result.stderr, "/private/path") {
		t.Fatalf("unsafe notarization command failure: err=%v stderr=%q", result.err, result.stderr)
	}
}

func TestPlistPlaceholderCheckRejectsIncompleteMetadata(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	valid := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(valid, []byte("<string>1.2.3</string>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runTestScript(t, repo, t.TempDir(), nil,
		"scripts/release/check-plist-placeholders.sh", valid)
	if result.err != nil {
		t.Fatalf("complete metadata was rejected: %v: %s", result.err, result.stderr)
	}

	incomplete := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(incomplete, []byte("<string>@SHORT_VERSION@</string>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = runTestScript(t, repo, t.TempDir(), nil,
		"scripts/release/check-plist-placeholders.sh", incomplete)
	if result.err == nil || !strings.Contains(result.stderr, "application metadata is incomplete") || strings.Contains(result.stderr, incomplete) {
		t.Fatalf("incomplete metadata result: err=%v stderr=%q", result.err, result.stderr)
	}
}

func TestMacOSVerifyScriptHidesCommandDiagnostics(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "go"), `#!/bin/bash
echo "Developer ID Application: Private Name; TOKEN=verify-secret; /private/path" >&2
exit 1
`)
	result := runTestScript(t, repo, bin, nil,
		"scripts/release/verify-macos.sh",
		"--directory", t.TempDir(), "--version", "1.2.3", "--short-version", "1.2.3",
		"--build-number", "1", "--product", "ProfileDeck", "--binary", "profiledeck-desktop",
		"--bundle-id", "io.github.strahe.profiledeck", "--min-version", "14.0", "--public-key", "public-key",
		"--updater", "update.zip", "--signature", "update.zip.sig", "--installer", "installer.dmg",
	)
	if result.err == nil || strings.Contains(result.stderr, "verify-secret") || strings.Contains(result.stderr, "Private Name") {
		t.Fatalf("unsafe verification failure: err=%v stderr=%q", result.err, result.stderr)
	}
}

func TestMacOSVerifyScriptRejectsCriticalFailuresAndCleansUp(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	writeFakeMacVerificationTools(t, bin)

	for _, test := range []struct {
		name    string
		stage   string
		message string
	}{
		{name: "ZIP layout", stage: "zip-layout", message: "must contain one application"},
		{name: "application architecture", stage: "app-architecture", message: "not a Universal macOS build"},
		{name: "DMG integrity", stage: "dmg-integrity", message: "installer image is damaged"},
		{name: "DMG mount", stage: "dmg-mount", message: "installer image could not be mounted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			assets := t.TempDir()
			diskutilMarker := filepath.Join(t.TempDir(), "diskutil")
			for _, name := range []string{"update.zip", "update.zip.sig", "installer.dmg"} {
				if err := os.WriteFile(filepath.Join(assets, name), []byte("test artifact"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result := runTestScript(t, repo, bin, []string{
				"TMPDIR=" + tempRoot,
				"FAKE_MAC_FAILURE=" + test.stage,
				"FAKE_MAC_DISKUTIL_MARKER=" + diskutilMarker,
			}, "scripts/release/verify-macos.sh",
				"--directory", assets, "--version", "1.2.3", "--short-version", "1.2.3",
				"--build-number", "1", "--product", "ProfileDeck", "--binary", "profiledeck-desktop",
				"--bundle-id", "io.github.strahe.profiledeck", "--min-version", "14.0", "--public-key", "public-key",
				"--updater", "update.zip", "--signature", "update.zip.sig", "--installer", "installer.dmg",
			)
			if result.err == nil || !strings.Contains(result.stderr, test.message) || strings.Contains(result.stderr, "mac-verify-secret") {
				t.Fatalf("critical failure result: err=%v stderr=%q", result.err, result.stderr)
			}
			entries, err := os.ReadDir(tempRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("verification temporary files were not removed: %#v", entries)
			}
			if test.stage == "dmg-mount" {
				if _, err := os.Stat(diskutilMarker + ".eject"); err != nil {
					t.Fatalf("failed mount was not followed by an eject attempt: %v", err)
				}
			}
		})
	}
}

func TestGitHubScriptCreatesResumesAndVerifiesDraftWithoutClobber(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	state := t.TempDir()
	logPath := filepath.Join(state, "gh.log")
	writeFakeGitHubCLI(t, filepath.Join(bin, "gh"))
	version, _ := parseReleaseVersion("1.2.3-beta.1")
	root := t.TempDir()
	handoff := createTestHandoff(t, root, macOSPlatform, version, 31, testReleaseCommit)
	inputs, _ := parsePlatformInputs([]string{macOSPlatform + "=" + handoff}, version)
	bundle := filepath.Join(root, "bundle")
	if err := assembleRelease(bundle, version, 31, testReleaseCommit, inputs); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"FAKE_GH_STATE=" + state,
		"FAKE_GH_LOG=" + logPath,
		"FAKE_RELEASE_COMMIT=" + testReleaseCommit,
		"GOCACHE=" + filepath.Join(t.TempDir(), "go-cache"),
	}
	result := runTestScript(t, repo, bin, env,
		"scripts/release/github-release.sh", "draft",
		"--version", version.String(), "--build-number", "31", "--repo", "strahe/profiledeck",
		"--commit", testReleaseCommit, "--platforms", macOSPlatform, "--bundle", bundle,
	)
	if result.err != nil || !strings.Contains(result.stdout, "Draft Release is ready for review") {
		t.Fatalf("create draft: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}
	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logContent)
	if strings.Contains(logText, "--clobber") || !strings.Contains(logText, "--prerelease --latest=false") {
		t.Fatalf("unexpected GitHub commands: %s", logText)
	}
	assets, err := os.ReadDir(filepath.Join(state, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 4 {
		t.Fatalf("uploaded asset count = %d, want 4", len(assets))
	}
	for _, asset := range assets[1:] {
		if err := os.Remove(filepath.Join(state, "assets", asset.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result = runTestScript(t, repo, bin, env,
		"scripts/release/github-release.sh", "draft",
		"--version", version.String(), "--build-number", "31", "--repo", "strahe/profiledeck",
		"--commit", testReleaseCommit, "--platforms", macOSPlatform, "--bundle", bundle,
	)
	if result.err != nil {
		t.Fatalf("resume partial draft: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}
	logContent, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if uploads := strings.Count(string(logContent), "release upload"); uploads != 3 {
		t.Fatalf("partial resume upload count = %d, want 3: %s", uploads, logContent)
	}

	firstAsset := filepath.Join(state, "assets", assets[0].Name())
	if err := os.WriteFile(firstAsset, []byte("changed remotely"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = runTestScript(t, repo, bin, env,
		"scripts/release/github-release.sh", "draft",
		"--version", version.String(), "--build-number", "31", "--repo", "strahe/profiledeck",
		"--commit", testReleaseCommit, "--platforms", macOSPlatform, "--bundle", bundle,
	)
	if result.err == nil || !strings.Contains(result.stderr, "differs from this release") {
		t.Fatalf("changed Draft asset was accepted: err=%v stderr=%q", result.err, result.stderr)
	}
}

func TestGitHubScriptRejectsUnsafeRemoteState(t *testing.T) {
	requireBash(t)
	repo := testRepositoryRoot(t)
	bin := t.TempDir()
	writeFakeGitHubCLI(t, filepath.Join(bin, "gh"))
	version, _ := parseReleaseVersion("1.2.3-beta.1")
	root := t.TempDir()
	handoff := createTestHandoff(t, root, macOSPlatform, version, 31, testReleaseCommit)
	inputs, _ := parsePlatformInputs([]string{macOSPlatform + "=" + handoff}, version)
	bundle := filepath.Join(root, "bundle")
	if err := assembleRelease(bundle, version, 31, testReleaseCommit, inputs); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		message string
		prepare func(t *testing.T, state string)
		extra   []string
	}{
		{
			name:    "moved tag",
			message: "tag points to another commit",
			prepare: func(t *testing.T, state string) {
				writeTestStateFile(t, state, "tag", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
			},
		},
		{
			name:    "unexpected asset",
			message: "unexpected asset",
			prepare: func(t *testing.T, state string) {
				writeTestDraftState(t, state, version.tag())
				writeTestStateFile(t, filepath.Join(state, "assets"), "unexpected.txt", "unexpected\n")
			},
		},
		{
			name:    "published release",
			message: "published or incompatible Release",
			prepare: func(t *testing.T, state string) {
				writeTestDraftState(t, state, version.tag())
				writeTestStateFile(t, state, "published-release", "true\n")
			},
		},
		{
			name:    "final download mismatch",
			message: "downloaded Draft asset does not match",
			prepare: func(*testing.T, string) {},
			extra:   []string{"FAKE_GH_TAMPER_DOWNLOAD=true"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			test.prepare(t, state)
			env := []string{
				"FAKE_GH_STATE=" + state,
				"FAKE_GH_LOG=" + filepath.Join(state, "gh.log"),
				"FAKE_RELEASE_COMMIT=" + testReleaseCommit,
				"GOCACHE=" + filepath.Join(t.TempDir(), "go-cache"),
			}
			env = append(env, test.extra...)
			result := runTestScript(t, repo, bin, env,
				"scripts/release/github-release.sh", "draft",
				"--version", version.String(), "--build-number", "31", "--repo", "strahe/profiledeck",
				"--commit", testReleaseCommit, "--platforms", macOSPlatform, "--bundle", bundle,
			)
			if result.err == nil || !strings.Contains(result.stderr, test.message) {
				t.Fatalf("unsafe remote state was accepted: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
			}
		})
	}
}

func writeTestDraftState(t *testing.T, state, tag string) {
	t.Helper()
	writeTestStateFile(t, state, "tag", testReleaseCommit+"\n")
	writeTestStateFile(t, state, "release", tag+"\n")
	writeTestStateFile(t, state, "prerelease", "true\n")
}

func writeTestStateFile(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFakeGitHubCLI(t *testing.T, path string) {
	t.Helper()
	writeTestExecutable(t, path, `#!/bin/bash
set -euo pipefail
state="$FAKE_GH_STATE"
mkdir -p "$state/assets"
printf '%s\n' "$*" >>"$FAKE_GH_LOG"
if [[ "$1 $2" == "auth status" ]]; then exit 0; fi
if [[ "$1" == "api" ]]; then
  endpoint="$2"
  case "$endpoint" in
    repos/*/commits/*) exit 0 ;;
    repos/*/git/ref/tags/*)
      if [[ -f "$state/tag" ]]; then printf 'commit %s\n' "$(cat "$state/tag")"; exit 0; fi
      echo "HTTP 404" >&2; exit 1 ;;
    repos/*/git/refs)
      sha=""
      for value in "$@"; do case "$value" in sha=*) sha="${value#sha=}" ;; esac; done
      printf '%s\n' "$sha" >"$state/tag"; exit 0 ;;
  esac
fi
if [[ "$1 $2" == "release list" ]]; then
  for value in "$@"; do
    if [[ "$value" == "--exclude-drafts" ]]; then
      [[ -f "$state/published" ]] && cat "$state/published"
      exit 0
    fi
  done
  [[ -f "$state/release" ]] && cat "$state/release"
  exit 0
fi
if [[ "$1 $2" == "release view" ]]; then
  [[ -f "$state/release" ]] || exit 1
  for value in "$@"; do
    if [[ "$value" == "assets" ]]; then
      for asset in "$state/assets"/*; do
        if [[ -f "$asset" ]]; then basename "$asset"; fi
      done | sort
      exit 0
    fi
  done
  draft=true
  [[ -f "$state/published-release" ]] && draft=false
  prerelease=false
  [[ -f "$state/prerelease" ]] && prerelease=true
  printf '%s\t%s\t%s\thttps://example.invalid/draft\n' "$draft" "$prerelease" "$(cat "$state/release")"
  exit 0
fi
if [[ "$1 $2" == "release create" ]]; then
  printf '%s\n' "$3" >"$state/release"
  for value in "$@"; do [[ "$value" == "--prerelease" ]] && : >"$state/prerelease"; done
  exit 0
fi
if [[ "$1 $2" == "release upload" ]]; then
  cp "$4" "$state/assets/$(basename "$4")"
  exit 0
fi
if [[ "$1 $2" == "release download" ]]; then
  destination=""
  previous=""
  for value in "$@"; do
    [[ "$previous" == "--dir" ]] && destination="$value"
    previous="$value"
  done
  for asset in "$state/assets"/*; do [[ -f "$asset" ]] && cp "$asset" "$destination/"; done
  if [[ "${FAKE_GH_TAMPER_DOWNLOAD-}" == "true" ]]; then
    for asset in "$destination"/*; do
      if [[ -f "$asset" ]]; then printf 'tampered\n' >>"$asset"; break; fi
    done
  fi
  exit 0
fi
echo "unsupported gh command: $*" >&2
exit 1
`)
}

func writeFakeMacVerificationTools(t *testing.T, directory string) {
	t.Helper()
	writeTestExecutable(t, filepath.Join(directory, "go"), "#!/bin/bash\nexit 0\n")
	writeTestExecutable(t, filepath.Join(directory, "ditto"), `#!/bin/bash
set -euo pipefail
destination="${@: -1}"
app="$destination/ProfileDeck.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
printf '#!/bin/bash\n' >"$app/Contents/MacOS/profiledeck-desktop"
chmod 755 "$app/Contents/MacOS/profiledeck-desktop"
printf 'plist\n' >"$app/Contents/Info.plist"
printf 'assets\n' >"$app/Contents/Resources/Assets.car"
printf 'icons\n' >"$app/Contents/Resources/icons.icns"
if [[ "$FAKE_MAC_FAILURE" == "zip-layout" ]]; then
  printf 'extra\n' >"$destination/extra.txt"
fi
`)
	writeTestExecutable(t, filepath.Join(directory, "xcrun"), `#!/bin/bash
if [[ "$1" == "lipo" ]]; then
  if [[ "$FAKE_MAC_FAILURE" == "app-architecture" ]]; then echo arm64; else echo 'arm64 x86_64'; fi
  exit 0
fi
if [[ "$1 $2" == "stapler validate" ]]; then exit 0; fi
exit 1
`)
	writeTestExecutable(t, filepath.Join(directory, "plutil"), `#!/bin/bash
case "$2" in
  CFBundleIdentifier) echo io.github.strahe.profiledeck ;;
  CFBundleExecutable) echo profiledeck-desktop ;;
  CFBundleIconFile) echo icons ;;
  CFBundleIconName) echo appicon ;;
  CFBundleShortVersionString) echo 1.2.3 ;;
  CFBundleVersion) echo 1 ;;
  LSMinimumSystemVersion) echo 14.0 ;;
  CFBundleInfoDictionaryVersion) echo 6.0 ;;
  *) exit 1 ;;
esac
`)
	writeTestExecutable(t, filepath.Join(directory, "codesign"), `#!/bin/bash
if [[ " $* " == *" --display "* ]]; then
  echo 'Authority=Developer ID Application: Private Name' >&2
  echo 'flags=0x10000(runtime)' >&2
  echo 'Timestamp=Jul 22, 2026' >&2
fi
exit 0
`)
	writeTestExecutable(t, filepath.Join(directory, "spctl"), "#!/bin/bash\nexit 0\n")
	writeTestExecutable(t, filepath.Join(directory, "hdiutil"), `#!/bin/bash
if [[ "$FAKE_MAC_FAILURE" == "dmg-integrity" ]]; then
  echo 'TOKEN=mac-verify-secret; /private/path' >&2
  exit 1
fi
exit 0
`)
	writeTestExecutable(t, filepath.Join(directory, "diskutil"), `#!/bin/bash
if [[ "$1 $2" == "image attach" ]]; then
  touch "$FAKE_MAC_DISKUTIL_MARKER.attach"
  echo 'TOKEN=mac-verify-secret; /private/path' >&2
  exit 1
fi
if [[ "$1" == "eject" ]]; then
  touch "$FAKE_MAC_DISKUTIL_MARKER.eject"
  exit 0
fi
exit 1
`)
}

type scriptResult struct {
	stdout string
	stderr string
	err    error
}

func runTestScript(t *testing.T, repo, bin string, extraEnv []string, script string, args ...string) scriptResult {
	t.Helper()
	command := exec.Command("bash", append([]string{script}, args...)...)
	command.Dir = repo
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.Env = append(command.Env, extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return scriptResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "..", ".."))
}

func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release shell adapters require Bash")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
}
