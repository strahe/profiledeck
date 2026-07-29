package grokbuild

import (
	"context"
	"errors"
	"os"

	grokauth "github.com/strahe/profiledeck/internal/grokbuild/auth"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/store"
)

type DetectResult struct {
	ProviderID             string   `json:"provider_id"`
	AdapterID              string   `json:"adapter_id"`
	GrokHome               string   `json:"grok_home"`
	ConfigPath             string   `json:"config_path"`
	AuthPath               string   `json:"auth_path"`
	GrokHomeExists         bool     `json:"grok_home_exists"`
	ConfigStatus           string   `json:"config_status"`
	AuthStatus             string   `json:"auth_status"`
	FileAuthSupported      bool     `json:"file_auth_supported"`
	ProfileDeckInitialized bool     `json:"profiledeck_initialized"`
	ProviderExists         bool     `json:"provider_exists"`
	ProviderAdapterID      string   `json:"provider_adapter_id,omitempty"`
	ProviderCompatible     bool     `json:"provider_compatible"`
	Warnings               []string `json:"warnings,omitempty"`
}

func (service *Service) Detect(ctx context.Context) (DetectResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return DetectResult{}, err
	}
	home, err := service.resolveHome()
	if err != nil {
		return DetectResult{}, err
	}
	result := DetectResult{
		ProviderID: grokconfig.ProviderID, AdapterID: grokconfig.AdapterID,
		GrokHome: home.Dir, ConfigPath: home.ConfigPath, AuthPath: home.AuthPath,
		ConfigStatus: "missing", AuthStatus: "missing",
		FileAuthSupported:  !grokconfig.UnsupportedAuthEnvironment(),
		ProviderCompatible: true,
	}
	if !result.FileAuthSupported {
		result.Warnings = append(
			result.Warnings,
			"Grok Build uses a custom authentication source; file-backed profile changes are unavailable",
		)
	}
	if info, statErr := os.Stat(home.Dir); statErr == nil {
		result.GrokHomeExists = info.IsDir()
		if !info.IsDir() {
			result.Warnings = append(result.Warnings, "Grok Build Home is not a directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		result.Warnings = append(result.Warnings, "Grok Build Home could not be inspected")
	}
	if snapshot, readErr := grokconfig.ReadSnapshot(home.ConfigPath); readErr == nil {
		if !snapshot.Missing {
			result.ConfigStatus = "valid"
			if grokconfig.HasAuthOverride(snapshot.Content) {
				result.Warnings = append(result.Warnings, grokconfig.AuthOverrideWarning)
			}
		}
	} else {
		result.ConfigStatus = "invalid"
		result.Warnings = append(result.Warnings, "Grok Build config.toml is invalid or unreadable")
	}
	if _, readErr := grokauth.ReadSnapshot(home.AuthPath); readErr == nil {
		result.AuthStatus = "valid"
	} else if errors.Is(readErr, os.ErrNotExist) {
		result.AuthStatus = "missing"
	} else {
		result.AuthStatus = "invalid"
		result.Warnings = append(result.Warnings, "Grok Build auth.json is invalid or unreadable")
	}

	status, err := service.runtime.Status(ctx)
	if err != nil {
		return DetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	if status.Initialized && !status.SchemaHealthy {
		result.Warnings = append(result.Warnings, "ProfileDeck database schema is not healthy")
		return result, nil
	}
	if !result.ProfileDeckInitialized {
		return result, nil
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return DetectResult{}, err
	}
	defer db.Close()
	stored, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return DetectResult{}, err
	}
	result.ProviderExists = true
	result.ProviderAdapterID = stored.AdapterID
	metadata, metadataErr := grokpreset.DecodeProviderMetadata(stored.MetadataJSON)
	result.ProviderCompatible = stored.AdapterID == grokconfig.AdapterID &&
		metadataErr == nil && metadata.Compatible() &&
		metadata.GrokHome == home.Dir &&
		metadata.ConfigPath == home.ConfigPath &&
		metadata.AuthPath == home.AuthPath
	if !result.ProviderCompatible {
		result.Warnings = append(result.Warnings, "stored Grok Build Home or Provider contract does not match this application")
	}
	return result, nil
}
