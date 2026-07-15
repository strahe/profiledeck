package adapter

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	codexprofile "github.com/strahe/profiledeck/internal/codex/profile"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	captureKindCredential = "credential"
	captureKindConfigSet  = "config-set"
)

// Adapter builds and validates Codex plans without mutating SQLite or targets.
type Adapter struct{}

type desiredResource struct {
	ID      string
	Name    string
	Content string
	SHA256  string
}

type preparedPlan struct {
	ConfigTarget      switchplan.Target
	AuthTarget        switchplan.Target
	TargetBindings    codexprofile.Bindings
	CurrentBindings   codexprofile.Bindings
	ConfigResource    desiredResource
	AuthResource      desiredResource
	KnownAuthPayloads []string
	KnownConfigHashes map[string]struct{}
	Warnings          []string
}

type pendingCapture struct {
	Public           switchplan.StateCapture
	CredentialUpdate *switchplan.CredentialUpdate
	ConfigSetUpdate  *switchplan.ConfigSetUpdate
}

func (Adapter) ID() string { return codexconfig.AdapterID }

func (Adapter) ManagedProviderIDs() []string { return []string{codexconfig.ProviderID} }

func (Adapter) LoadTargets(ctx context.Context, input switchplan.Input) ([]switchplan.Target, error) {
	if input.State == nil {
		return nil, apperror.New(apperror.PlanBuildFailed, "Codex plan requires store access")
	}
	credentialBindings, err := input.State.ListCredentialBindings(ctx, input.Profile.ID, codexconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex login bindings", err)
	}
	configBindings, err := input.State.ListConfigSetBindings(ctx, input.Profile.ID, codexconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex config bindings", err)
	}
	credentialRecords := make([]codexprofile.CredentialBindingRecord, 0, len(credentialBindings))
	for _, binding := range credentialBindings {
		credentialRecords = append(credentialRecords, codexprofile.CredentialBindingRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, SlotID: binding.SlotID, CredentialID: binding.CredentialID,
		})
	}
	configRecords := make([]codexprofile.ConfigSetBindingRecord, 0, len(configBindings))
	for _, binding := range configBindings {
		configRecords = append(configRecords, codexprofile.ConfigSetBindingRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, SlotID: binding.SlotID, ConfigSetID: binding.ConfigSetID,
		})
	}
	records, err := codexprofile.BindingTargetRecords(
		codexprofile.ProviderRecord{ID: input.Provider.ID, MetadataJSON: input.Provider.MetadataJSON},
		credentialRecords,
		configRecords,
	)
	if err != nil {
		return nil, err
	}
	targets := make([]switchplan.Target, 0, len(records))
	for _, record := range records {
		targets = append(targets, switchplan.Target{
			ProfileID: record.ProfileID, ProviderID: record.ProviderID, TargetID: record.TargetID,
			Path: record.Path, Format: record.Format, Strategy: record.Strategy,
			ValueJSON: record.ValueJSON, Enabled: true, MetadataJSON: record.MetadataJSON,
		})
	}
	return targets, nil
}

func (Adapter) ResolveTargetSpec(providerID, targetID, backendID, path, label string) (switchtarget.Spec, error) {
	if providerID != codexconfig.ProviderID {
		return nil, apperror.New(apperror.RecoveryUnsupported, "Codex recovery target has an incompatible Provider").WithDetail("provider_id", providerID)
	}
	if backendID != "" && backendID != switchtarget.BackendFile {
		return switchtarget.ResolveFileSpec(targetID, backendID, path, label)
	}
	recoveryMode := os.FileMode(0)
	if targetID == codexconfig.AuthTargetID {
		recoveryMode = 0o600
	}
	return switchtarget.FileSpec{
		ID: targetID, Path: path, NeedsContent: true, Secret: targetID == codexconfig.AuthTargetID, Label: label,
		EnforcedRecoveryMode: recoveryMode,
	}, nil
}

