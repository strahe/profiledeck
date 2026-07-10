package profilebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	Format         = "profiledeck.codex-profile-bundle"
	Version        = 1
	MaxBundleBytes = 256 * 1024 * 1024
	maxBundleItems = 10_000
	maxIDLength    = 80
	maxNameLength  = 120
	maxDescription = 1000
)

type Bundle struct {
	Format        string       `json:"format"`
	Version       int          `json:"version"`
	ProviderID    string       `json:"provider_id"`
	PresetVersion int          `json:"preset_version"`
	Profiles      []Profile    `json:"profiles"`
	Credentials   []Credential `json:"credentials"`
	ConfigSets    []ConfigSet  `json:"config_sets"`
}

type Profile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	CredentialID string `json:"credential_id"`
	ConfigSetID  string `json:"config_set_id"`
}

type Credential struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	PayloadJSON   string `json:"payload_json"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type ConfigSet struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	PayloadText   string `json:"payload_text"`
	PayloadSHA256 string `json:"payload_sha256"`
}

func New(profiles []Profile, credentials []Credential, configSets []ConfigSet) Bundle {
	return Bundle{
		Format:        Format,
		Version:       Version,
		ProviderID:    codexconfig.ProviderID,
		PresetVersion: codexconfig.PresetVersion,
		Profiles:      profiles,
		Credentials:   credentials,
		ConfigSets:    configSets,
	}
}

func Encode(bundle Bundle) ([]byte, error) {
	canonical := canonicalBundle(bundle)
	if err := Validate(canonical); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(canonical); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Decode(raw []byte) (Bundle, error) {
	if len(raw) > MaxBundleBytes {
		return Bundle{}, fmt.Errorf("Codex profile bundle exceeds %d bytes", MaxBundleBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode Codex profile bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Bundle{}, errors.New("Codex profile bundle must contain one JSON object")
		}
		return Bundle{}, fmt.Errorf("decode Codex profile bundle: %w", err)
	}
	bundle = canonicalBundle(bundle)
	if err := Validate(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func Validate(bundle Bundle) error {
	if bundle.Format != Format {
		return fmt.Errorf("unsupported Codex profile bundle format %q", bundle.Format)
	}
	if bundle.Version != Version {
		return fmt.Errorf("unsupported Codex profile bundle version %d", bundle.Version)
	}
	if bundle.ProviderID != codexconfig.ProviderID {
		return fmt.Errorf("unsupported Codex profile bundle provider %q", bundle.ProviderID)
	}
	if bundle.PresetVersion != codexconfig.PresetVersion {
		return fmt.Errorf("unsupported Codex preset version %d", bundle.PresetVersion)
	}
	if len(bundle.Profiles) == 0 && len(bundle.ConfigSets) == 0 {
		return errors.New("Codex profile bundle is empty")
	}
	if len(bundle.Profiles) > maxBundleItems || len(bundle.Credentials) > maxBundleItems || len(bundle.ConfigSets) > maxBundleItems {
		return fmt.Errorf("Codex profile bundle contains more than %d items of one kind", maxBundleItems)
	}

	credentials := make(map[string]Credential, len(bundle.Credentials))
	for _, credential := range bundle.Credentials {
		if err := validateID(credential.ID, "credential id"); err != nil {
			return err
		}
		if _, exists := credentials[credential.ID]; exists {
			return fmt.Errorf("duplicate credential id %q", credential.ID)
		}
		if credential.Kind != codexpreset.CredentialKindAuthJSON {
			return fmt.Errorf("credential %q has unsupported kind %q", credential.ID, credential.Kind)
		}
		if _, err := codexauth.NormalizePayload([]byte(credential.PayloadJSON)); err != nil {
			return fmt.Errorf("credential %q payload is invalid: %w", credential.ID, err)
		}
		if err := validateDigest(credential.PayloadJSON, credential.PayloadSHA256, "credential", credential.ID); err != nil {
			return err
		}
		credentials[credential.ID] = credential
	}

	configSets := make(map[string]ConfigSet, len(bundle.ConfigSets))
	for _, configSet := range bundle.ConfigSets {
		if err := validateID(configSet.ID, "Config Set id"); err != nil {
			return err
		}
		if _, exists := configSets[configSet.ID]; exists {
			return fmt.Errorf("duplicate Config Set id %q", configSet.ID)
		}
		if configSet.Kind != codexpreset.ConfigSetKindTOML {
			return fmt.Errorf("Config Set %q has unsupported kind %q", configSet.ID, configSet.Kind)
		}
		if err := validateName(configSet.Name, "Config Set", configSet.ID); err != nil {
			return err
		}
		if err := validateDescription(configSet.Description, "Config Set", configSet.ID); err != nil {
			return err
		}
		if len(configSet.PayloadText) > targetfs.MaxFileBytes {
			return fmt.Errorf("Config Set %q payload exceeds %d bytes", configSet.ID, targetfs.MaxFileBytes)
		}
		if err := codexconfig.ValidateTOML(configSet.PayloadText); err != nil {
			return fmt.Errorf("Config Set %q payload is invalid", configSet.ID)
		}
		if err := validateDigest(configSet.PayloadText, configSet.PayloadSHA256, "Config Set", configSet.ID); err != nil {
			return err
		}
		configSets[configSet.ID] = configSet
	}

	referencedCredentials := make(map[string]struct{}, len(bundle.Credentials))
	profiles := make(map[string]struct{}, len(bundle.Profiles))
	for _, profile := range bundle.Profiles {
		if err := validateID(profile.ID, "profile id"); err != nil {
			return err
		}
		if _, exists := profiles[profile.ID]; exists {
			return fmt.Errorf("duplicate profile id %q", profile.ID)
		}
		if err := validateName(profile.Name, "profile", profile.ID); err != nil {
			return err
		}
		if err := validateDescription(profile.Description, "profile", profile.ID); err != nil {
			return err
		}
		if _, exists := credentials[profile.CredentialID]; !exists {
			return fmt.Errorf("profile %q references missing credential %q", profile.ID, profile.CredentialID)
		}
		if _, exists := configSets[profile.ConfigSetID]; !exists {
			return fmt.Errorf("profile %q references missing Config Set %q", profile.ID, profile.ConfigSetID)
		}
		referencedCredentials[profile.CredentialID] = struct{}{}
		profiles[profile.ID] = struct{}{}
	}
	for id := range credentials {
		if _, referenced := referencedCredentials[id]; !referenced {
			return fmt.Errorf("credential %q is not referenced by any profile", id)
		}
	}
	return nil
}

func canonicalBundle(bundle Bundle) Bundle {
	bundle.Profiles = append([]Profile(nil), bundle.Profiles...)
	bundle.Credentials = append([]Credential(nil), bundle.Credentials...)
	bundle.ConfigSets = append([]ConfigSet(nil), bundle.ConfigSets...)
	sort.Slice(bundle.Profiles, func(i, j int) bool { return bundle.Profiles[i].ID < bundle.Profiles[j].ID })
	sort.Slice(bundle.Credentials, func(i, j int) bool { return bundle.Credentials[i].ID < bundle.Credentials[j].ID })
	sort.Slice(bundle.ConfigSets, func(i, j int) bool { return bundle.ConfigSets[i].ID < bundle.ConfigSets[j].ID })
	return bundle
}

func validateID(value string, label string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not contain surrounding whitespace", label)
	}
	if len(value) > maxIDLength {
		return fmt.Errorf("%s is too long", label)
	}
	for index, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if index > 0 {
			valid = valid || r == '.' || r == '_' || r == '-'
		}
		if !valid {
			return fmt.Errorf("%s contains unsupported characters", label)
		}
	}
	return nil
}

func validateName(value string, kind string, id string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s %q name is required and must not contain surrounding whitespace", kind, id)
	}
	if len(value) > maxNameLength {
		return fmt.Errorf("%s %q name is too long", kind, id)
	}
	return nil
}

func validateDescription(value string, kind string, id string) error {
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s %q description must not contain surrounding whitespace", kind, id)
	}
	if len(value) > maxDescription {
		return fmt.Errorf("%s %q description is too long", kind, id)
	}
	return nil
}

func validateDigest(payload string, expected string, kind string, id string) error {
	digest, err := hex.DecodeString(expected)
	if err != nil || len(digest) != sha256.Size || expected != strings.ToLower(expected) {
		return fmt.Errorf("%s %q SHA-256 is invalid", kind, id)
	}
	actual := sha256.Sum256([]byte(payload))
	if !bytes.Equal(digest, actual[:]) {
		return fmt.Errorf("%s %q SHA-256 does not match its payload", kind, id)
	}
	return nil
}
