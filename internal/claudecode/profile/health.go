package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"

	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	doctorcore "github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

// TargetInspection is a semantic result supplied by app composition. The
// provider package never depends on an entrypoint-specific error type.
type TargetInspection struct {
	Snapshot                      switchtarget.Snapshot
	Err                           error
	KeychainAuthorizationRequired bool
	KeychainReferenceInvalid      bool
}

// TargetInspector reads the persisted Claude Code working-copy target.
type TargetInspector func(context.Context, switchtarget.Spec) TargetInspection

// InspectHealth validates Claude Code resources, typed bindings, and the
// persisted credential target through an injected, read-only inspector.
func InspectHealth(ctx context.Context, db *store.Store, inspect TargetInspector) []doctorcore.Finding {
	if db == nil {
		return nil
	}
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []doctorcore.Finding{{ID: "claude_code_provider_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Claude Code provider"}}
	}
	metadata, err := ValidateProvider(provider)
	if err != nil {
		return []doctorcore.Finding{{ID: "claude_code_preset_invalid", Level: doctorcore.LevelError, Message: "Claude Code provider or credential locator is incompatible"}}
	}
	findings := []doctorcore.Finding{}
	credentials, err := db.ListProviderCredentials(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		findings = append(findings, doctorcore.Finding{ID: "claude_code_login_state_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect saved Claude Code login state"})
	} else {
		for _, credential := range credentials {
			if _, credentialErr := RequireCredential(ctx, db, credential.ID); credentialErr != nil {
				findings = append(findings, doctorcore.Finding{
					ID: "claude_code_login_state_invalid", Level: doctorcore.LevelError,
					Message: "saved Claude Code login state has an invalid kind, payload hash, or schema",
					Details: map[string]any{"credential_id": credential.ID},
				})
			}
		}
	}
	credentialBindings, err := db.ListProfileCredentialBindingsByProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "claude_code_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Claude Code Profile bindings"})
	}
	configBindings, err := db.ListProfileConfigSetBindingsByProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "claude_code_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Claude Code Profile bindings"})
	}
	genericTargets, err := db.ListProfileTargetsByProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "claude_code_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Claude Code Profile bindings"})
	}
	grouped := map[string][]store.ProfileCredentialBinding{}
	for _, binding := range credentialBindings {
		grouped[binding.ProfileID] = append(grouped[binding.ProfileID], binding)
	}
	configCounts := map[string]int{}
	for _, binding := range configBindings {
		configCounts[binding.ProfileID]++
		if _, ok := grouped[binding.ProfileID]; !ok {
			grouped[binding.ProfileID] = nil
		}
	}
	targetCounts := map[string]int{}
	for _, target := range genericTargets {
		targetCounts[target.ProfileID]++
		if _, ok := grouped[target.ProfileID]; !ok {
			grouped[target.ProfileID] = nil
		}
	}
	activeProfileID := ""
	if active, activeErr := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID); activeErr == nil {
		activeProfileID = active.ProfileID
		if _, ok := grouped[active.ProfileID]; !ok {
			grouped[active.ProfileID] = nil
		}
	} else if activeErr != nil && !errors.Is(activeErr, store.ErrNotFound) {
		findings = append(findings, doctorcore.Finding{ID: "claude_code_active_state_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect active Claude Code Profile"})
	}
	var activeCredential *store.ProviderCredential
	for profileID, bindings := range grouped {
		if _, err := db.GetProfile(ctx, profileID); err != nil {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_profile_missing", Level: doctorcore.LevelError, Message: "Claude Code binding references a missing Profile", Details: map[string]any{"profile_id": profileID}})
			continue
		}
		if len(bindings) != 1 || bindings[0].SlotID != claudecodeconfig.CredentialSlot || configCounts[profileID] != 0 || targetCounts[profileID] != 0 {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_login_binding_invalid", Level: doctorcore.LevelError, Message: "Claude Code Profile bindings are missing or unsupported", Details: map[string]any{"profile_id": profileID}})
			continue
		}
		credential, err := RequireCredential(ctx, db, bindings[0].CredentialID)
		if err != nil {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_login_state_invalid", Level: doctorcore.LevelError, Message: "Claude Code Profile references missing or invalid login state", Details: map[string]any{"profile_id": profileID}})
			continue
		}
		if profileID == activeProfileID {
			credentialCopy := credential
			activeCredential = &credentialCopy
		}
	}
	if inspect == nil {
		return append(findings, doctorcore.Finding{ID: "claude_code_login_unavailable", Level: doctorcore.LevelWarning, Message: "Claude Code login working copy could not be read"})
	}
	inspection := inspect(ctx, TargetSpec(metadata))
	if inspection.Err != nil {
		if metadata.Storage == claudecodeconfig.StorageFile {
			if info, statErr := os.Lstat(metadata.Path); statErr == nil && info.Mode()&os.ModeSymlink == 0 && (info.IsDir() || !info.Mode().IsRegular()) {
				return append(findings, doctorcore.Finding{ID: "claude_code_login_file_type", Level: doctorcore.LevelError, Message: "Claude Code credential target is not a regular file"})
			}
		}
		if metadata.Storage == claudecodeconfig.StorageKeychain && inspection.KeychainAuthorizationRequired {
			return append(findings, doctorcore.Finding{ID: "claude_code_keychain_authorization_required", Level: doctorcore.LevelWarning, Message: "Claude Code Keychain login needs explicit authorization before it can be inspected"})
		}
		if metadata.Storage == claudecodeconfig.StorageKeychain && inspection.KeychainReferenceInvalid {
			return append(findings, doctorcore.Finding{ID: "claude_code_keychain_reference_invalid", Level: doctorcore.LevelError, Message: "Claude Code Keychain item could not be resolved uniquely"})
		}
		return append(findings, doctorcore.Finding{ID: "claude_code_login_unavailable", Level: doctorcore.LevelWarning, Message: "Claude Code login working copy could not be read"})
	}
	snapshot := inspection.Snapshot
	if !snapshot.Exists {
		findings = append(findings, doctorcore.Finding{ID: "claude_code_login_missing", Level: doctorcore.LevelWarning, Message: "Claude Code login working copy is missing"})
		if metadata.Storage == claudecodeconfig.StorageFile && goruntime.GOOS == "windows" && !FileReplacementAvailable(metadata.Path) {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_credentials_replace_unavailable", Level: doctorcore.LevelWarning, Message: "Claude Code credential file directory is not writable for atomic replacement"})
		}
		return findings
	}
	if snapshot.IsSymlink {
		return append(findings, doctorcore.Finding{ID: "claude_code_login_symlink", Level: doctorcore.LevelError, Message: "Claude Code credential file is a symlink"})
	}
	normalized, info, authErr := claudecodeauth.Normalize([]byte(snapshot.Content))
	if authErr != nil {
		level, id, message := doctorcore.LevelError, "claude_code_login_invalid", "Claude Code login working copy is invalid"
		if claudecodeauth.IsKind(authErr, claudecodeauth.ErrorUnsupportedAccountType) {
			id, message = "claude_code_login_unsupported", "Claude Code login working copy does not report an active Pro, Max, Team, or Enterprise subscription"
		}
		findings = append(findings, doctorcore.Finding{ID: id, Level: level, Message: message})
	} else {
		if info.ExpiryUnknown {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_expiry_unknown", Level: doctorcore.LevelWarning, Message: "Claude Code login expiry could not be determined"})
		}
		if activeCredential != nil && normalized != activeCredential.PayloadJSON {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_working_copy_changed", Level: doctorcore.LevelWarning, Message: "Claude Code login working copy differs from the active saved login"})
		}
	}
	if metadata.Storage == claudecodeconfig.StorageFile {
		if goruntime.GOOS == "windows" && !FileReplacementAvailable(metadata.Path) {
			findings = append(findings, doctorcore.Finding{ID: "claude_code_credentials_replace_unavailable", Level: doctorcore.LevelWarning, Message: "Claude Code credential file directory is not writable for atomic replacement"})
		}
	}
	return findings
}

// FileReplacementAvailable verifies the atomic-replacement primitive needed on Windows.
func FileReplacementAvailable(path string) bool {
	directory := filepath.Dir(path)
	source, err := os.CreateTemp(directory, ".profiledeck-doctor-source-*")
	if err != nil {
		return false
	}
	sourceName := source.Name()
	defer func() { _ = os.Remove(sourceName) }()
	if err := source.Close(); err != nil {
		return false
	}
	destination, err := os.CreateTemp(directory, ".profiledeck-doctor-destination-*")
	if err != nil {
		return false
	}
	destinationName := destination.Name()
	defer func() { _ = os.Remove(destinationName) }()
	if err := destination.Close(); err != nil {
		return false
	}
	return os.Rename(sourceName, destinationName) == nil
}
