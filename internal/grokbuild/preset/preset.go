package preset

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
)

const (
	ProviderName            = "Grok Build"
	CredentialKindAuthJSON  = "grok-build-auth-json"
	ConfigSetKindTOML       = "grok-build-config-toml"
	CredentialSlotAuth      = "auth"
	ConfigSetSlotUserConfig = "user-config"
	TargetModeConfigSet     = "config-set-binding"
	TargetModeCredential    = "credential-binding"
)

type ProviderMetadata struct {
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
	GrokHome      string `json:"grok_home"`
	ConfigPath    string `json:"config_path"`
	AuthPath      string `json:"auth_path"`
}

type TargetMetadata struct {
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
	TargetKind    string `json:"target_kind"`
	Mode          string `json:"mode"`
}

func ProviderMetadataJSON(home grokconfig.Home) (string, error) {
	raw, err := json.Marshal(ProviderMetadata{
		Preset:        grokconfig.PresetName,
		PresetVersion: grokconfig.PresetVersion,
		GrokHome:      home.Dir,
		ConfigPath:    home.ConfigPath,
		AuthPath:      home.AuthPath,
	})
	return string(raw), err
}

func DecodeProviderMetadata(raw string) (ProviderMetadata, error) {
	var metadata ProviderMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return ProviderMetadata{}, err
	}
	return metadata, nil
}

func (metadata ProviderMetadata) Compatible() bool {
	return metadata.Preset == grokconfig.PresetName &&
		metadata.PresetVersion == grokconfig.PresetVersion &&
		filepath.IsAbs(metadata.GrokHome) &&
		metadata.GrokHome == filepath.Clean(metadata.GrokHome) &&
		metadata.ConfigPath == filepath.Join(metadata.GrokHome, grokconfig.ConfigFileName) &&
		metadata.AuthPath == filepath.Join(metadata.GrokHome, grokconfig.AuthFileName)
}

func TargetMetadataJSON(targetKind, mode string) (string, error) {
	raw, err := json.Marshal(TargetMetadata{
		Preset:        grokconfig.PresetName,
		PresetVersion: grokconfig.PresetVersion,
		TargetKind:    targetKind,
		Mode:          mode,
	})
	return string(raw), err
}

func DecodeTargetMetadata(raw string) (TargetMetadata, error) {
	var metadata TargetMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return TargetMetadata{}, err
	}
	return metadata, nil
}

func (metadata TargetMetadata) Compatible() bool {
	if metadata.Preset != grokconfig.PresetName || metadata.PresetVersion != grokconfig.PresetVersion {
		return false
	}
	switch metadata.TargetKind {
	case grokconfig.AuthTargetID:
		return metadata.Mode == TargetModeCredential
	case grokconfig.ConfigTargetID:
		return metadata.Mode == TargetModeConfigSet
	default:
		return false
	}
}

func CredentialBindingValueJSON(credentialID string) (string, error) {
	return bindingValueJSON("credential_id", credentialID)
}

func ConfigSetBindingValueJSON(configSetID string) (string, error) {
	return bindingValueJSON("config_set_id", configSetID)
}

func bindingValueJSON(key, id string) (string, error) {
	raw, err := json.Marshal(map[string]string{key: strings.TrimSpace(id)})
	return string(raw), err
}

func ParseCredentialBindingValueJSON(raw string) (string, error) {
	return parseBindingValueJSON(raw, "credential_id", "auth target")
}

func ParseConfigSetBindingValueJSON(raw string) (string, error) {
	return parseBindingValueJSON(raw, "config_set_id", "config target")
}

func parseBindingValueJSON(raw, key, label string) (string, error) {
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
		return "", errors.New(label + " value must contain one JSON object")
	}
	id := strings.TrimSpace(value[key])
	if id == "" || len(value) != 1 {
		return "", errors.New(label + " value is invalid")
	}
	return id, nil
}