func (Adapter) Prepare(ctx context.Context, input switchplan.Input) (switchplan.Prepared, error) {
	if input.State == nil {
		return switchplan.Prepared{}, apperror.New(apperror.PlanBuildFailed, "Codex plan requires store access")
	}
	if input.Provider.ID != codexconfig.ProviderID || input.Provider.AdapterID != codexconfig.AdapterID {
		return switchplan.Prepared{}, apperror.New(apperror.CodexInvalid, "Codex plan adapter received an incompatible provider")
	}
	targets := map[string]switchplan.Target{}
	for _, target := range input.Targets {
		if err := codexprofile.ValidatePlanTargetRecord(
			codexprofile.ProviderRecord{ID: input.Provider.ID, MetadataJSON: input.Provider.MetadataJSON},
			codexTargetRecord(target),
		); err != nil {
			return switchplan.Prepared{}, err
		}
		targets[target.TargetID] = target
	}
	configTarget, hasConfig := targets[codexconfig.TargetID]
	authTarget, hasAuth := targets[codexconfig.AuthTargetID]
	if !hasConfig || !hasAuth || len(targets) != 2 {
		return switchplan.Prepared{}, apperror.New(apperror.CodexInvalid, "Codex profile must contain config and auth bindings only").
			WithDetail("profile_id", input.Profile.ID)
	}

	targetBindings, configResource, authResource, err := loadTargetResources(ctx, input.State, configTarget, authTarget)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	currentBindings, bindingWarnings, err := activeBindings(ctx, input.State)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	knownAuthPayloads, knownConfigHashes, err := loadKnownResourceContent(ctx, input.State)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	prepared := preparedPlan{
		ConfigTarget: configTarget, AuthTarget: authTarget,
		TargetBindings: targetBindings, CurrentBindings: currentBindings,
		ConfigResource: configResource, AuthResource: authResource,
		KnownAuthPayloads: knownAuthPayloads, KnownConfigHashes: knownConfigHashes,
		Warnings: bindingWarnings,
	}
	return switchplan.Prepared{
		Targets: []switchplan.PreparedTarget{
			{Spec: switchtarget.FileSpec{ID: authTarget.TargetID, Path: authTarget.Path, NeedsContent: true, Secret: true, Label: "Codex login"}},
			{Spec: switchtarget.FileSpec{ID: configTarget.TargetID, Path: configTarget.Path, NeedsContent: true, Label: "Codex settings"}},
		},
		Data: prepared,
	}, nil
}

func (Adapter) Finalize(ctx context.Context, input switchplan.Input, preparedResult switchplan.Prepared, snapshots map[string]switchtarget.Snapshot) (switchplan.Result, error) {
	prepared, ok := preparedResult.Data.(preparedPlan)
	if !ok {
		return switchplan.Result{}, apperror.New(apperror.PlanBuildFailed, "prepared Codex plan is invalid")
	}
	result := switchplan.Result{
		Operations: make([]switchplan.ApplyOperation, 0, 2),
		Warnings:   append([]string{}, prepared.Warnings...),
		Bindings: []switchplan.Binding{
			{TargetID: codexconfig.AuthTargetID, CurrentResourceID: prepared.CurrentBindings.CredentialID, TargetResourceID: prepared.TargetBindings.CredentialID, Changed: prepared.CurrentBindings.CredentialID != prepared.TargetBindings.CredentialID},
			{TargetID: codexconfig.TargetID, CurrentResourceID: prepared.CurrentBindings.ConfigSetID, TargetResourceID: prepared.TargetBindings.ConfigSetID, Changed: prepared.CurrentBindings.ConfigSetID != prepared.TargetBindings.ConfigSetID},
		},
	}

	authSpec := switchtarget.FileSpec{ID: prepared.AuthTarget.TargetID, Path: prepared.AuthTarget.Path, NeedsContent: true, Secret: true, Label: "Codex login"}
	authOp, capture, err := buildResourcePlanOperation(ctx, input, prepared.AuthTarget, prepared.CurrentBindings.CredentialID, prepared.AuthResource, authSpec, snapshots[codexconfig.AuthTargetID], prepared.KnownAuthPayloads, prepared.KnownConfigHashes)
	if err != nil {
		return switchplan.Result{}, err
	}
	result.Operations = append(result.Operations, authOp)
	result.Warnings = append(result.Warnings, authOp.Warnings...)
	if capture != nil {
		result.StateCaptures = append(result.StateCaptures, capture.Public)
		result.CredentialUpdates = append(result.CredentialUpdates, *capture.CredentialUpdate)
	}

	configSpec := switchtarget.FileSpec{ID: prepared.ConfigTarget.TargetID, Path: prepared.ConfigTarget.Path, NeedsContent: true, Label: "Codex settings"}
	configOp, capture, err := buildResourcePlanOperation(ctx, input, prepared.ConfigTarget, prepared.CurrentBindings.ConfigSetID, prepared.ConfigResource, configSpec, snapshots[codexconfig.TargetID], prepared.KnownAuthPayloads, prepared.KnownConfigHashes)
	if err != nil {
		return switchplan.Result{}, err
	}
	result.Operations = append(result.Operations, configOp)
	result.Warnings = append(result.Warnings, configOp.Warnings...)
	if capture != nil {
		result.StateCaptures = append(result.StateCaptures, capture.Public)
		result.ConfigSetUpdates = append(result.ConfigSetUpdates, *capture.ConfigSetUpdate)
	}
	result.Warnings = codexprofile.UniqueStrings(result.Warnings)
	return result, nil
}

