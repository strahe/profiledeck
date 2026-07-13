package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
)

const claudeCodeCaptureKindCredential = "credential"

type claudeCodePlanAdapter struct{}

type claudeCodePreparedPlan struct {
	Spec                    targetSpec
	TargetCredential        store.ProviderCredential
	CurrentCredential       *store.ProviderCredential
	CurrentResourceID       string
	CurrentReferenceCount   int
	KnownCredentialPayloads map[string]struct{}
	Warnings                []string
}

func (claudeCodePlanAdapter) ID() string { return claudecodeconfig.AdapterID }

func (claudeCodePlanAdapter) ResolveTargetSpec(providerID, targetID, backendID, path, label string) (targetSpec, error) {
	if providerID != claudecodeconfig.ProviderID || targetID != claudecodeconfig.TargetID {
		return nil, NewError(ErrorRollbackUnsupported, "Claude Code recovery target is unsupported").WithDetail("target_id", targetID)
	}
	if backendID == targetBackendFile {
		if !filepath.IsAbs(path) || filepath.Base(path) != claudecodeconfig.CredentialsFile {
			return nil, NewError(ErrorRollbackUnsupported, "Claude Code recovery file is outside the supported credential target")
		}
		return fileTargetSpec{ID: targetID, Path: path, NeedsContent: true, Secret: true, Label: label}, nil
	}
	if backendID == targetBackendClaudeCodeKeychain {
		account := strings.TrimSpace(path)
		if account != "" {
			return claudeCodeKeychainTargetSpec{ID: targetID, Service: claudecodeconfig.KeychainService, Account: account, Label: label}, nil
		}
	}
	return nil, NewError(ErrorRollbackUnsupported, "Claude Code recovery backend is unsupported").WithDetail("backend_id", backendID)
}

