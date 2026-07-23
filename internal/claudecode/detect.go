package claudecode

import (
	"context"
	"errors"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type ClaudeCodeDetectRequest struct {
	AllowKeychainInteraction bool `json:"allow_keychain_interaction"`
}

type ClaudeCodeDetectResult struct {
	ProviderID                    string   `json:"provider_id"`
	AdapterID                     string   `json:"adapter_id"`
	CredentialStatus              string   `json:"credential_status"`
	ExpiresAtUnixMS               int64    `json:"expires_at_unix_ms,omitempty"`
	ProfileDeckInitialized        bool     `json:"profiledeck_initialized"`
	ProviderExists                bool     `json:"provider_exists"`
	ProviderCompatible            bool     `json:"provider_compatible"`
	KeychainAuthorizationRequired bool     `json:"keychain_authorization_required"`
	ObservedAuthOverrideHints     []string `json:"observed_auth_override_hints"`
	Warnings                      []string `json:"warnings"`
}

func (service *Service) Detect(ctx context.Context, req ClaudeCodeDetectRequest) (ClaudeCodeDetectResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeDetectResult{}, err
	}
	result := ClaudeCodeDetectResult{
		ProviderID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID,
		CredentialStatus: claudecodeauth.StatusMissing, ProviderCompatible: true,
		ObservedAuthOverrideHints: observedClaudeCodeAuthOverrideHints(), Warnings: []string{},
	}
	status, err := service.runtime.Status(ctx)
	if err != nil {
		return ClaudeCodeDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	var spec switchtarget.Spec
	if result.ProfileDeckInitialized {
		db, err := service.stores.OpenHealthy(ctx, true)
		if err != nil {
			return ClaudeCodeDetectResult{}, err
		}
		defer db.Close()
		provider, providerErr := db.GetProvider(ctx, claudecodeconfig.ProviderID)
		if providerErr == nil {
			result.ProviderExists = true
			metadata, validationErr := validateClaudeCodeProvider(provider)
			if validationErr != nil {
				result.ProviderCompatible = false
				result.Warnings = append(result.Warnings, "Saved Claude Code credential target is incompatible")
				return result, nil
			}
			spec = claudeCodeTargetSpec(metadata)
			result.Warnings = append(result.Warnings, claudeCodeLocatorWarnings(metadata)...)
		} else if !errors.Is(providerErr, store.ErrNotFound) {
			return ClaudeCodeDetectResult{}, mapProviderStoreError(providerErr)
		}
	}
	if spec == nil {
		locator, err := claudecodeconfig.ResolveLocator()
		if err != nil {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.Warnings = append(result.Warnings, "Claude Code credential target is unavailable")
			return result, nil
		}
		metadata := newClaudeCodeProviderMetadata(locator)
		spec = claudeCodeTargetSpec(metadata)
	}
	snapshot, err := service.inspectTarget(ctx, spec, req.AllowKeychainInteraction)
	if err != nil {
		if IsKeychainAuthorizationRequired(err) {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.KeychainAuthorizationRequired = true
			result.Warnings = append(result.Warnings, "Claude Code Keychain authorization is required")
			return result, nil
		}
		var appErr *apperror.Error
		if errors.As(err, &appErr) && (appErr.Code == apperror.ClaudeCodeInvalid || appErr.Code == apperror.TargetChanged) {
			result.CredentialStatus = claudecodeauth.StatusInvalid
			result.Warnings = append(result.Warnings, "Claude Code login target is invalid")
		} else {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.Warnings = append(result.Warnings, "Claude Code login could not be read")
		}
		return result, nil
	}
	if !snapshot.Exists {
		return result, nil
	}
	if snapshot.IsSymlink {
		result.CredentialStatus = claudecodeauth.StatusInvalid
		result.Warnings = append(result.Warnings, "Claude Code credential file is a symbolic link and will not be used")
		return result, nil
	}
	_, info, err := claudecodeauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		if claudecodeauth.IsKind(err, claudecodeauth.ErrorUnsupportedAccountType) {
			result.CredentialStatus = claudecodeauth.StatusUnsupported
			result.Warnings = append(result.Warnings, "Claude Code does not report an active Pro, Max, Team, or Enterprise subscription for this login")
		} else {
			result.CredentialStatus = claudecodeauth.StatusInvalid
			result.Warnings = append(result.Warnings, "Claude Code login is invalid")
		}
		return result, nil
	}
	result.CredentialStatus = claudecodeauth.StatusAt(info, time.Now())
	result.ExpiresAtUnixMS = info.ExpiresAtUnixMS
	if info.ExpiryUnknown {
		result.Warnings = append(result.Warnings, "Claude Code login expiry could not be determined")
	}
	return result, nil
}
