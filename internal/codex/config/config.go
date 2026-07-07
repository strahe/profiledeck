package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	ProviderID           = "codex"
	AdapterID            = "codex"
	TargetID             = "config"
	AuthTargetID         = "auth"
	PresetName           = "codex"
	PresetVersion        = 1
	DefaultModelProvider = "openai"
	ConfigFileName       = "config.toml"
	AuthFileName         = "auth.json"
	managedKeyModel      = "model"
	managedKeyProvider   = "model_provider"
	managedKeyBaseURL    = "openai_base_url"
)

var managedKeys = []string{managedKeyModel, managedKeyProvider, managedKeyBaseURL}

type Home struct {
	Dir        string
	ConfigPath string
	AuthPath   string
}

type ManagedConfig struct {
	Model            string
	ModelProvider    string
	OpenAIBaseURL    string
	HasOpenAIBaseURL bool
}

type Snapshot struct {
	Content string
	Missing bool
}

func ManagedKeys() []string {
	return append([]string(nil), managedKeys...)
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

func NormalizeManaged(model string, modelProvider string, openAIBaseURL *string) (ManagedConfig, error) {
	normalizedModel, err := normalizeScalar(model, "model")
	if err != nil {
		return ManagedConfig{}, err
	}
	if strings.TrimSpace(modelProvider) == "" {
		modelProvider = DefaultModelProvider
	}
	normalizedProvider, err := normalizeScalar(modelProvider, "model_provider")
	if err != nil {
		return ManagedConfig{}, err
	}

	result := ManagedConfig{
		Model:         normalizedModel,
		ModelProvider: normalizedProvider,
	}
	if openAIBaseURL == nil {
		return result, nil
	}
	baseURL, err := normalizeBaseURL(*openAIBaseURL)
	if err != nil {
		return ManagedConfig{}, err
	}
	result.OpenAIBaseURL = baseURL
	result.HasOpenAIBaseURL = true
	return result, nil
}

func ValueJSON(config ManagedConfig) (string, error) {
	value := map[string]string{
		managedKeyModel:    config.Model,
		managedKeyProvider: config.ModelProvider,
	}
	if config.HasOpenAIBaseURL {
		value[managedKeyBaseURL] = config.OpenAIBaseURL
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ParseValueJSON(raw string) (ManagedConfig, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return ManagedConfig{}, fmt.Errorf("value_json must be a JSON object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return ManagedConfig{}, fmt.Errorf("value_json must contain one JSON object: %w", err)
		}
		return ManagedConfig{}, errors.New("value_json must contain one JSON object")
	}

	allowed := map[string]struct{}{
		managedKeyModel:    {},
		managedKeyProvider: {},
		managedKeyBaseURL:  {},
	}
	for key := range value {
		if _, ok := allowed[key]; !ok {
			return ManagedConfig{}, fmt.Errorf("value_json contains unsupported key %q", key)
		}
	}

	model, ok := value[managedKeyModel].(string)
	if !ok {
		return ManagedConfig{}, errors.New("value_json model must be a string")
	}
	provider, ok := value[managedKeyProvider].(string)
	if !ok {
		return ManagedConfig{}, errors.New("value_json model_provider must be a string")
	}
	var baseURL *string
	if rawBaseURL, ok := value[managedKeyBaseURL]; ok {
		baseURLValue, ok := rawBaseURL.(string)
		if !ok {
			return ManagedConfig{}, errors.New("value_json openai_base_url must be a string")
		}
		baseURL = &baseURLValue
	}
	return NormalizeManaged(model, provider, baseURL)
}

func ApplyManagedTOML(existing string, fileExists bool, desired ManagedConfig) (string, error) {
	base := map[string]any{}
	if fileExists && strings.TrimSpace(existing) != "" {
		if err := toml.Unmarshal([]byte(existing), &base); err != nil {
			return "", fmt.Errorf("target TOML content is invalid: %w", err)
		}
		if base == nil {
			base = map[string]any{}
		}
	}

	for _, key := range managedKeys {
		delete(base, key)
	}
	base[managedKeyModel] = desired.Model
	base[managedKeyProvider] = desired.ModelProvider
	if desired.HasOpenAIBaseURL {
		base[managedKeyBaseURL] = desired.OpenAIBaseURL
	}

	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(base); err != nil {
		return "", fmt.Errorf("encode target TOML content: %w", err)
	}
	content := buf.String()
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
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

func normalizeScalar(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s cannot contain control characters", field)
		}
	}
	return value, nil
}

func normalizeBaseURL(value string) (string, error) {
	value, err := normalizeScalar(value, managedKeyBaseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("openai_base_url is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("openai_base_url must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("openai_base_url host is required")
	}
	if parsed.User != nil {
		return "", errors.New("openai_base_url cannot contain user info")
	}
	if parsed.RawQuery != "" {
		return "", errors.New("openai_base_url cannot contain query")
	}
	if parsed.Fragment != "" {
		return "", errors.New("openai_base_url cannot contain fragment")
	}
	return parsed.String(), nil
}
