package codexconfig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestResolveHomeOrder(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	envHome := filepath.Join(t.TempDir(), "env")
	userHome := t.TempDir()

	t.Setenv("CODEX_HOME", envHome)
	t.Setenv("HOME", userHome)

	got, err := ResolveHome(explicit)
	if err != nil {
		t.Fatalf("expected explicit home to resolve, got %v", err)
	}
	wantExplicit, err := filepath.Abs(explicit)
	if err != nil {
		t.Fatalf("expected abs explicit path, got %v", err)
	}
	if got.Dir != filepath.Clean(wantExplicit) {
		t.Fatalf("expected explicit dir %q, got %q", filepath.Clean(wantExplicit), got.Dir)
	}
	if got.ConfigPath != filepath.Join(got.Dir, ConfigFileName) {
		t.Fatalf("unexpected explicit config path: %#v", got)
	}
	if got.AuthPath != filepath.Join(got.Dir, AuthFileName) {
		t.Fatalf("unexpected explicit auth path: %#v", got)
	}

	got, err = ResolveHome("")
	if err != nil {
		t.Fatalf("expected CODEX_HOME to resolve, got %v", err)
	}
	wantEnv, err := filepath.Abs(envHome)
	if err != nil {
		t.Fatalf("expected abs env path, got %v", err)
	}
	if got.Dir != filepath.Clean(wantEnv) {
		t.Fatalf("expected CODEX_HOME dir %q, got %q", filepath.Clean(wantEnv), got.Dir)
	}

	t.Setenv("CODEX_HOME", "")
	got, err = ResolveHome("")
	if err != nil {
		t.Fatalf("expected fallback home to resolve, got %v", err)
	}
	if got.Dir != filepath.Join(userHome, ".codex") {
		t.Fatalf("expected fallback dir %q, got %q", filepath.Join(userHome, ".codex"), got.Dir)
	}
}

func TestNormalizeManagedValidatesBaseURL(t *testing.T) {
	for _, raw := range []string{
		"",
		"ftp://example.test",
		"https://user@example.test",
		"https://example.test/path?query=1",
		"https://example.test/path#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := NormalizeManaged("gpt-5.3-codex", "", &raw)
			if err == nil {
				t.Fatalf("expected base URL %q to be rejected", raw)
			}
		})
	}

	baseURL := "https://api.example.test/v1"
	got, err := NormalizeManaged(" gpt-5.3-codex ", "", &baseURL)
	if err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if got.Model != "gpt-5.3-codex" || got.ModelProvider != DefaultModelProvider || got.OpenAIBaseURL != baseURL {
		t.Fatalf("unexpected normalized config: %#v", got)
	}
}

func TestApplyManagedTOMLPreservesUnmanagedContentAndDeletesStaleBaseURL(t *testing.T) {
	existing := strings.Join([]string{
		`model = "old-model"`,
		`model_provider = "old-provider"`,
		`openai_base_url = "https://old.example.test/v1"`,
		`approval_policy = "never"`,
		``,
		`[tools]`,
		`web_search = true`,
	}, "\n")
	desired, err := NormalizeManaged("gpt-5.3-codex", "", nil)
	if err != nil {
		t.Fatalf("expected desired config to normalize, got %v", err)
	}

	content, err := ApplyManagedTOML(existing, true, desired)
	if err != nil {
		t.Fatalf("expected TOML update to succeed, got %v", err)
	}
	if !strings.HasSuffix(content, "\n") {
		t.Fatalf("expected output to end with newline, got %q", content)
	}
	if strings.Contains(content, "old.example.test") || strings.Contains(content, "openai_base_url") {
		t.Fatalf("expected stale openai_base_url to be removed, got %q", content)
	}

	var decoded map[string]any
	if err := toml.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("expected output TOML to parse, got %v", err)
	}
	if decoded["model"] != "gpt-5.3-codex" || decoded["model_provider"] != DefaultModelProvider {
		t.Fatalf("unexpected managed values: %#v", decoded)
	}
	if decoded["approval_policy"] != "never" {
		t.Fatalf("expected unmanaged top-level key to survive, got %#v", decoded)
	}
	tools, ok := decoded["tools"].(map[string]any)
	if !ok || tools["web_search"] != true {
		t.Fatalf("expected unmanaged section to survive, got %#v", decoded)
	}
}

func TestApplyManagedTOMLRejectsInvalidExistingTOML(t *testing.T) {
	desired, err := NormalizeManaged("gpt-5.3-codex", "", nil)
	if err != nil {
		t.Fatalf("expected desired config to normalize, got %v", err)
	}
	if _, err := ApplyManagedTOML(`model = "unterminated`, true, desired); err == nil {
		t.Fatalf("expected invalid existing TOML to fail")
	}
}
