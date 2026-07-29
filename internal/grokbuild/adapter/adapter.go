package adapter

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	grokauth "github.com/strahe/profiledeck/internal/grokbuild/auth"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	captureKindCredential = "credential"
	captureKindConfigSet  = "config-set"

	activeSessionWarning = "finish active Grok Build sessions before applying file changes"
)

// Adapter builds Grok Build switch plans without exposing managed file
// contents through public plan fields.
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
	TargetBindings    grokprofile.Bindings
	CurrentBindings   grokprofile.Bindings
	ConfigResource    desiredResource
	AuthResource      desiredResource
	KnownAuthHashes   map[string]struct{}
	KnownConfigHashes map[string]struct{}
	Warnings          []string
}

type pendingCapture struct {
	Public           switchplan.StateCapture
	CredentialUpdate *switchplan.CredentialUpdate
	ConfigSetUpdate  *switchplan.ConfigSetUpdate
}

func (Adapter) ID() string { return grokconfig.AdapterID }

func (Adapter) ManagedProviderIDs() []string { return []string{grokconfig.ProviderID} }

func (Adapter) LoadTargets(ctx context.Context, input switchplan.Input) ([]switchplan.Target, error) {
	if input.State == nil {
		return nil, apperror.New(apperror.PlanBuildFailed, "Grok Build plan requires store access")
	}
	credentialBindings, err := input.State.ListCredentialBindings(ctx, input.Profile.ID, grokconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build login bindings", err)
	}
	configBindings, err := input.State.ListConfigSetBindings(ctx, input.Profile.ID, grokconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build Config Set bindings", err)
	}
	credentialRecords := make([]grokprofile.CredentialBindingRecord, 0, len(credentialBindings))
	for _, binding := range credentialBindings {
		credentialRecords = append(credentialRecords, grokprofile.CredentialBindingRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID,
			SlotID: binding.SlotID, CredentialID: binding.CredentialID,
		})
	}
	configRecords := make([]grokprofile.ConfigSetBindingRecord, 0, len(configBindings))
	for _, binding := range configBindings {
		configRecords = append(configRecords, grokprofile.ConfigSetBindingRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID,
			SlotID: binding.SlotID, ConfigSetID: binding.ConfigSetID,
		})
	}
	records, err := grokprofile.BindingTargetRecords(
		grokprofile.ProviderRecord{ID: input.Provider.ID, MetadataJSON: input.Provider.MetadataJSON},
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
	if providerID != grokconfig.ProviderID {
		return nil, apperror.New(apperror.RecoveryUnsupported, "Grok Build recovery target has an incompatible Provider")
	}
	if backendID != switchtarget.BackendFile {
		return nil, apperror.New(apperror.RecoveryUnsupported, "Grok Build recovery backend is unsupported").
			WithDetail("backend_id", backendID)
	}
	recoveryMode := os.FileMode(0)
	switch targetID {
	case grokconfig.AuthTargetID:
		recoveryMode = 0o600
	case grokconfig.ConfigTargetID:
	default:
		return nil, apperror.New(apperror.RecoveryUnsupported, "Grok Build recovery target is unsupported").
			WithDetail("target_id", targetID)
	}
	return switchtarget.FileSpec{
		ID: targetID, Path: path, NeedsContent: true, Secret: true, Label: label,
		EnforcedRecoveryMode: recoveryMode,
	}, nil
}

