package adapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	claudeprofile "github.com/strahe/profiledeck/internal/claudecode/profile"
	claudetarget "github.com/strahe/profiledeck/internal/claudecode/target"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const captureKindCredential = "credential"

// Adapter builds and validates Claude Code plans without applying target writes.
type Adapter struct{}

type preparedPlan struct {
	Spec                    switchtarget.Spec
	TargetCredential        switchplan.Credential
	CurrentCredential       *switchplan.Credential
	CurrentResourceID       string
	CurrentReferenceCount   int
	KnownCredentialPayloads map[string]struct{}
	Warnings                []string
}

func (Adapter) ID() string { return claudecodeconfig.AdapterID }

func (Adapter) ManagedProviderIDs() []string { return []string{claudecodeconfig.ProviderID} }

func (Adapter) LoadTargets(_ context.Context, input switchplan.Input) ([]switchplan.Target, error) {
	return append([]switchplan.Target(nil), input.Targets...), nil
}

func (Adapter) ResolveTargetSpec(providerID, targetID, backendID, path, label string) (switchtarget.Spec, error) {
	if providerID != claudecodeconfig.ProviderID || targetID != claudecodeconfig.TargetID {
		return nil, apperror.New(apperror.RecoveryUnsupported, "Claude Code recovery target is unsupported").WithDetail("target_id", targetID)
	}
	if backendID == switchtarget.BackendFile {
		if !filepathIsSupportedCredential(path) {
			return nil, apperror.New(apperror.RecoveryUnsupported, "Claude Code recovery file is outside the supported credential target")
		}
		return switchtarget.FileSpec{
			ID: targetID, Path: path, NeedsContent: true, Secret: true, Label: label,
			EnforcedRecoveryMode: 0o600,
		}, nil
	}
	if backendID == switchtarget.BackendClaudeCodeKeychain {
		account := strings.TrimSpace(path)
		if account != "" {
			return claudetarget.KeychainSpec{ID: targetID, Service: claudecodeconfig.KeychainService, Account: account, Label: label}, nil
		}
	}
	return nil, apperror.New(apperror.RecoveryUnsupported, "Claude Code recovery backend is unsupported").WithDetail("backend_id", backendID)
}

