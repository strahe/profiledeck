package app

import (
	"context"
	"errors"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/store"
)

const antigravityCaptureKindCredential = "credential"

type antigravityPlanAdapter struct{}

type antigravityPreparedPlan struct {
	TargetCredential        store.ProviderCredential
	CurrentCredential       *store.ProviderCredential
	CurrentResourceID       string
	KnownCredentialPayloads map[string]struct{}
	Warnings                []string
}

func (antigravityPlanAdapter) ID() string { return agyconfig.AdapterID }

func (antigravityPlanAdapter) ResolveTargetSpec(providerID, targetID, backendID, _, _ string) (targetSpec, error) {
	if providerID == agyconfig.ProviderID && targetID == agyconfig.TargetID && backendID == targetBackendKeyring {
		return antigravityTargetSpec(), nil
	}
	return nil, NewError(ErrorRollbackUnsupported, "Antigravity recovery target is unsupported").
		WithDetail("backend_id", backendID).WithDetail("target_id", targetID)
}

func (antigravityPlanAdapter) Prepare(ctx context.Context, input planAdapterInput) (planAdapterPrepared, error) {
	if input.Store == nil {
		return planAdapterPrepared{}, NewError(ErrorPlanBuildFailed, "Antigravity plan requires store access")
	}
	if input.Provider.ID != agyconfig.ProviderID || input.Provider.AdapterID != agyconfig.AdapterID {
		return planAdapterPrepared{}, NewError(ErrorAntigravityInvalid, "Antigravity plan adapter received an incompatible provider")
	}
	if err := validateAntigravityProvider(input.Provider); err != nil {
		return planAdapterPrepared{}, err
	}
	if len(input.Targets) != 0 {
		return planAdapterPrepared{}, NewError(ErrorAntigravityInvalid, "Antigravity profiles cannot contain generic file targets")
	}
	bindings, err := input.Store.ListProfileCredentialBindings(ctx, input.Profile.ID, agyconfig.ProviderID)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read Antigravity profile bindings", err)
	}
	if len(bindings) != 1 || bindings[0].SlotID != agyconfig.CredentialSlot {
		return planAdapterPrepared{}, NewError(ErrorAntigravityInvalid, "Antigravity profile login binding is missing or invalid").WithDetail("profile_id", input.Profile.ID)
	}
	configBindings, err := input.Store.ListProfileConfigSetBindings(ctx, input.Profile.ID, agyconfig.ProviderID)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read Antigravity profile config bindings", err)
	}
	if len(configBindings) != 0 {
		return planAdapterPrepared{}, NewError(ErrorAntigravityInvalid, "Antigravity profile contains unsupported config bindings").WithDetail("profile_id", input.Profile.ID)
	}
	targetCredential, err := requireAntigravityCredential(ctx, input.Store, bindings[0].CredentialID)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	knownBindings, err := input.Store.ListProfileCredentialBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read Antigravity login bindings", err)
	}
	knownPayloads := make(map[string]struct{}, len(knownBindings))
	seenCredentials := make(map[string]struct{}, len(knownBindings))
	for _, binding := range knownBindings {
		if _, seen := seenCredentials[binding.CredentialID]; seen {
			continue
		}
		seenCredentials[binding.CredentialID] = struct{}{}
		credential, credentialErr := requireAntigravityCredential(ctx, input.Store, binding.CredentialID)
		if credentialErr == nil {
			knownPayloads[credential.PayloadJSON] = struct{}{}
		}
	}
	prepared := antigravityPreparedPlan{
		TargetCredential: targetCredential, KnownCredentialPayloads: knownPayloads,
	}
	active, err := input.Store.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID)
	if err == nil {
		activeBinding, bindingErr := input.Store.GetProfileCredentialBinding(ctx, active.ProfileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
		switch {
		case bindingErr == nil:
			prepared.CurrentResourceID = activeBinding.CredentialID
			if credential, credentialErr := requireAntigravityCredential(ctx, input.Store, activeBinding.CredentialID); credentialErr == nil {
				prepared.CurrentCredential = &credential
			} else {
				prepared.Warnings = append(prepared.Warnings, "active Antigravity login resource is missing or invalid; current login will not be captured")
			}
		case errors.Is(bindingErr, store.ErrNotFound):
			prepared.Warnings = append(prepared.Warnings, "active Antigravity login binding is missing; current login will not be captured")
		default:
			return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read active Antigravity login binding", bindingErr)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return planAdapterPrepared{}, WrapError(ErrorStoreStatusFailed, "failed to read active Antigravity profile", err)
	}
	return planAdapterPrepared{
		Targets: []preparedTarget{{Spec: antigravityTargetSpec()}},
		Data:    prepared,
	}, nil
}

func (antigravityPlanAdapter) Finalize(_ context.Context, input planAdapterInput, preparedResult planAdapterPrepared, snapshots map[string]targetSnapshot) (planAdapterResult, error) {
	prepared, ok := preparedResult.Data.(antigravityPreparedPlan)
	if !ok {
		return planAdapterResult{}, NewError(ErrorPlanBuildFailed, "prepared Antigravity plan is invalid")
	}
	spec := antigravityTargetSpec()
	before := snapshots[agyconfig.TargetID]
	desired := prepared.TargetCredential.PayloadJSON
	warnings := append([]string{}, prepared.Warnings...)
	currentPayload := ""
	currentValid := false
	if before.Exists {
		normalized, _, err := agyauth.Normalize([]byte(before.Content))
		if err != nil {
			return planAdapterResult{}, NewError(ErrorAntigravityInvalid, "current Antigravity login is not compatible with agy v2")
		}
		currentPayload = normalized
		currentValid = true
	}
	result := planAdapterResult{
		Warnings: warnings,
		Bindings: []PlanBinding{{
			TargetID: agyconfig.TargetID, CurrentResourceID: prepared.CurrentResourceID,
			TargetResourceID: prepared.TargetCredential.ID,
			Changed:          prepared.CurrentResourceID != prepared.TargetCredential.ID,
		}},
	}
	currentMatchesTarget := currentValid && currentPayload == prepared.TargetCredential.PayloadJSON
	currentMatchesCurrent := currentValid && prepared.CurrentCredential != nil && currentPayload == prepared.CurrentCredential.PayloadJSON
	_, currentMatchesKnown := prepared.KnownCredentialPayloads[currentPayload]
	currentMatchesOtherKnown := currentValid && currentMatchesKnown && !currentMatchesCurrent && !currentMatchesTarget
	if currentValid && !currentMatchesTarget && !currentMatchesOtherKnown && prepared.CurrentCredential != nil && !currentMatchesCurrent {
		result.StateCaptures = append(result.StateCaptures, StateCapture{
			ResourceKind: antigravityCaptureKindCredential, ResourceID: prepared.CurrentCredential.ID, Changed: true,
		})
		result.CredentialUpdates = append(result.CredentialUpdates, store.UpsertProviderCredentialParams{
			ID: prepared.CurrentCredential.ID, ProviderID: prepared.CurrentCredential.ProviderID,
			CredentialKind: prepared.CurrentCredential.CredentialKind,
			PayloadJSON:    currentPayload, PayloadSHA256: sha256HexString(currentPayload),
			MetadataJSON: prepared.CurrentCredential.MetadataJSON,
		})
	}
	if currentValid && (currentMatchesTarget || (prepared.CurrentResourceID == prepared.TargetCredential.ID && !currentMatchesOtherKnown)) {
		// Keep the exact working-copy bytes when no Keyring write is needed. The
		// normalized payload is stored for resource capture, but recovery hashes
		// must describe the value that actually remains in the credential store.
		desired = before.Content
	}
	op := applyPlanOperation{
		PlanOperation: PlanOperation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID,
			TargetID: agyconfig.TargetID, BackendID: targetBackendKeyring,
			TargetLabel: spec.SafeLabel(), FileExists: before.Exists,
			Warnings: warnings, locatorFingerprint: spec.LocatorFingerprint(), sensitive: true,
			privateBeforeFingerprint:  before.Fingerprint,
			privateDesiredFingerprint: sha256HexString(desired),
		},
		DesiredContent: desired, Spec: spec, Snapshot: before,
	}
	switch {
	case !before.Exists:
		op.Action = planActionCreate
		op.StatusReason = planReasonTargetMissing
	case before.Exists && before.Fingerprint == sha256HexString(desired):
		op.Action = planActionNoop
		op.StatusReason = planReasonTargetSameContent
	default:
		op.Action = planActionUpdate
		op.StatusReason = planReasonTargetDifferentContent
	}
	result.Operations = []applyPlanOperation{op}
	return result, nil
}