func buildResourcePlanOperation(ctx context.Context, input switchplan.Input, target switchplan.Target, currentResourceID string, desired desiredResource, spec switchtarget.Spec, before switchtarget.Snapshot, knownAuthPayloads []string, knownConfigHashes map[string]struct{}) (switchplan.ApplyOperation, *pendingCapture, error) {
	op := switchplan.ApplyOperation{
		Operation: switchplan.Operation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID, TargetID: target.TargetID,
			BackendID: spec.BackendID(), TargetLabel: spec.SafeLabel(), Path: target.Path, Format: target.Format,
			Strategy: target.Strategy, LocatorFingerprint: spec.LocatorFingerprint(), Sensitive: spec.Sensitive(),
		},
		Spec: spec, Snapshot: before,
	}
	op.FileExists = before.Exists
	op.IsSymlink = before.IsSymlink
	op.BeforeMode = before.Mode
	if before.IsSymlink {
		op.Action = switchplan.ActionUnsupported
		op.StatusReason = switchplan.ReasonTargetIsSymlink
		op.Warnings = append(op.Warnings, "target path is a symlink and will not be followed")
		return op, nil, nil
	}
	if before.Exists {
		op.BeforeSHA256 = before.Fingerprint
		op.BeforePreview = switchplan.PreviewFromTarget(before.Preview)
		if target.TargetID == codexconfig.AuthTargetID {
			op.BeforePreview = switchplan.Preview{Content: codexpreset.AuthPreviewContent, Truncated: before.Preview.Truncated}
		}
	}

	currentContent, valid := validWorkingCopy(target.TargetID, before)
	if !valid {
		if before.Exists {
			op.Warnings = append(op.Warnings, "current Codex "+target.TargetID+" working copy is invalid and was not captured")
		} else {
			op.Warnings = append(op.Warnings, "current Codex "+target.TargetID+" working copy is missing and was not captured")
		}
	}
	capture, captureWarning, currentMatchesOtherKnown, err := buildPendingCapture(ctx, input.State, target.TargetID, currentResourceID, desired, currentContent, valid, knownAuthPayloads, knownConfigHashes)
	if err != nil {
		return switchplan.ApplyOperation{}, nil, err
	}
	if captureWarning != "" {
		op.Warnings = append(op.Warnings, captureWarning)
	}

	content := desired.Content
	currentMatchesDesired := valid && workingCopyMatchesDesired(target.TargetID, currentContent, switchtarget.SHA256String(currentContent), desired)
	if currentMatchesDesired || valid && currentResourceID != "" && currentResourceID == desired.ID && !currentMatchesOtherKnown {
		// An active shared binding is checked back in as the working copy; this
		// keeps formatting-only differences from causing unnecessary rewrites.
		content = currentContent
	}
	preview := switchplan.SensitivePreview(content)
	if target.TargetID == codexconfig.AuthTargetID {
		op.UseDesiredMode = true
		op.DesiredMode = 0o600
		preview = switchplan.Preview{Content: codexpreset.AuthPreviewContent}
	}
	if err := finishPlanOperation(&op, before, target, content, preview); err != nil {
		return switchplan.ApplyOperation{}, nil, err
	}
	op.PrivateBeforeFingerprint = before.Fingerprint
	op.PrivateDesiredFingerprint = switchtarget.SHA256String(content)
	return op, capture, nil
}

