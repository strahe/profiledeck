package main

import (
	"os"
	"path/filepath"
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
	} {
		if !strings.Contains(content, required) {
			t.Errorf("release workflow is missing %s", name)
		}
	}
}