func (Adapter) Prepare(ctx context.Context, input switchplan.Input) (switchplan.Prepared, error) {
	if input.State == nil {
		return switchplan.Prepared{}, apperror.New(apperror.PlanBuildFailed, "Grok Build plan requires store access")
	}
	if grokconfig.UnsupportedAuthEnvironment() {
		return switchplan.Prepared{}, apperror.New(
			apperror.GrokBuildInvalid,
			"Grok Build uses a custom authentication source; unset GROK_AUTH and GROK_AUTH_PATH before switching profiles",
		)
	}
	if input.Provider.ID != grokconfig.ProviderID || input.Provider.AdapterID != grokconfig.AdapterID {
		return switchplan.Prepared{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build plan received an incompatible Provider")
	}
	targets := make(map[string]switchplan.Target, len(input.Targets))
	for _, target := range input.Targets {
		if err := grokprofile.ValidatePlanTargetRecord(
			grokprofile.ProviderRecord{ID: input.Provider.ID, MetadataJSON: input.Provider.MetadataJSON},
			targetRecord(target),
		); err != nil {
			return switchplan.Prepared{}, err
		}
		targets[target.TargetID] = target
	}
	configTarget, hasConfig := targets[grokconfig.ConfigTargetID]
	authTarget, hasAuth := targets[grokconfig.AuthTargetID]
	if !hasConfig || !hasAuth || len(targets) != 2 {
		return switchplan.Prepared{}, apperror.New(
			apperror.GrokBuildInvalid,
			"Grok Build Profile must contain one login and one Config Set",
		).WithDetail("profile_id", input.Profile.ID)
	}

	targetBindings, configResource, authResource, err := loadTargetResources(ctx, input.State, configTarget, authTarget)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	currentBindings, warnings, err := activeBindings(ctx, input.State)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	knownAuthHashes, knownConfigHashes, err := loadKnownResourceHashes(ctx, input.State)
	if err != nil {
		return switchplan.Prepared{}, err
	}
	prepared := preparedPlan{
		ConfigTarget: configTarget, AuthTarget: authTarget,
		TargetBindings: targetBindings, CurrentBindings: currentBindings,
		ConfigResource: configResource, AuthResource: authResource,
		KnownAuthHashes: knownAuthHashes, KnownConfigHashes: knownConfigHashes,
		Warnings: warnings,
	}
	return switchplan.Prepared{
		Targets: []switchplan.PreparedTarget{
			{Spec: authSpec(authTarget)},
			{Spec: configSpec(configTarget)},
		},
		Data: prepared,
	}, nil
}

func (Adapter) Finalize(
	ctx context.Context,
	input switchplan.Input,
	preparedResult switchplan.Prepared,
	snapshots map[string]switchtarget.Snapshot,
) (switchplan.Result, error) {
	prepared, ok := preparedResult.Data.(preparedPlan)
	if !ok {
		return switchplan.Result{}, apperror.New(apperror.PlanBuildFailed, "prepared Grok Build plan is invalid")
	}
	result := switchplan.Result{
		Operations: make([]switchplan.ApplyOperation, 0, 2),
		Warnings:   append([]string(nil), prepared.Warnings...),
		Bindings: []switchplan.Binding{
			{
				TargetID:          grokconfig.AuthTargetID,
				CurrentResourceID: prepared.CurrentBindings.CredentialID,
				TargetResourceID:  prepared.TargetBindings.CredentialID,
				Changed:           prepared.CurrentBindings.CredentialID != prepared.TargetBindings.CredentialID,
			},
			{
				TargetID:          grokconfig.ConfigTargetID,
				CurrentResourceID: prepared.CurrentBindings.ConfigSetID,
				TargetResourceID:  prepared.TargetBindings.ConfigSetID,
				Changed:           prepared.CurrentBindings.ConfigSetID != prepared.TargetBindings.ConfigSetID,
			},
		},
	}

	authOperation, capture, err := buildResourceOperation(
		ctx, input, prepared.AuthTarget, prepared.CurrentBindings.CredentialID,
		prepared.AuthResource, authSpec(prepared.AuthTarget), snapshots[grokconfig.AuthTargetID],
		prepared.KnownAuthHashes,
	)
	if err != nil {
		return switchplan.Result{}, err
	}
	result.Operations = append(result.Operations, authOperation)
	result.Warnings = append(result.Warnings, authOperation.Warnings...)
	appendCapture(&result, capture)

	configOperation, capture, err := buildResourceOperation(
		ctx, input, prepared.ConfigTarget, prepared.CurrentBindings.ConfigSetID,
		prepared.ConfigResource, configSpec(prepared.ConfigTarget), snapshots[grokconfig.ConfigTargetID],
		prepared.KnownConfigHashes,
	)
	if err != nil {
		return switchplan.Result{}, err
	}
	result.Operations = append(result.Operations, configOperation)
	result.Warnings = append(result.Warnings, configOperation.Warnings...)
	appendCapture(&result, capture)
	result.Warnings = grokprofile.UniqueStrings(result.Warnings)
	return result, nil
}

func authSpec(target switchplan.Target) switchtarget.FileSpec {
	return switchtarget.FileSpec{
		ID: target.TargetID, Path: target.Path, NeedsContent: true, Secret: true,
		Label: "Grok Build login", EnforcedRecoveryMode: 0o600,
	}
}

func configSpec(target switchplan.Target) switchtarget.FileSpec {
	return switchtarget.FileSpec{
		ID: target.TargetID, Path: target.Path, NeedsContent: true, Secret: true,
		Label: "Grok Build settings",
	}
}

func appendCapture(result *switchplan.Result, capture *pendingCapture) {
	if capture == nil {
		return
	}
	result.StateCaptures = append(result.StateCaptures, capture.Public)
	if capture.CredentialUpdate != nil {
		result.CredentialUpdates = append(result.CredentialUpdates, *capture.CredentialUpdate)
	}
	if capture.ConfigSetUpdate != nil {
		result.ConfigSetUpdates = append(result.ConfigSetUpdates, *capture.ConfigSetUpdate)
	}
}

func buildResourceOperation(
	ctx context.Context,
	input switchplan.Input,
	target switchplan.Target,
	currentResourceID string,
	desired desiredResource,
	spec switchtarget.FileSpec,
	before switchtarget.Snapshot,
	knownHashes map[string]struct{},
) (switchplan.ApplyOperation, *pendingCapture, error) {
	operation := switchplan.ApplyOperation{
		Operation: switchplan.Operation{
			ProviderID: input.Provider.ID, ProfileID: input.Profile.ID, TargetID: target.TargetID,
			BackendID: spec.BackendID(), TargetLabel: spec.SafeLabel(), Path: target.Path,
			Format: target.Format, Strategy: target.Strategy,
			LocatorFingerprint: spec.LocatorFingerprint(), Sensitive: true,
			FileExists: before.Exists, IsSymlink: before.IsSymlink,
		},
		Spec: spec, Snapshot: before, BeforeMode: before.Mode,
	}
	if before.IsSymlink {
		operation.Action = switchplan.ActionUnsupported
		operation.StatusReason = switchplan.ReasonTargetIsSymlink
		operation.Warnings = append(operation.Warnings, "Grok Build target is a symlink and will not be followed")
		return operation, nil, nil
	}
	if before.Exists {
		operation.BeforeSHA256 = before.Fingerprint
	}

	currentContent, valid := validWorkingCopy(target.TargetID, before)
	if !valid {
		if before.Exists {
			operation.Warnings = append(operation.Warnings, "current Grok Build working copy is invalid and was not saved")
		} else {
			operation.Warnings = append(operation.Warnings, "current Grok Build working copy is missing and was not saved")
		}
	}
	capture, captureWarning, matchesKnown, err := buildPendingCapture(
		ctx, input.State, target.TargetID, currentResourceID, currentContent, valid, knownHashes,
	)
	if err != nil {
		return switchplan.ApplyOperation{}, nil, err
	}
	if captureWarning != "" {
		operation.Warnings = append(operation.Warnings, captureWarning)
	}

	content := desired.Content
	currentHash := switchtarget.SHA256String(currentContent)
	if valid && (currentHash == desired.SHA256 ||
		currentResourceID != "" && currentResourceID == desired.ID && !matchesKnown) {
		content = currentContent
	}
	if len(content) > switchplan.MaxTargetContentBytes {
		return switchplan.ApplyOperation{}, nil, apperror.New(
			apperror.TargetInvalid,
			"Grok Build target content is too large",
		).WithDetail("target_id", target.TargetID)
	}
	operation.DesiredContent = content
	operation.DesiredSHA256 = switchtarget.SHA256String(content)
	operation.PrivateBeforeFingerprint = before.Fingerprint
	operation.PrivateDesiredFingerprint = operation.DesiredSHA256
	if target.TargetID == grokconfig.AuthTargetID {
		operation.UseDesiredMode = true
		operation.DesiredMode = 0o600
	}
	switch {
	case !before.Exists:
		operation.Action = switchplan.ActionCreate
		operation.StatusReason = switchplan.ReasonTargetMissing
	case before.Fingerprint == operation.DesiredSHA256:
		operation.Action = switchplan.ActionNoop
		operation.StatusReason = switchplan.ReasonTargetSameContent
	default:
		operation.Action = switchplan.ActionUpdate
		operation.StatusReason = switchplan.ReasonTargetDifferentContent
	}
	if operation.Action != switchplan.ActionNoop {
		operation.Warnings = append(operation.Warnings, activeSessionWarning)
	}
	if target.TargetID == grokconfig.ConfigTargetID && grokconfig.HasAuthOverride(content) {
		operation.Warnings = append(operation.Warnings, grokconfig.AuthOverrideWarning)
	}
	// Both managed files are sensitive. Public plan consumers get actions,
	// paths, labels, and warnings, but never file content or excerpts.
	operation.BeforePreview = switchplan.Preview{}
	operation.DesiredPreview = switchplan.Preview{}
	operation.AfterPreview = switchplan.Preview{}
	return operation, capture, nil
}

func validWorkingCopy(targetID string, snapshot switchtarget.Snapshot) (string, bool) {
	if !snapshot.Exists || snapshot.IsSymlink {
		return "", false
	}
	switch targetID {
	case grokconfig.AuthTargetID:
		payload, err := grokauth.Validate([]byte(snapshot.Content))
		return payload, err == nil
	case grokconfig.ConfigTargetID:
		return snapshot.Content, grokconfig.ValidateTOML(snapshot.Content) == nil
	default:
		return "", false
	}
}

func buildPendingCapture(
	ctx context.Context,
	state switchplan.StateReader,
	targetID string,
	currentResourceID string,
	currentContent string,
	valid bool,
	knownHashes map[string]struct{},
) (*pendingCapture, string, bool, error) {
	if !valid || currentResourceID == "" {
		return nil, "", false, nil
	}
	currentHash := switchtarget.SHA256String(currentContent)
	resourceHash, capture, err := captureForResource(ctx, state, targetID, currentResourceID, currentContent, currentHash)
	if err != nil {
		return nil, "active Grok Build resource is missing or invalid; working copy was not saved", false, nil
	}
	if currentHash == resourceHash {
		return nil, "", false, nil
	}
	if _, matches := knownHashes[currentHash]; matches {
		return nil, "", true, nil
	}
	return capture, "", false, nil
}

func captureForResource(
	ctx context.Context,
	state switchplan.StateReader,
	targetID string,
	resourceID string,
	content string,
	hash string,
) (string, *pendingCapture, error) {
	switch targetID {
	case grokconfig.AuthTargetID:
		credential, err := requireAuthCredential(ctx, state, resourceID)
		if err != nil {
			return "", nil, err
		}
		return credential.PayloadSHA256, &pendingCapture{
			Public: switchplan.StateCapture{
				ResourceKind: captureKindCredential, ResourceID: credential.ID,
				StoredSHA256: credential.PayloadSHA256, CurrentSHA256: hash, Changed: true,
			},
			CredentialUpdate: &switchplan.CredentialUpdate{
				ID: credential.ID, ProviderID: credential.ProviderID,
				CredentialKind: credential.CredentialKind, PayloadJSON: content,
				PayloadSHA256: hash, MetadataJSON: credential.MetadataJSON,
			},
		}, nil
	case grokconfig.ConfigTargetID:
		configSet, err := requireConfigSet(ctx, state, resourceID)
		if err != nil {
			return "", nil, err
		}
		return configSet.PayloadSHA256, &pendingCapture{
			Public: switchplan.StateCapture{
				ResourceKind: captureKindConfigSet, ResourceID: configSet.ID,
				ResourceName: configSet.Name, StoredSHA256: configSet.PayloadSHA256,
				CurrentSHA256: hash, Changed: true,
			},
			ConfigSetUpdate: &switchplan.ConfigSetUpdate{
				ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind,
				Name: configSet.Name, Description: configSet.Description, PayloadText: content,
				PayloadSHA256: hash, MetadataJSON: configSet.MetadataJSON,
			},
		}, nil
	default:
		return "", nil, apperror.New(apperror.GrokBuildInvalid, "unsupported Grok Build working copy")
	}
}

func loadTargetResources(
	ctx context.Context,
	state switchplan.StateReader,
	configTarget switchplan.Target,
	authTarget switchplan.Target,
) (grokprofile.Bindings, desiredResource, desiredResource, error) {
	configSetID, err := grokprofile.ConfigSetIDFromRecord(targetRecord(configTarget))
	if err != nil {
		return grokprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	configSet, err := requireConfigSet(ctx, state, configSetID)
	if err != nil {
		return grokprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	credentialID, err := grokprofile.CredentialIDFromRecord(targetRecord(authTarget))
	if err != nil {
		return grokprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	credential, err := requireAuthCredential(ctx, state, credentialID)
	if err != nil {
		return grokprofile.Bindings{}, desiredResource{}, desiredResource{}, err
	}
	return grokprofile.Bindings{ConfigSetID: configSetID, CredentialID: credentialID},
		desiredResource{
			ID: configSet.ID, Name: configSet.Name,
			Content: configSet.PayloadText, SHA256: configSet.PayloadSHA256,
		},
		desiredResource{
			ID: credential.ID, Content: credential.PayloadJSON, SHA256: credential.PayloadSHA256,
		}, nil
}

func activeBindings(ctx context.Context, state switchplan.StateReader) (grokprofile.Bindings, []string, error) {
	active, err := state.GetActiveState(ctx, grokconfig.ProviderID)
	if errors.Is(err, switchplan.ErrStateNotFound) {
		return grokprofile.Bindings{}, nil, nil
	}
	if err != nil {
		return grokprofile.Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Grok Build Profile", err)
	}
	credentials, err := state.ListCredentialBindings(ctx, active.ProfileID, grokconfig.ProviderID)
	if err != nil {
		return grokprofile.Bindings{}, nil, err
	}
	configSets, err := state.ListConfigSetBindings(ctx, active.ProfileID, grokconfig.ProviderID)
	if err != nil {
		return grokprofile.Bindings{}, nil, err
	}
	bindings := grokprofile.Bindings{}
	warnings := []string{}
	for _, binding := range credentials {
		if binding.SlotID == grokpreset.CredentialSlotAuth && bindings.CredentialID == "" {
			bindings.CredentialID = binding.CredentialID
		} else {
			bindings.CredentialID = ""
			warnings = append(warnings, "active Grok Build login binding is unsupported; working copy will not be saved")
			break
		}
	}
	for _, binding := range configSets {
		if binding.SlotID == grokpreset.ConfigSetSlotUserConfig && bindings.ConfigSetID == "" {
			bindings.ConfigSetID = binding.ConfigSetID
		} else {
			bindings.ConfigSetID = ""
			warnings = append(warnings, "active Grok Build Config Set binding is unsupported; working copy will not be saved")
			break
		}
	}
	if bindings.CredentialID == "" {
		warnings = append(warnings, "active Grok Build login binding is missing; working copy will not be saved")
	}
	if bindings.ConfigSetID == "" {
		warnings = append(warnings, "active Grok Build Config Set binding is missing; working copy will not be saved")
	}
	return bindings, grokprofile.UniqueStrings(warnings), nil
}

func loadKnownResourceHashes(
	ctx context.Context,
	state switchplan.StateReader,
) (map[string]struct{}, map[string]struct{}, error) {
	credentials, err := state.ListCredentials(ctx, grokconfig.ProviderID)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build login resources", err)
	}
	authHashes := make(map[string]struct{}, len(credentials))
	for _, credential := range credentials {
		if validated, validateErr := requireAuthCredential(ctx, state, credential.ID); validateErr == nil {
			authHashes[validated.PayloadSHA256] = struct{}{}
		}
	}
	configSets, err := state.ListConfigSets(ctx, grokconfig.ProviderID, grokpreset.ConfigSetKindTOML)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build Config Sets", err)
	}
	configHashes := make(map[string]struct{}, len(configSets))
	for _, configSet := range configSets {
		if validated, validateErr := requireConfigSet(ctx, state, configSet.ID); validateErr == nil {
			configHashes[validated.PayloadSHA256] = struct{}{}
		}
	}
	return authHashes, configHashes, nil
}

func requireAuthCredential(
	ctx context.Context,
	state switchplan.StateReader,
	credentialID string,
) (switchplan.Credential, error) {
	credential, err := state.GetCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, switchplan.ErrStateNotFound) {
			return switchplan.Credential{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build login was not found")
		}
		return switchplan.Credential{}, err
	}
	if err := grokprofile.ValidateCredentialRecord(grokprofile.CredentialRecord{
		ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
		PayloadJSON: credential.PayloadJSON, PayloadSHA256: credential.PayloadSHA256,
		MetadataJSON: credential.MetadataJSON,
	}); err != nil {
		return switchplan.Credential{}, err
	}
	return credential, nil
}

func requireConfigSet(
	ctx context.Context,
	state switchplan.StateReader,
	configSetID string,
) (switchplan.ConfigSet, error) {
	configSet, err := state.GetConfigSet(ctx, grokconfig.ProviderID, configSetID)
	if err != nil {
		if errors.Is(err, switchplan.ErrStateNotFound) {
			return switchplan.ConfigSet{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set was not found")
		}
		return switchplan.ConfigSet{}, err
	}
	if err := grokprofile.ValidateConfigSetRecord(grokprofile.ConfigSetRecord{
		ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind,
		Name: configSet.Name, Description: configSet.Description, PayloadText: configSet.PayloadText,
		PayloadSHA256: configSet.PayloadSHA256, MetadataJSON: configSet.MetadataJSON,
	}); err != nil {
		return switchplan.ConfigSet{}, err
	}
	return configSet, nil
}

func targetRecord(target switchplan.Target) grokprofile.TargetRecord {
	return grokprofile.TargetRecord{
		ProfileID: target.ProfileID, ProviderID: target.ProviderID, TargetID: target.TargetID,
		Path: target.Path, Format: target.Format, Strategy: target.Strategy,
		ValueJSON: target.ValueJSON, MetadataJSON: target.MetadataJSON,
	}
}

var _ switchplan.Adapter = Adapter{}