func validWorkingCopy(targetID string, before switchtarget.Snapshot) (string, bool) {
	if !before.Exists || before.IsSymlink {
		return "", false
	}
	switch targetID {
	case codexconfig.TargetID:
		if err := codexconfig.ValidateTOML(before.Content); err != nil {
			return "", false
		}
		return before.Content, true
	case codexconfig.AuthTargetID:
		payload, err := codexauth.NormalizePayload([]byte(before.Content))
		if err != nil {
			return "", false
		}
		return payload, true
	default:
		return "", false
	}
}

func buildPendingCapture(ctx context.Context, state switchplan.StateReader, targetID, currentResourceID string, desired desiredResource, currentContent string, valid bool, knownAuthPayloads []string, knownConfigHashes map[string]struct{}) (*pendingCapture, string, bool, error) {
	if !valid || currentResourceID == "" {
		return nil, "", false, nil
	}
	currentHash := switchtarget.SHA256String(currentContent)
	if workingCopyMatchesDesired(targetID, currentContent, currentHash, desired) {
		return nil, "", false, nil
	}
	switch targetID {
	case codexconfig.AuthTargetID:
		credential, err := requireAuthCredential(ctx, state, currentResourceID)
		if err != nil {
			return nil, "active Codex login resource is missing or invalid; auth working copy was not captured", false, nil
		}
		if currentHash == credential.PayloadSHA256 || codexprofile.AuthPayloadsEqual(currentContent, credential.PayloadJSON) {
			return nil, "", false, nil
		}
		if workingCopyMatchesKnown(targetID, currentContent, currentHash, knownAuthPayloads, knownConfigHashes) {
			return nil, "", true, nil
		}
		return &pendingCapture{
			Public: switchplan.StateCapture{ResourceKind: captureKindCredential, ResourceID: credential.ID, StoredSHA256: credential.PayloadSHA256, CurrentSHA256: currentHash},
			CredentialUpdate: &switchplan.CredentialUpdate{
				ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
				PayloadJSON: currentContent, PayloadSHA256: currentHash, MetadataJSON: credential.MetadataJSON,
			},
		}, "", false, nil
	case codexconfig.TargetID:
		configSet, err := requireConfigSet(ctx, state, currentResourceID)
		if err != nil {
			return nil, "active Codex config set is missing or invalid; config working copy was not captured", false, nil
		}
		if currentHash == configSet.PayloadSHA256 {
			return nil, "", false, nil
		}
		if workingCopyMatchesKnown(targetID, currentContent, currentHash, knownAuthPayloads, knownConfigHashes) {
			return nil, "", true, nil
		}
		return &pendingCapture{
			Public: switchplan.StateCapture{ResourceKind: captureKindConfigSet, ResourceID: configSet.ID, ResourceName: configSet.Name, StoredSHA256: configSet.PayloadSHA256, CurrentSHA256: currentHash},
			ConfigSetUpdate: &switchplan.ConfigSetUpdate{
				ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind, Name: configSet.Name,
				Description: configSet.Description, PayloadText: currentContent, PayloadSHA256: currentHash, MetadataJSON: configSet.MetadataJSON,
			},
		}, "", false, nil
	default:
		return nil, "", false, apperror.New(apperror.CodexInvalid, "unsupported Codex working copy target").WithDetail("target_id", targetID)
	}
}

