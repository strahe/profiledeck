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
	ProviderID           = "codex"
	AdapterID            = "codex"
	TargetID             = "config"
	AuthTargetID         = "auth"
	PresetName           = "codex"
	PresetVersion        = 2
	DefaultModelProvider = "openai"
	ConfigFileName       = "config.toml"
	AuthFileName         = "auth.json"
)

type Home struct {
	Dir        string
	ConfigPath string
	AuthPath   string
}

type Snapshot struct {
	Content string
	Missing bool
}

func ResolveHome(explicit string) (Home, error) {
	raw := strings.TrimSpace(explicit)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Home{}, fmt.Errorf("resolve user home: %w", err)
		}
		raw = filepath.Join(home, ".codex")
	}

	dir, err := filepath.Abs(raw)
	if err != nil {
		return Home{}, fmt.Errorf("resolve Codex home: %w", err)
	}
	dir = filepath.Clean(dir)
	return Home{
		Dir:        dir,
		ConfigPath: filepath.Join(dir, ConfigFileName),
		AuthPath:   filepath.Join(dir, AuthFileName),
	}, nil
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

func ReadSnapshot(path string) (Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{Missing: true}, nil
		}
		return Snapshot{}, fmt.Errorf("read Codex config: %w", err)
	}
	if len(raw) > targetfs.MaxFileBytes {
		return Snapshot{}, errors.New("Codex config is too large")
	}
	content := string(raw)
	if err := ValidateTOML(content); err != nil {
		return Snapshot{}, fmt.Errorf("Codex config TOML is invalid: %w", err)
	}
	return Snapshot{Content: content}, nil
}
