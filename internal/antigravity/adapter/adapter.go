package adapter

import (
	"context"
	"errors"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	agyprofile "github.com/strahe/profiledeck/internal/antigravity/profile"
	"github.com/strahe/profiledeck/internal/apperror"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const captureKindCredential = "credential"

// Adapter builds Antigravity plans without touching external targets or mutating SQLite.
type Adapter struct{}

type preparedPlan struct {
	TargetCredential        switchplan.Credential
	CurrentCredential       *switchplan.Credential
	CurrentResourceID       string
	KnownCredentialPayloads map[string]struct{}
	Warnings                []string
}

func (Adapter) ID() string { return agyconfig.AdapterID }

func (Adapter) ManagedProviderIDs() []string { return []string{agyconfig.ProviderID} }

func (Adapter) LoadTargets(_ context.Context, input switchplan.Input) ([]switchplan.Target, error) {
	return append([]switchplan.Target(nil), input.Targets...), nil
}

func (Adapter) ResolveTargetSpec(providerID, targetID, backendID, _, _ string) (switchtarget.Spec, error) {
	if providerID == agyconfig.ProviderID && targetID == agyconfig.TargetID && backendID == switchtarget.BackendKeyring {
		return agyprofile.TargetSpec(), nil
	}
	return nil, apperror.New(apperror.RollbackUnsupported, "Antigravity recovery target is unsupported").
		WithDetail("backend_id", backendID).WithDetail("target_id", targetID)
}

func (Adapter) Prepare(ctx context.Context, input switchplan.Input) (switchplan.Prepared, error) {
	if input.State == nil {
		return switchplan.Prepared{}, apperror.New(apperror.PlanBuildFailed, "Antigravity plan requires store access")
	}
	if input.Provider.ID != agyconfig.ProviderID || input.Provider.AdapterID != agyconfig.AdapterID {
		return switchplan.Prepared{}, apperror.New(apperror.AntigravityInvalid, "Antigravity plan adapter received an incompatible provider")
	}
	if err := agyprofile.ValidateProviderRecord(input.Provider.AdapterID, input.Provider.MetadataJSON); err != nil {
		return switchplan.Prepared{}, err
	}
	if len(input.Targets) != 0 {
		return switchplan.Prepared{}, apperror.New(apperror.AntigravityInvalid, "Antigravity profiles cannot contain generic file targets")
	}
	bindings, err := input.State.ListCredentialBindings(ctx, input.Profile.ID, agyconfig.ProviderID)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity profile bindings", err)
	}
	if len(bindings) != 1 || bindings[0].SlotID != agyconfig.CredentialSlot {
		return switchplan.Prepared{}, apperror.New(apperror.AntigravityInvalid, "Antigravity profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	configBindings, err := input.State.ListConfigSetBindings(ctx, input.Profile.ID, agyconfig.ProviderID)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity profile config bindings", err)
	}
	if len(configBindings) != 0 {
		return switchplan.Prepared{}, apperror.New(apperror.AntigravityInvalid, "Antigravity profile contains unsupported config bindings").WithDetail("profile_id", input.Profile.ID)
	}
	targetCredential, err := requireCredential(ctx, input.State, bindings[0].CredentialID)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	knownBindings, err := input.State.ListCredentialBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity login bindings", err)
	}
	knownPayloads := make(map[string]struct{}, len(knownBindings))
	seenCredentials := make(map[string]struct{}, len(knownBindings))
	for _, binding := range knownBindings {
		if _, seen := seenCredentials[binding.CredentialID]; seen {
			continue
		}
		seenCredentials[binding.CredentialID] = struct{}{}
		credential, credentialErr := requireCredential(ctx, input.State, binding.CredentialID)
		if credentialErr == nil {
			knownPayloads[credential.PayloadJSON] = struct{}{}
		}
	}
	prepared := preparedPlan{TargetCredential: targetCredential, KnownCredentialPayloads: knownPayloads}
	active, err := input.State.GetActiveState(ctx, agyconfig.ProviderID)
	if err == nil {
		activeBinding, bindingErr := input.State.GetCredentialBinding(ctx, active.ProfileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
		switch {
		case bindingErr == nil:
			prepared.CurrentResourceID = activeBinding.CredentialID
			if credential, credentialErr := requireCredential(ctx, input.State, activeBinding.CredentialID); credentialErr == nil {
				prepared.CurrentCredential = &credential
			} else {
				prepared.Warnings = append(prepared.Warnings, "active Antigravity login resource is missing or invalid; current login will not be captured")
			}
		case errors.Is(bindingErr, switchplan.ErrStateNotFound):
			prepared.Warnings = append(prepared.Warnings, "active Antigravity login binding is missing; current login will not be captured")
		default:
			return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Antigravity login binding", bindingErr)
		}
	} else if !errors.Is(err, switchplan.ErrStateNotFound) {
		return switchplan.Prepared{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Antigravity profile", err)
	}
	return switchplan.Prepared{
		Targets: []switchplan.PreparedTarget{{Spec: agyprofile.TargetSpec()}},
		Data:    prepared,
	}, nil
}

func (Adapter) Finalize(_ context.Context, input switchplan.Input, preparedResult switchplan.Prepared, snapshots map[string]switchtarget.Snapshot) (switchplan.Result, error) {
	prepared, ok := preparedResult.Data.(preparedPlan)
	if !ok {
		return switchplan.Result{}, apperror.New(apperror.PlanBuildFailed, "prepared Antigravity plan is invalid")
	}
	spec := agyprofile.TargetSpec()
	before := snapshots[agyconfig.TargetID]
	desired := prepared.TargetCredential.PayloadJSON
	warnings := append([]string{}, prepared.Warnings...)
	currentPayload := ""
	currentValid := false
	if before.Exists {
		normalized, _, err := agyauth.Normalize([]byte(before.Content))
		if err != nil {
			return switchplan.Result{}, apperror.New(apperror.AntigravityInvalid, "current Antigravity login is not compatible with agy v2")
		}
		currentPayload = normalized
		currentValid = true
	}
	result := switchplan.Result{
		Warnings: warnings,
		Bindings: []switchplan.Binding{{
			TargetID: agyconfig.TargetID, CurrentResourceID: prepared.CurrentResourceID,
			TargetResourceID: prepared.TargetCredential.ID, Changed: prepared.CurrentResourceID != prepared.TargetCredential.ID,
		}},
	}
	currentMatchesTarget := currentValid && currentPayload == prepared.TargetCredential.PayloadJSON
	currentMatchesCurrent := currentValid && prepared.CurrentCredential != nil && currentPayload == prepared.CurrentCredential.PayloadJSON
	_, currentMatchesKnown := prepared.KnownCredentialPayloads[currentPayload]
	currentMatchesOtherKnown := currentValid && currentMatchesKnown && !currentMatchesCurrent && !currentMatchesTarget
	if currentValid && !currentMatchesTarget && !currentMatchesOtherKnown && prepared.CurrentCredential != nil && !currentMatchesCurrent {
		result.StateCaptures = append(result.StateCaptures, switchplan.StateCapture{
			ResourceKind: captureKindCredential, ResourceID: prepared.CurrentCredential.ID, Changed: true,
		})
		result.CredentialUpdates = append(result.CredentialUpdates, switchplan.CredentialUpdate{
			ID: prepared.CurrentCredential.ID, ProviderID: prepared.CurrentCredential.ProviderID,
			CredentialKind: prepared.CurrentCredential.CredentialKind,
			PayloadJSON:    currentPayload, PayloadSHA256: switchtarget.SHA256String(currentPayload),
			MetadataJSON: prepared.CurrentCredential.MetadataJSON,
		})
	}
	if currentValid && (currentMatchesTarget || (prepared.CurrentResourceID == prepared.TargetCredential.ID && !currentMatchesOtherKnown)) {
		// Preserve exact working bytes when no Keyring write is required.
		desired = before.Content
	}
	operation := switchplan.ApplyOperation{
		Operation: switchplan.Operation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID,
			TargetID: agyconfig.TargetID, BackendID: switchtarget.BackendKeyring,
			TargetLabel: spec.SafeLabel(), FileExists: before.Exists, Warnings: warnings,
			LocatorFingerprint: spec.LocatorFingerprint(), Sensitive: true,
			PrivateBeforeFingerprint:  before.Fingerprint,
			PrivateDesiredFingerprint: switchtarget.SHA256String(desired),
		},
		DesiredContent: desired, Spec: spec, Snapshot: before,
	}
	switch {
	case !before.Exists:
		operation.Action = switchplan.ActionCreate
		operation.StatusReason = switchplan.ReasonTargetMissing
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

func requireCredential(ctx context.Context, state switchplan.StateReader, credentialID string) (switchplan.Credential, error) {
	credential, err := state.GetCredential(ctx, credentialID)
	if err != nil {
		return switchplan.Credential{}, apperror.Wrap(apperror.StoreStatusFailed, "Antigravity login not found", err)
	}
	if err := agyprofile.ValidateCredentialRecord(
		credential.ProviderID, credential.CredentialKind, credential.PayloadJSON, credential.PayloadSHA256,
	); err != nil {
		return switchplan.Credential{}, err
	}
	return credential, nil
}

var _ switchplan.Adapter = Adapter{}