func workingCopyMatchesKnown(targetID, currentContent, currentHash string, authPayloads []string, configHashes map[string]struct{}) bool {
	switch targetID {
	case codexconfig.AuthTargetID:
		for _, payload := range authPayloads {
			if codexprofile.AuthPayloadsEqual(currentContent, payload) {
				return true
			}
		}
	case codexconfig.TargetID:
		_, ok := configHashes[currentHash]
		return ok
	}
	return false
}

func workingCopyMatchesDesired(targetID, currentContent, currentHash string, desired desiredResource) bool {
	if currentHash == desired.SHA256 {
		return true
	}
	return targetID == codexconfig.AuthTargetID && codexprofile.AuthPayloadsEqual(currentContent, desired.Content)
}

func loadTargetResources(ctx context.Context, state switchplan.StateReader, configTarget, authTarget switchplan.Target) (codexprofile.Bindings, desiredResource, desiredResource, error) {
	configSetID, err := codexprofile.ConfigSetIDFromRecord(codexTargetRecord(configTarget))
	if err != nil {
		return codexprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	configSet, err := requireConfigSet(ctx, state, configSetID)
	if err != nil {
		return codexprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	credentialID, err := codexprofile.CredentialIDFromRecord(codexTargetRecord(authTarget))
	if err != nil {
		return codexprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	credential, err := requireAuthCredential(ctx, state, credentialID)
	if err != nil {
		return codexprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	return codexprofile.Bindings{ConfigSetID: configSetID, CredentialID: credentialID},
		desiredResource{ID: configSet.ID, Name: configSet.Name, Content: configSet.PayloadText, SHA256: configSet.PayloadSHA256},
		desiredResource{ID: credential.ID, Content: credential.PayloadJSON, SHA256: credential.PayloadSHA256}, nil
}

func finishPlanOperation(op *switchplan.ApplyOperation, before switchtarget.Snapshot, target switchplan.Target, content string, preview switchplan.Preview) error {
	if len(content) > switchplan.MaxTargetContentBytes {
		return apperror.New(apperror.TargetInvalid, "desired target content is too large").
			WithDetail("target_id", target.TargetID).
			WithDetail("path", target.Path).
			WithDetail("size_bytes", len(content)).
			WithDetail("max_bytes", switchplan.MaxTargetContentBytes)
	}
	op.DesiredContent = content
	op.DesiredSHA256 = switchtarget.SHA256String(content)
	op.DesiredPreview = preview
	op.AfterPreview = preview
	if !before.Exists {
		op.Action = switchplan.ActionCreate
		op.StatusReason = switchplan.ReasonTargetMissing
		return nil
	}
	if op.BeforeSHA256 == op.DesiredSHA256 {
		op.Action = switchplan.ActionNoop
		op.StatusReason = switchplan.ReasonTargetSameContent
		return nil
	}
	op.Action = switchplan.ActionUpdate
	op.StatusReason = switchplan.ReasonTargetDifferentContent
	return nil
}

func codexTargetRecord(target switchplan.Target) codexprofile.TargetRecord {
	return codexprofile.TargetRecord{
		ProfileID: target.ProfileID, ProviderID: target.ProviderID, TargetID: target.TargetID,
		Path: target.Path, Format: target.Format, Strategy: target.Strategy,
		ValueJSON: target.ValueJSON, MetadataJSON: target.MetadataJSON,
	}
}

func requireAuthCredential(ctx context.Context, state switchplan.StateReader, credentialID string) (switchplan.Credential, error) {
	credential, err := state.GetCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, switchplan.ErrStateNotFound) {
			return switchplan.Credential{}, apperror.New(apperror.CodexInvalid, "Codex auth credential not found")
		}
		return switchplan.Credential{}, apperror.Wrap(apperror.StoreStatusFailed, "Codex auth credential store operation failed", err)
	}
	if err := codexprofile.ValidateCredentialRecord(codexprofile.CredentialRecord{
		ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
		PayloadJSON: credential.PayloadJSON, PayloadSHA256: credential.PayloadSHA256, MetadataJSON: credential.MetadataJSON,
	}); err != nil {
		return switchplan.Credential{}, err
	}
	return credential, nil
}

func requireConfigSet(ctx context.Context, state switchplan.StateReader, configSetID string) (switchplan.ConfigSet, error) {
	configSet, err := state.GetConfigSet(ctx, configSetID)
	if err != nil {
		if errors.Is(err, switchplan.ErrStateNotFound) {
			return switchplan.ConfigSet{}, apperror.New(apperror.CodexInvalid, "Codex config set not found")
		}
		return switchplan.ConfigSet{}, apperror.Wrap(apperror.StoreStatusFailed, "Codex config set store operation failed", err)
	}
	if err := codexprofile.ValidateConfigSetRecord(codexprofile.ConfigSetRecord{
		ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind,
		Name: configSet.Name, Description: configSet.Description, PayloadText: configSet.PayloadText,
		PayloadSHA256: configSet.PayloadSHA256, MetadataJSON: configSet.MetadataJSON,
	}); err != nil {
		return switchplan.ConfigSet{}, err
	}
	return configSet, nil
}

func activeBindings(ctx context.Context, state switchplan.StateReader) (codexprofile.Bindings, []string, error) {
	active, err := state.GetActiveState(ctx, codexconfig.ProviderID)
	if errors.Is(err, switchplan.ErrStateNotFound) {
		return codexprofile.Bindings{}, nil, nil
	}
	if err != nil {
		return codexprofile.Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex profile state", err)
	}
	credentialBindings, err := state.ListCredentialBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return codexprofile.Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex login bindings", err)
	}
	configBindings, err := state.ListConfigSetBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return codexprofile.Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex config bindings", err)
	}
	bindings := codexprofile.Bindings{}
	warnings := []string{}
	unsupportedCredentials := false
	for _, binding := range credentialBindings {
		if binding.SlotID == codexpreset.CredentialSlotAuth && bindings.CredentialID == "" {
			bindings.CredentialID = binding.CredentialID
			continue
		}
		unsupportedCredentials = true
	}
	if unsupportedCredentials {
		bindings.CredentialID = ""
		warnings = append(warnings, "active Codex login bindings are unsupported; auth working copy will not be captured")
	}
	unsupportedConfigSets := false
	for _, binding := range configBindings {
		if binding.SlotID == codexpreset.ConfigSetSlotUserConfig && bindings.ConfigSetID == "" {
			bindings.ConfigSetID = binding.ConfigSetID
			continue
		}
		unsupportedConfigSets = true
	}
	if unsupportedConfigSets {
		bindings.ConfigSetID = ""
		warnings = append(warnings, "active Codex config bindings are unsupported; config working copy will not be captured")
	}
	if bindings.ConfigSetID == "" {
		warnings = append(warnings, "active Codex config binding is missing; config working copy will not be captured")
	}
	if bindings.CredentialID == "" {
		warnings = append(warnings, "active Codex login binding is missing; auth working copy will not be captured")
	}
	return bindings, codexprofile.UniqueStrings(warnings), nil
}

func loadKnownResourceContent(ctx context.Context, state switchplan.StateReader) ([]string, map[string]struct{}, error) {
	credentials, err := state.ListCredentials(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex login resources", err)
	}
	authPayloads := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		valid, credentialErr := requireAuthCredential(ctx, state, credential.ID)
		if credentialErr == nil {
			authPayloads = append(authPayloads, valid.PayloadJSON)
		}
	}
	configSets, err := state.ListConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex config resources", err)
	}
	configHashes := make(map[string]struct{}, len(configSets))
	for _, configSet := range configSets {
		valid, configErr := requireConfigSet(ctx, state, configSet.ID)
		if configErr == nil {
			configHashes[valid.PayloadSHA256] = struct{}{}
		}
	}
	return authPayloads, configHashes, nil
}

var _ switchplan.Adapter = Adapter{}
