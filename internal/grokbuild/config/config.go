package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	ProviderID     = "grok-build"
	AdapterID      = "grok-build"
	PresetName     = "grok-build"
	PresetVersion  = 1
	ConfigTargetID = "config"
	AuthTargetID   = "auth"
	ConfigFileName = "config.toml"
	AuthFileName   = "auth.json"
	LockFileName   = "auth.json.lock"

	AuthOverrideWarning = "Grok Build settings may override the selected login"
)

type Home struct {
	Dir        string
	ConfigPath string
	AuthPath   string
	LockPath   string
}

type Snapshot struct {
	Content string
	Missing bool
}

func ResolveHome(explicit string) (Home, error) {
	raw := strings.TrimSpace(explicit)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("GROK_HOME"))
	}
	if raw == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Home{}, fmt.Errorf("resolve user home: %w", err)
		}
		raw = filepath.Join(userHome, ".grok")
	}
	dir, err := filepath.Abs(raw)
	if err != nil {
		return Home{}, fmt.Errorf("resolve Grok Home: %w", err)
	}
	dir = filepath.Clean(dir)
	return Home{
		Dir:        dir,
		ConfigPath: filepath.Join(dir, ConfigFileName),
		AuthPath:   filepath.Join(dir, AuthFileName),
		LockPath:   filepath.Join(dir, LockFileName),
	}, nil
}

func UnsupportedAuthEnvironment() bool {
	return os.Getenv("GROK_AUTH") != "" || os.Getenv("GROK_AUTH_PATH") != ""
}

func ValidateTOML(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value map[string]any
	if err := toml.Unmarshal([]byte(raw), &value); err != nil {
		return err
	}
	return nil
}

// HasAuthOverride reports only the presence of settings that can change which
// login Grok uses. It never returns source keys or values.
func HasAuthOverride(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var value map[string]any
	if toml.Unmarshal([]byte(raw), &value) != nil {
		return false
	}
	return containsAuthOverride(value)
}

func containsAuthOverride(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
			switch normalized {
			case "api_key", "env_key", "auth_provider", "preferred_auth_method":
				return true
			}
			if containsAuthOverride(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsAuthOverride(nested) {
				return true
			}
		}
	}
	return false
}

func ReadSnapshot(path string) (Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Missing: true}, nil
		}
		return Snapshot{}, fmt.Errorf("read Grok config: %w", err)
	}
	if len(raw) > targetfs.MaxFileBytes {
		return Snapshot{}, errors.New("grok config is too large")
	}
	content := string(raw)
	if err := ValidateTOML(content); err != nil {
		return Snapshot{}, fmt.Errorf("grok config TOML is invalid: %w", err)
	}
	return Snapshot{Content: content}, nil
}
