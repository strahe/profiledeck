package preset

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
)

const (
	ProviderName            = "Codex"
	SecretKindAuthJSON      = "codex-auth-json"
	TargetModeManagedKeys   = "managed-keys"
	TargetModeFullFile      = "full-file"
	AuthPreviewContent      = "[REDACTED_Codex_AUTH]"
	FileCredentialStoreHint = `Codex auth.json is required; set cli_auth_credentials_store = "file" in config.toml and run codex login again`
)

type ProviderMetadata struct {
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
	CodexDir      string `json:"codex_dir"`
	ConfigPath    string `json:"config_path"`
	AuthPath      string `json:"auth_path,omitempty"`
}

type TargetMetadata struct {
	Preset        string   `json:"preset"`
	PresetVersion int      `json:"preset_version"`
	TargetKind    string   `json:"target_kind"`
	Mode          string   `json:"mode,omitempty"`
	ManagedKeys   []string `json:"managed_keys"`
}

type TargetFormatStrategyNames struct {
	JSONFormat          string
	TOMLFormat          string
	TOMLMergeStrategy   string
	ReplaceFileStrategy string
}

func ProviderMetadataJSON(home codexconfig.Home) (string, error) {
	raw, err := json.Marshal(ProviderMetadata{
		Preset:        codexconfig.PresetName,
		PresetVersion: codexconfig.PresetVersion,
		CodexDir:      home.Dir,
		ConfigPath:    home.ConfigPath,
		AuthPath:      home.AuthPath,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func DecodeProviderMetadata(raw string) (ProviderMetadata, error) {
	var metadata ProviderMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return ProviderMetadata{}, err
	}
	return metadata, nil
}

func (metadata ProviderMetadata) Compatible() bool {
	return metadata.Preset == codexconfig.PresetName &&
		metadata.PresetVersion == codexconfig.PresetVersion &&
		metadata.CodexDir != "" &&
		metadata.ConfigPath != ""
}

func TargetMetadataJSON(targetKind string, mode string) (string, error) {
	metadata := TargetMetadata{
		Preset:        codexconfig.PresetName,
		PresetVersion: codexconfig.PresetVersion,
		TargetKind:    targetKind,
		Mode:          mode,
	}
	if targetKind == codexconfig.TargetID && mode == TargetModeManagedKeys {
		metadata.ManagedKeys = codexconfig.ManagedKeys()
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func DecodeTargetMetadata(raw string) (TargetMetadata, error) {
	var metadata TargetMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return TargetMetadata{}, err
	}
	return metadata, nil
}

func (metadata TargetMetadata) Compatible() bool {
	if metadata.Preset != codexconfig.PresetName || metadata.PresetVersion != codexconfig.PresetVersion {
		return false
	}
	switch metadata.TargetKind {
	case codexconfig.TargetID:
		switch metadata.ModeOrDefault() {
		case TargetModeManagedKeys:
			return sameStringSet(metadata.ManagedKeys, codexconfig.ManagedKeys())
		case TargetModeFullFile:
			return len(metadata.ManagedKeys) == 0
		default:
			return false
		}
	case codexconfig.AuthTargetID:
		return metadata.Mode == TargetModeFullFile && len(metadata.ManagedKeys) == 0
	default:
		return false
	}
}

func (metadata TargetMetadata) ModeOrDefault() string {
	if metadata.Mode == "" && metadata.TargetKind == codexconfig.TargetID {
		return TargetModeManagedKeys
	}
	return metadata.Mode
}

func AccountMetadataJSON(home codexconfig.Home, codexAccountID string) (string, error) {
	metadata := map[string]any{
		"preset":           codexconfig.PresetName,
		"preset_version":   codexconfig.PresetVersion,
		"codex_account_id": codexAccountID,
	}
	if home.Dir != "" {
		metadata["codex_dir"] = home.Dir
	}
	if home.AuthPath != "" {
		metadata["auth_path"] = home.AuthPath
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ReplaceFileValueJSON(content string) (string, error) {
	raw, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func AuthTargetValueJSON(accountID string) (string, error) {
	raw, err := json.Marshal(map[string]string{"account_id": accountID})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ParseAuthTargetValueJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var value map[string]string
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", errors.New("auth target value_json must contain one JSON object")
	}
	accountID := strings.TrimSpace(value["account_id"])
	if accountID == "" || len(value) != 1 {
		return "", errors.New(`auth target value_json must be {"account_id": string}`)
	}
	return accountID, nil
}

func ConfigTargetFormatValid(format string, names TargetFormatStrategyNames) bool {
	return format == names.TOMLFormat
}

func ConfigTargetStrategyValid(strategy string, names TargetFormatStrategyNames) bool {
	return strategy == names.TOMLMergeStrategy || strategy == names.ReplaceFileStrategy
}

func AuthTargetFormatStrategyValid(format string, strategy string, names TargetFormatStrategyNames) bool {
	return format == names.JSONFormat && strategy == names.ReplaceFileStrategy
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
		if seen[value] == 0 {
			delete(seen, value)
		}
	}
	return len(seen) == 0
}
