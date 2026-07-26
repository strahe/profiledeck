package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseWorkflowKeepsTagAndManualEntrypoints(t *testing.T) {
	t.Parallel()
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := os.ReadFile(filepath.Join(
		packageDirectory,
		"..",
		"..",
		".github",
		"workflows",
		"release.yml",
	))
	if err != nil {
		t.Fatal(err)
	}
	content := string(workflow)
	for name, required := range map[string]string{
		"tag trigger":      "  push:\n    tags:\n      - 'v*'\n",
		"manual input":     "  workflow_dispatch:\n    inputs:\n      version:\n",
		"main restriction": `refs/heads/main`,
		"manual handling":  `"$GITHUB_EVENT_NAME" == "workflow_dispatch"`,
		"exact source":     "          ref: ${{ github.sha }}\n",
		"exact tag":        "      - name: Create exact manual release tag\n",
		"Linux runner": "  build-linux:\n    needs: validate\n" +
			"    runs-on: ${{ vars.RUNS_ON || 'ubuntu-24.04' }}\n",
		"macOS runner": "  build-macos:\n    needs: validate\n" +
			"    runs-on: ${{ vars.MACOS_RUNS_ON || 'macos-26' }}\n",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("release workflow is missing %s", name)
		}
	}
	// draft runs ci-release-finalize, which go-installs wails3 with CGO.
	// bare ubuntu-latest lacks GTK4/WebKitGTK; build-linux already installs them.
	draftIndex := strings.Index(content, "\n  draft:\n")
	if draftIndex < 0 {
		t.Fatal("release workflow is missing the draft job")
	}
	draftSection := content[draftIndex:]
	if !strings.Contains(draftSection, "libgtk-4-dev libwebkitgtk-6.0-dev") {
		t.Error("draft job must install libgtk-4-dev and libwebkitgtk-6.0-dev before ci-release-finalize")
	}
}

func TestWorkflowRunnerVariablesStayPlatformScoped(t *testing.T) {
	t.Parallel()
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workflowDirectory := filepath.Join(packageDirectory, "..", "..", ".github", "workflows")
	entries, err := os.ReadDir(workflowDirectory)
	if err != nil {
		t.Fatal(err)
	}
	approved := map[string]bool{
		"RUNS_ON":         true,
		"MACOS_RUNS_ON":   true,
		"WINDOWS_RUNS_ON": true,
	}
	variablePattern := regexp.MustCompile(`vars\.([A-Z][A-Z0-9_]*)`)
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || (extension != ".yml" && extension != ".yaml") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(workflowDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for lineNumber, line := range strings.Split(string(content), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "runs-on:") {
				continue
			}
			matches := variablePattern.FindAllStringSubmatch(line, -1)
			if len(matches) != 1 || !approved[matches[0][1]] {
				t.Errorf(
					"%s:%d must select its runner with RUNS_ON, MACOS_RUNS_ON, or WINDOWS_RUNS_ON",
					entry.Name(),
					lineNumber+1,
				)
			}
		}
	}
}
