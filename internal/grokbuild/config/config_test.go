package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHomePrecedence(t *testing.T) {
	envHome := filepath.Join(t.TempDir(), "from-env")
	t.Setenv("GROK_HOME", envHome)

	explicit := filepath.Join(t.TempDir(), "from-flag")
	home, err := ResolveHome(explicit)
	if err != nil {
		t.Fatalf("ResolveHome explicit: %v", err)
	}
	if home.Dir != explicit {
		t.Fatalf("explicit home = %q, want %q", home.Dir, explicit)
	}

	home, err = ResolveHome("")
	if err != nil {
		t.Fatalf("ResolveHome env: %v", err)
	}
	if home.Dir != envHome {
		t.Fatalf("environment home = %q, want %q", home.Dir, envHome)
	}
	if home.AuthPath != filepath.Join(envHome, AuthFileName) ||
		home.ConfigPath != filepath.Join(envHome, ConfigFileName) ||
		home.LockPath != filepath.Join(envHome, LockFileName) {
		t.Fatalf("resolved paths = %#v", home)
	}
}

func TestResolveHomeMakesRelativeInputAbsolute(t *testing.T) {
	t.Setenv("GROK_HOME", "")
	home, err := ResolveHome(filepath.Join("relative", "grok"))
	if err != nil {
		t.Fatalf("ResolveHome: %v", err)
	}
	if !filepath.IsAbs(home.Dir) {
		t.Fatalf("resolved home is not absolute: %q", home.Dir)
	}
}

func TestUnsupportedAuthEnvironment(t *testing.T) {
	t.Setenv("GROK_AUTH", "")
	t.Setenv("GROK_AUTH_PATH", "")
	if UnsupportedAuthEnvironment() {
		t.Fatal("empty environment was reported as unsupported")
	}
	t.Setenv("GROK_AUTH", "synthetic")
	if !UnsupportedAuthEnvironment() {
		t.Fatal("GROK_AUTH was not reported as unsupported")
	}
	t.Setenv("GROK_AUTH", "")
	t.Setenv("GROK_AUTH_PATH", filepath.Join(t.TempDir(), "auth.json"))
	if !UnsupportedAuthEnvironment() {
		t.Fatal("GROK_AUTH_PATH was not reported as unsupported")
	}
	t.Setenv("GROK_AUTH_PATH", " ")
	if !UnsupportedAuthEnvironment() {
		t.Fatal("a non-empty GROK_AUTH_PATH was not reported as unsupported")
	}
}

func TestConfigValidationAndAuthOverrideWarning(t *testing.T) {
	if err := ValidateTOML(""); err != nil {
		t.Fatalf("empty config: %v", err)
	}
	raw := "# preserved comment\n[models.default]\nname = \"grok-code\"\n"
	if err := ValidateTOML(raw); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if HasAuthOverride(raw) {
		t.Fatal("ordinary config reported an auth override")
	}
	for _, override := range []string{
		"[models.default]\napi_key = \"synthetic\"\n",
		"[models.default]\nenv-key = \"SYNTHETIC_KEY\"\n",
		"[[auth_provider]]\nname = \"synthetic\"\n",
	} {
		if !HasAuthOverride(override) {
			t.Fatalf("override was not detected: %q", override)
		}
	}
	if err := ValidateTOML("[broken"); err == nil {
		t.Fatal("invalid config unexpectedly succeeded")
	}
}

func TestReadSnapshotTreatsMissingConfigAsEmptySemanticState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot missing: %v", err)
	}
	if !snapshot.Missing || snapshot.Content != "" {
		t.Fatalf("missing snapshot = %#v", snapshot)
	}
	raw := "# exact\nmodel = \"grok-code\"\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snapshot, err = ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot existing: %v", err)
	}
	if snapshot.Missing || snapshot.Content != raw {
		t.Fatalf("existing snapshot = %#v", snapshot)
	}
}
