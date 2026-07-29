package backend

import (
	"os"
	"strings"
)

const (
	envConfigDir = "PROFILEDECK_CONFIG_DIR"
	envCodexDir  = "PROFILEDECK_CODEX_DIR"
	envGrokHome  = "GROK_HOME"
)

type Environment struct {
	ConfigDir string `json:"config_dir"`
	CodexDir  string `json:"codex_dir"`
	GrokHome  string `json:"grok_home"`
}

func NewEnvironmentFromEnv() Environment {
	return Environment{
		ConfigDir: strings.TrimSpace(os.Getenv(envConfigDir)),
		CodexDir:  strings.TrimSpace(os.Getenv(envCodexDir)),
		GrokHome:  strings.TrimSpace(os.Getenv(envGrokHome)),
	}
}
