package profile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	claudetarget "github.com/strahe/profiledeck/internal/claudecode/target"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const credentialRandomBytes = 8

// ProviderMetadata is the fixed Claude Code credential locator persisted with the Provider.
type ProviderMetadata struct {
	Preset             string `json:"preset"`
	PresetVersion      int    `json:"preset_version"`
	Storage            string `json:"storage"`
	Path               string `json:"path,omitempty"`
	Service            string `json:"service,omitempty"`
	Account            string `json:"account,omitempty"`
	LocatorFingerprint string `json:"locator_fingerprint"`
}

func NewProviderMetadata(locator claudecodeconfig.Locator) ProviderMetadata {
	metadata := ProviderMetadata{
		Preset: claudecodeconfig.PresetName, PresetVersion: claudecodeconfig.PresetVersion,
		Storage: locator.Storage, Path: locator.Path, Service: locator.Service, Account: locator.Account,
	}
	metadata.LocatorFingerprint = LocatorFingerprint(metadata)
	return metadata
}

func LocatorFingerprint(metadata ProviderMetadata) string {
	return switchtarget.SHA256String(strings.Join([]string{metadata.Storage, metadata.Path, metadata.Service, metadata.Account}, "\x00"))
}

// ValidateProvider validates both fixed preset metadata and safe locator shape.
func ValidateProvider(provider store.Provider) (ProviderMetadata, error) {
	return ValidateProviderRecord(provider.ID, provider.AdapterID, provider.MetadataJSON)
}

func ValidateProviderRecord(providerID, adapterID, metadataJSON string) (ProviderMetadata, error) {
	if providerID != claudecodeconfig.ProviderID || adapterID != claudecodeconfig.AdapterID {
		return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "existing Claude Code provider uses a different adapter")
	}
	var metadata ProviderMetadata
	decoder := json.NewDecoder(strings.NewReader(metadataJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil || metadata.Preset != claudecodeconfig.PresetName || metadata.PresetVersion != claudecodeconfig.PresetVersion {
		return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "existing Claude Code provider is incompatible")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "existing Claude Code provider is incompatible")
	}
	if metadata.LocatorFingerprint == "" || metadata.LocatorFingerprint != LocatorFingerprint(metadata) {
		return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code credential locator is invalid")
	}
	switch metadata.Storage {
	case claudecodeconfig.StorageFile:
		if !filepath.IsAbs(metadata.Path) || filepath.Clean(metadata.Path) != metadata.Path || filepath.Base(metadata.Path) != claudecodeconfig.CredentialsFile || metadata.Service != "" || metadata.Account != "" {
			return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code credential file locator is invalid")
		}
	case claudecodeconfig.StorageKeychain:
		if metadata.Path != "" || metadata.Service != claudecodeconfig.KeychainService || strings.TrimSpace(metadata.Account) == "" || metadata.Account != strings.TrimSpace(metadata.Account) {
			return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Keychain locator is invalid")
		}
	default:
		return ProviderMetadata{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code credential storage is unsupported")
	}
	return metadata, nil
}

// TargetSpec creates a typed file or Keychain target from persisted metadata.
func TargetSpec(metadata ProviderMetadata) switchtarget.Spec {
	if metadata.Storage == claudecodeconfig.StorageKeychain {
		return claudetarget.KeychainSpec{ID: claudecodeconfig.TargetID, Service: metadata.Service, Account: metadata.Account, Label: "Claude Code login"}
	}
	return switchtarget.FileSpec{
		ID: claudecodeconfig.TargetID, Path: metadata.Path, NeedsContent: true, Secret: true, Label: "Claude Code login",
		EnforcedRecoveryMode: 0o600,
	}
}

// RequireCredential validates the exact stored login payload before it enters a plan.
func RequireCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.ProviderCredential{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login is missing")
		}
		return store.ProviderCredential{}, apperror.Wrap(apperror.StoreStatusFailed, "Claude Code login not found", err)
	}
	if err := ValidateCredentialRecord(credential.ProviderID, credential.CredentialKind, credential.PayloadJSON, credential.PayloadSHA256); err != nil {
		return store.ProviderCredential{}, err
	}
	return credential, nil
}

func ValidateCredentialRecord(providerID, credentialKind, payload, payloadSHA256 string) error {
	if providerID != claudecodeconfig.ProviderID || credentialKind != claudecodeconfig.CredentialKind {
		return apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login has unsupported kind")
	}
	if switchtarget.SHA256String(payload) != payloadSHA256 {
		return apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login payload hash is invalid")
	}
	normalized, _, err := claudecodeauth.Normalize([]byte(payload))
	if err != nil || normalized != payload {
		return apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login is invalid")
	}
	return nil
}

func LocatorWarnings(saved ProviderMetadata) []string {
	current, err := claudecodeconfig.ResolveLocator()
	if err != nil {
		return []string{"This ProfileDeck process could not resolve the current Claude Code credential target; the saved target will still be used"}
	}
	observed := NewProviderMetadata(current)
	if observed.LocatorFingerprint != saved.LocatorFingerprint {
		if saved.Storage == claudecodeconfig.StorageFile && observed.Storage == claudecodeconfig.StorageFile {
			return []string{"This process observes a different CLAUDE_CONFIG_DIR; ProfileDeck will continue using the saved Claude Code credential target"}
		}
		return []string{"This process observes a different Claude Code credential locator; ProfileDeck will continue using the saved target"}
	}
	return nil
}

func ObservedAuthOverrideHints() []string {
	names := []string{
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
	}
	result := []string{}
	for _, name := range names {
		if _, ok := os.LookupEnv(name); ok {
			result = append(result, name)
		}
	}
	return result
}

func ParentDirectoryExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(filepath.Dir(path))
	return err == nil && info.IsDir()
}

func NewCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, credentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("claude_code_cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}