func (claudeCodePlanAdapter) Prepare(ctx context.Context, input planAdapterInput) (planAdapterPrepared, error) {
	if input.Store == nil {
		return planAdapterPrepared{}, NewError(ErrorPlanBuildFailed, "Claude Code plan requires store access")
	}
	if input.Provider.ID != claudecodeconfig.ProviderID || input.Provider.AdapterID != claudecodeconfig.AdapterID {
		return planAdapterPrepared{}, NewError(ErrorClaudeCodeInvalid, "Claude Code plan adapter received an incompatible provider")
	}
	metadata, err := validateClaudeCodeProvider(input.Provider)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	allTargets, err := input.Store.ListProfileTargets(ctx, input.Profile.ID, claudecodeconfig.ProviderID, true)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile targets", err)
	}
	if len(input.Targets) != 0 || len(allTargets) != 0 {
		return planAdapterPrepared{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Profiles cannot contain generic file targets")
	}
	binding, err := input.Store.GetProfileCredentialBinding(ctx, input.Profile.ID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
	if err != nil {
		return planAdapterPrepared{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	allBindings, err := input.Store.ListProfileCredentialBindings(ctx, input.Profile.ID, claudecodeconfig.ProviderID)
	if err != nil || len(allBindings) != 1 {
		return planAdapterPrepared{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	configBindings, err := input.Store.ListProfileConfigSetBindings(ctx, input.Profile.ID, claudecodeconfig.ProviderID)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
	}
	if len(configBindings) != 0 {
		return planAdapterPrepared{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Profile contains unsupported config bindings").WithDetail("profile_id", input.Profile.ID)
	}
	targetCredential, err := requireClaudeCodeCredential(ctx, input.Store, binding.CredentialID)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	knownCredentials, err := input.Store.ListProviderCredentials(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read saved Claude Code logins", err)
	}
	knownPayloads := map[string]struct{}{}
	for _, known := range knownCredentials {
		if credential, err := requireClaudeCodeCredential(ctx, input.Store, known.ID); err == nil {
			knownPayloads[credential.PayloadJSON] = struct{}{}
		}
	}
	warnings := claudeCodeLocatorWarnings(metadata)
	_, targetInfo, _ := claudecodeauth.Normalize([]byte(targetCredential.PayloadJSON))
	if targetInfo.ExpiryUnknown {
		warnings = append(warnings, "Selected Claude Code login expiry could not be determined")
	} else if claudecodeauth.StatusAt(targetInfo, time.Now()) == claudecodeauth.StatusExpired {
		warnings = append(warnings, "Selected Claude Code login is expired; use Claude Code /login to renew it after switching")
	}
	prepared := claudeCodePreparedPlan{
		Spec: claudeCodeTargetSpec(metadata), TargetCredential: targetCredential,
		KnownCredentialPayloads: knownPayloads, Warnings: warnings,
	}
	for _, name := range observedClaudeCodeAuthOverrideHints() {
		prepared.Warnings = append(prepared.Warnings, "This ProfileDeck process observes "+name+"; it may override the switched Claude Code login")
	}
	prepared.Warnings = append(prepared.Warnings,
		"Restart Claude Code sessions after switching",
		"Claude Code apiKeyHelper and settings authentication precedence may override this login",
	)
	active, err := input.Store.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if err == nil {
		activeBinding, bindingErr := input.Store.GetProfileCredentialBinding(ctx, active.ProfileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
		if bindingErr == nil {
			prepared.CurrentResourceID = activeBinding.CredentialID
			if credential, credentialErr := requireClaudeCodeCredential(ctx, input.Store, activeBinding.CredentialID); credentialErr == nil {
				prepared.CurrentCredential = &credential
				references, referenceErr := input.Store.CountProviderCredentialReferences(ctx, credential.ID)
				if referenceErr != nil {
					return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to count active Claude Code login references", referenceErr)
				}
				prepared.CurrentReferenceCount = references
			} else {
				prepared.Warnings = append(prepared.Warnings, "Active Claude Code login is missing or invalid; the current working copy will not be captured")
			}
		} else if errors.Is(bindingErr, store.ErrNotFound) {
			prepared.Warnings = append(prepared.Warnings, "Active Claude Code login binding is missing; the current working copy will not be captured")
		} else {
			return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read active Claude Code login binding", bindingErr)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read active Claude Code Profile", err)
	}
	return planAdapterPrepared{Targets: []preparedTarget{{Spec: prepared.Spec}}, Data: prepared}, nil
}

func (claudeCodePlanAdapter) Finalize(_ context.Context, input planAdapterInput, preparedResult planAdapterPrepared, snapshots map[string]targetSnapshot) (planAdapterResult, error) {
	prepared, ok := preparedResult.Data.(claudeCodePreparedPlan)
	if !ok {
		return planAdapterResult{}, NewError(ErrorPlanBuildFailed, "prepared Claude Code plan is invalid")
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
	result := planAdapterResult{
		Warnings: warnings,
		Bindings: []PlanBinding{{
			TargetID: claudecodeconfig.TargetID, CurrentResourceID: prepared.CurrentResourceID,
			TargetResourceID: prepared.TargetCredential.ID, Changed: prepared.CurrentResourceID != prepared.TargetCredential.ID,
		}},
	}
	currentMatchesTarget := currentValid && currentPayload == prepared.TargetCredential.PayloadJSON
	currentMatchesCurrent := currentValid && prepared.CurrentCredential != nil && currentPayload == prepared.CurrentCredential.PayloadJSON
	_, currentMatchesKnown := prepared.KnownCredentialPayloads[currentPayload]
	currentMatchesOtherKnown := currentValid && currentMatchesKnown && !currentMatchesCurrent && !currentMatchesTarget
	if currentValid && !currentExpired && !currentMatchesTarget && !currentMatchesOtherKnown && prepared.CurrentCredential != nil && !currentMatchesCurrent {
		result.StateCaptures = append(result.StateCaptures, StateCapture{ResourceKind: claudeCodeCaptureKindCredential, ResourceID: prepared.CurrentCredential.ID, Changed: true})
		result.CredentialUpdates = append(result.CredentialUpdates, store.UpsertProviderCredentialParams{
			ID: prepared.CurrentCredential.ID, ProviderID: prepared.CurrentCredential.ProviderID,
			CredentialKind: prepared.CurrentCredential.CredentialKind, PayloadJSON: currentPayload,
			PayloadSHA256: sha256HexString(currentPayload), MetadataJSON: prepared.CurrentCredential.MetadataJSON,
		})
		if prepared.CurrentReferenceCount > 1 {
			warnings = append(warnings, fmt.Sprintf("The refreshed Claude Code login is shared by %d Profiles and will update all of them", prepared.CurrentReferenceCount))
		}
	}
	result.Warnings = append([]string{}, warnings...)
	if currentValid && (currentMatchesTarget || (!currentExpired && prepared.CurrentResourceID == prepared.TargetCredential.ID && !currentMatchesOtherKnown)) {
		desired = before.Content
	}
	needsModeRepair := runtime.GOOS == "linux" && prepared.Spec.BackendID() == targetBackendFile && before.Exists && before.Mode.Perm() != 0o600
	if needsModeRepair {
		warnings = append(warnings, "Claude Code credential file permissions will be repaired to 0600")
		result.Warnings = append([]string{}, warnings...)
	}
	op := applyPlanOperation{
		PlanOperation: PlanOperation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID,
			TargetID: claudecodeconfig.TargetID, BackendID: prepared.Spec.BackendID(), TargetLabel: prepared.Spec.SafeLabel(),
			FileExists: before.Exists, IsSymlink: before.IsSymlink, Warnings: warnings, locatorFingerprint: prepared.Spec.LocatorFingerprint(), sensitive: true,
			privateBeforeFingerprint: before.Fingerprint, privateDesiredFingerprint: sha256HexString(desired),
		},
		DesiredContent: desired, Spec: prepared.Spec, Snapshot: before,
	}
	if fileSpec, ok := prepared.Spec.(fileTargetSpec); ok {
		op.Path = fileSpec.Path
		op.Format = "json"
		op.Strategy = "replace"
		op.DesiredMode = 0o600
		op.UseDesiredMode = true
	}
	if keychainSpec, ok := prepared.Spec.(claudeCodeKeychainTargetSpec); ok {
		op.privateRecoveryLocator = keychainSpec.Account
		if before.privateLocator != "" {
			op.privateObjectFingerprint = sha256HexString(before.privateLocator)
		}
	}
	switch {
	case before.IsSymlink:
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetIsSymlink
		op.Warnings = append(op.Warnings, "Claude Code credential file is a symbolic link and will not be changed")
	case !before.Exists && prepared.Spec.BackendID() == targetBackendClaudeCodeKeychain:
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetMissing
		op.Warnings = append(op.Warnings, "Run Claude Code /login before switching so Claude Code can create its Keychain item")
	case !before.Exists && !parentDirectoryExists(op.Path):
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetMissing
		op.Warnings = append(op.Warnings, "Run Claude Code /login before switching so Claude Code can create its credential directory")
	case !before.Exists:
		op.Action = planActionCreate
		op.StatusReason = planReasonTargetMissing
	case needsModeRepair:
		op.Action = planActionUpdate
		op.StatusReason = planReasonTargetModeDifferent
	case before.Fingerprint == sha256HexString(desired):
		op.Action = planActionNoop
		op.StatusReason = planReasonTargetSameContent
	default:
		op.Action = planActionUpdate
		op.StatusReason = planReasonTargetDifferentContent
	}
	result.Operations = []applyPlanOperation{op}
	return result, nil
}

func parentDirectoryExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(filepath.Dir(path))
	return err == nil && info.IsDir()
}