func (Adapter) Prepare(ctx context.Context, input switchplan.Input) (switchplan.Prepared, error) {
	if input.State == nil {
		return switchplan.Prepared{}, apperror.New(apperror.PlanBuildFailed, "Claude Code plan requires store access")
	}
	if input.Provider.ID != claudecodeconfig.ProviderID || input.Provider.AdapterID != claudecodeconfig.AdapterID {
		return switchplan.Prepared{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code plan adapter received an incompatible provider")
	}
	metadata, err := claudeprofile.ValidateProviderRecord(input.Provider.ID, input.Provider.AdapterID, input.Provider.MetadataJSON)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	allTargets, err := input.State.ListTargets(ctx, input.Profile.ID, claudecodeconfig.ProviderID, true)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile targets", err)
	}
	if len(input.Targets) != 0 || len(allTargets) != 0 {
		return switchplan.Prepared{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Profiles cannot contain generic file targets")
	}
	binding, err := input.State.GetCredentialBinding(ctx, input.Profile.ID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
	if err != nil {
		return switchplan.Prepared{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	allBindings, err := input.State.ListCredentialBindings(ctx, input.Profile.ID, claudecodeconfig.ProviderID)
	if err != nil || len(allBindings) != 1 {
		return switchplan.Prepared{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	configBindings, err := input.State.ListConfigSetBindings(ctx, input.Profile.ID, claudecodeconfig.ProviderID)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
	}
	if len(configBindings) != 0 {
		return switchplan.Prepared{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Profile contains unsupported config bindings").WithDetail("profile_id", input.Profile.ID)
	}
	targetCredential, err := requireCredential(ctx, input.State, binding.CredentialID)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	knownCredentials, err := input.State.ListCredentials(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read saved Claude Code logins", err)
	}
	knownPayloads := map[string]struct{}{}
	for _, known := range knownCredentials {
		if credential, credentialErr := requireCredential(ctx, input.State, known.ID); credentialErr == nil {
			knownPayloads[credential.PayloadJSON] = struct{}{}
		}
	}
	warnings := claudeprofile.LocatorWarnings(metadata)
	_, targetInfo, _ := claudecodeauth.Normalize([]byte(targetCredential.PayloadJSON))
	if targetInfo.ExpiryUnknown {
		warnings = append(warnings, "Selected Claude Code login expiry could not be determined")
	} else if claudecodeauth.StatusAt(targetInfo, time.Now()) == claudecodeauth.StatusExpired {
		warnings = append(warnings, "Selected Claude Code login is expired; use Claude Code /login to renew it after switching")
	}
	prepared := preparedPlan{
		Spec: claudeprofile.TargetSpec(metadata), TargetCredential: targetCredential,
		KnownCredentialPayloads: knownPayloads, Warnings: warnings,
	}
	for _, name := range claudeprofile.ObservedAuthOverrideHints() {
		prepared.Warnings = append(prepared.Warnings, "This ProfileDeck process observes "+name+"; it may override the switched Claude Code login")
	}
	prepared.Warnings = append(prepared.Warnings,
		"Restart Claude Code sessions after switching",
		"Claude Code apiKeyHelper and settings authentication precedence may override this login",
	)
	active, err := input.State.GetActiveState(ctx, claudecodeconfig.ProviderID)
	if err == nil {
		activeBinding, bindingErr := input.State.GetCredentialBinding(ctx, active.ProfileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
		if bindingErr == nil {
			prepared.CurrentResourceID = activeBinding.CredentialID
			if credential, credentialErr := requireCredential(ctx, input.State, activeBinding.CredentialID); credentialErr == nil {
				prepared.CurrentCredential = &credential
				references, referenceErr := input.State.CountCredentialReferences(ctx, credential.ID)
				if referenceErr != nil {
					return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to count active Claude Code login references", referenceErr)
				}
				prepared.CurrentReferenceCount = references
			} else {
				prepared.Warnings = append(prepared.Warnings, "Active Claude Code login is missing or invalid; the current working copy will not be captured")
			}
		} else if errors.Is(bindingErr, switchplan.ErrStateNotFound) {
			prepared.Warnings = append(prepared.Warnings, "Active Claude Code login binding is missing; the current working copy will not be captured")
		} else {
			return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Claude Code login binding", bindingErr)
		}
	} else if !errors.Is(err, switchplan.ErrStateNotFound) {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Claude Code Profile", err)
	}
	return switchplan.Prepared{Targets: []switchplan.PreparedTarget{{Spec: prepared.Spec}}, Data: prepared}, nil
}

func (Adapter) Finalize(_ context.Context, input switchplan.Input, preparedResult switchplan.Prepared, snapshots map[string]switchtarget.Snapshot) (switchplan.Result, error) {
	prepared, ok := preparedResult.Data.(preparedPlan)
	if !ok {
		return switchplan.Result{}, apperror.New(apperror.PlanBuildFailed, "prepared Claude Code plan is invalid")
	}
	before := snapshots[claudecodeconfig.TargetID]
	desired := prepared.TargetCredential.PayloadJSON
	warnings := append([]string{}, prepared.Warnings...)
	currentPayload := ""
	currentValid, currentExpired := false, false
	if before.Exists && !before.IsSymlink {
		normalized, info, err := claudecodeauth.Normalize([]byte(before.Content))
		if err == nil {
			currentPayload = normalized
			currentValid = true
			currentExpired = claudecodeauth.StatusAt(info, time.Now()) == claudecodeauth.StatusExpired
			if info.ExpiryUnknown {
				warnings = append(warnings, "Current Claude Code login expiry could not be determined")
			}
			if currentExpired {
				warnings = append(warnings, "Expired current Claude Code login will not overwrite the saved active login")
			}
		} else if claudecodeauth.IsKind(err, claudecodeauth.ErrorUnsupportedAccountType) {
			warnings = append(warnings, "Current Claude Code login does not report an active Pro, Max, Team, or Enterprise subscription and will not be captured")
		} else {
			warnings = append(warnings, "Current Claude Code login is invalid and will not be captured")
		}
	}
	result := switchplan.Result{
		Warnings: warnings,
		Bindings: []switchplan.Binding{{
			TargetID: claudecodeconfig.TargetID, CurrentResourceID: prepared.CurrentResourceID,
			TargetResourceID: prepared.TargetCredential.ID, Changed: prepared.CurrentResourceID != prepared.TargetCredential.ID,
		}},
	}
	currentMatchesTarget := currentValid && currentPayload == prepared.TargetCredential.PayloadJSON
	currentMatchesCurrent := currentValid && prepared.CurrentCredential != nil && currentPayload == prepared.CurrentCredential.PayloadJSON
	_, currentMatchesKnown := prepared.KnownCredentialPayloads[currentPayload]
	currentMatchesOtherKnown := currentValid && currentMatchesKnown && !currentMatchesCurrent && !currentMatchesTarget
	if currentValid && !currentExpired && !currentMatchesTarget && !currentMatchesOtherKnown && prepared.CurrentCredential != nil && !currentMatchesCurrent {
		result.StateCaptures = append(result.StateCaptures, switchplan.StateCapture{ResourceKind: captureKindCredential, ResourceID: prepared.CurrentCredential.ID, Changed: true})
		result.CredentialUpdates = append(result.CredentialUpdates, switchplan.CredentialUpdate{
			ID: prepared.CurrentCredential.ID, ProviderID: prepared.CurrentCredential.ProviderID,
			CredentialKind: prepared.CurrentCredential.CredentialKind, PayloadJSON: currentPayload,
			PayloadSHA256: switchtarget.SHA256String(currentPayload), MetadataJSON: prepared.CurrentCredential.MetadataJSON,
		})
		if prepared.CurrentReferenceCount > 1 {
			warnings = append(warnings, fmt.Sprintf("The refreshed Claude Code login is shared by %d Profiles and will update all of them", prepared.CurrentReferenceCount))
		}
	}
	result.Warnings = append([]string{}, warnings...)
	if currentValid && (currentMatchesTarget || (!currentExpired && prepared.CurrentResourceID == prepared.TargetCredential.ID && !currentMatchesOtherKnown)) {
		desired = before.Content
	}
	needsModeRepair := runtime.GOOS == "linux" && prepared.Spec.BackendID() == switchtarget.BackendFile && before.Exists && before.Mode.Perm() != 0o600
	if needsModeRepair {
		warnings = append(warnings, "Claude Code credential file permissions will be repaired to 0600")
		result.Warnings = append([]string{}, warnings...)
	}
	operation := switchplan.ApplyOperation{
		Operation: switchplan.Operation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID, TargetID: claudecodeconfig.TargetID,
			BackendID: prepared.Spec.BackendID(), TargetLabel: prepared.Spec.SafeLabel(), FileExists: before.Exists,
			IsSymlink: before.IsSymlink, Warnings: warnings, LocatorFingerprint: prepared.Spec.LocatorFingerprint(), Sensitive: true,
			PrivateBeforeFingerprint: before.Fingerprint, PrivateDesiredFingerprint: switchtarget.SHA256String(desired),
		},
		DesiredContent: desired, Spec: prepared.Spec, Snapshot: before,
	}
	if fileSpec, ok := prepared.Spec.(switchtarget.FileSpec); ok {
		operation.Path = fileSpec.Path
		operation.Format = "json"
		operation.Strategy = "replace"
		operation.DesiredMode = 0o600
		operation.UseDesiredMode = true
	}
	if keychainSpec, ok := prepared.Spec.(claudetarget.KeychainSpec); ok {
		operation.PrivateRecoveryLocator = keychainSpec.Account
		if before.OpaqueState != "" {
			operation.PrivateObjectFingerprint = switchtarget.SHA256String(before.OpaqueState)
		}
	}
	switch {
	case before.IsSymlink:
		operation.Action = switchplan.ActionUnsupported
		operation.StatusReason = switchplan.ReasonTargetIsSymlink
		operation.Warnings = append(operation.Warnings, "Claude Code credential file is a symbolic link and will not be changed")
	case !before.Exists && prepared.Spec.BackendID() == switchtarget.BackendClaudeCodeKeychain:
		operation.Action = switchplan.ActionUnsupported
		operation.StatusReason = switchplan.ReasonTargetMissing
		operation.Warnings = append(operation.Warnings, "Run Claude Code /login before switching so Claude Code can create its Keychain item")
	case !before.Exists && !claudeprofile.ParentDirectoryExists(operation.Path):
		operation.Action = switchplan.ActionUnsupported
		operation.StatusReason = switchplan.ReasonTargetMissing
		operation.Warnings = append(operation.Warnings, "Run Claude Code /login before switching so Claude Code can create its credential directory")
	case !before.Exists:
		operation.Action = switchplan.ActionCreate
		operation.StatusReason = switchplan.ReasonTargetMissing
	case needsModeRepair:
		operation.Action = switchplan.ActionUpdate
		operation.StatusReason = switchplan.ReasonTargetModeDifferent
	case before.Fingerprint == switchtarget.SHA256String(desired):
		operation.Action = switchplan.ActionNoop
		operation.StatusReason = switchplan.ReasonTargetSameContent
	default:
		operation.Action = switchplan.ActionUpdate
		operation.StatusReason = switchplan.ReasonTargetDifferentContent
	}
	result.Operations = []switchplan.ApplyOperation{operation}
	return result, nil
}

func filepathIsSupportedCredential(path string) bool {
	return filepath.IsAbs(path) && filepath.Base(path) == claudecodeconfig.CredentialsFile
}

func requireCredential(ctx context.Context, state switchplan.StateReader, credentialID string) (switchplan.Credential, error) {
	credential, err := state.GetCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, switchplan.ErrStateNotFound) {
			return switchplan.Credential{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login is missing")
		}
		return switchplan.Credential{}, apperror.Wrap(apperror.StoreStatusFailed, "Claude Code login not found", err)
	}
	if err := claudeprofile.ValidateCredentialRecord(
		credential.ProviderID, credential.CredentialKind, credential.PayloadJSON, credential.PayloadSHA256,
	); err != nil {
		return switchplan.Credential{}, err
	}
	return credential, nil
}

var _ switchplan.Adapter = Adapter{}
