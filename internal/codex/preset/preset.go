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
	CredentialKindAuthJSON  = "codex-auth-json"
	ConfigSetKindTOML       = "codex-config-toml"
	TargetModeConfigSet     = "config-set-binding"
	TargetModeCredential    = "credential-binding"
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
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
	TargetKind    string `json:"target_kind"`
	Mode          string `json:"mode,omitempty"`
}

type TargetFormatStrategyNames struct {
	JSONFormat          string
	TOMLFormat          string
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
		return metadata.Mode == TargetModeConfigSet
	case codexconfig.AuthTargetID:
		return metadata.Mode == TargetModeCredential
	default:
		return false
	}
}

func CredentialBindingValueJSON(credentialID string) (string, error) {
	raw, err := json.Marshal(map[string]string{"credential_id": strings.TrimSpace(credentialID)})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ConfigSetBindingValueJSON(configSetID string) (string, error) {
	raw, err := json.Marshal(map[string]string{"config_set_id": strings.TrimSpace(configSetID)})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ParseCredentialBindingValueJSON(raw string) (string, error) {
	return parseBindingValueJSON(raw, "credential_id", "auth target")
}

func ParseConfigSetBindingValueJSON(raw string) (string, error) {
	return parseBindingValueJSON(raw, "config_set_id", "config target")
}

func parseBindingValueJSON(raw string, key string, label string) (string, error) {
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
		return "", errors.New(label + " value_json must contain one JSON object")
	}
	id := strings.TrimSpace(value[key])
	if id == "" || len(value) != 1 {
		return "", errors.New(label + ` value_json must contain only a non-empty "` + key + `" string`)
	}
	return id, nil
}

func ConfigTargetFormatValid(format string, names TargetFormatStrategyNames) bool {
	return format == names.TOMLFormat
}

func ConfigTargetStrategyValid(strategy string, names TargetFormatStrategyNames) bool {
	return strategy == names.ReplaceFileStrategy
}

func AuthTargetFormatStrategyValid(format string, strategy string, names TargetFormatStrategyNames) bool {
	return format == names.JSONFormat && strategy == names.ReplaceFileStrategy
}
