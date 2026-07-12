package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	codexCaptureKindCredential = "credential"
	codexCaptureKindConfigSet  = "config-set"
)

type codexPlanAdapter struct{}

type codexPlanBindings struct {
	ConfigSetID  string
	CredentialID string
}

type codexDesiredResource struct {
	ID           string
	Name         string
	Content      string
	SHA256       string
	MetadataJSON string
	ConfigSet    *store.ProviderConfigSet
	Credential   *store.ProviderCredential
}

type codexPreparedPlan struct {
	ConfigTarget      store.ProfileTarget
	AuthTarget        store.ProfileTarget
	TargetBindings    codexPlanBindings
	CurrentBindings   codexPlanBindings
	ConfigResource    codexDesiredResource
	AuthResource      codexDesiredResource
	KnownAuthPayloads []string
	KnownConfigHashes map[string]struct{}
	Warnings          []string
}

func (codexPlanAdapter) ID() string {
	return codexconfig.AdapterID
}

func (codexPlanAdapter) ResolveTargetSpec(providerID, targetID, backendID, path, label string) (targetSpec, error) {
	if providerID != codexconfig.ProviderID {
		return nil, NewError(ErrorRollbackUnsupported, "Codex recovery target has an incompatible Provider").WithDetail("provider_id", providerID)
	}
	return resolveFileTargetSpec(targetID, backendID, path, label)
}

func (codexPlanAdapter) Prepare(ctx context.Context, input planAdapterInput) (planAdapterPrepared, error) {
	if input.Store == nil {
		return planAdapterPrepared{}, NewError(ErrorPlanBuildFailed, "Codex plan requires store access")
	}
	if input.Provider.ID != codexconfig.ProviderID || input.Provider.AdapterID != codexconfig.AdapterID {
		return planAdapterPrepared{}, NewError(ErrorCodexInvalid, "Codex plan adapter received an incompatible provider")
	}
	targets := map[string]store.ProfileTarget{}
	for _, target := range input.Targets {
		if appErr := validateCodexPlanTarget(input.Provider, target); appErr != nil {
			return planAdapterPrepared{}, appErr
		}
		targets[target.TargetID] = target
	}
	configTarget, hasConfig := targets[codexconfig.TargetID]
	authTarget, hasAuth := targets[codexconfig.AuthTargetID]
	if !hasConfig || !hasAuth || len(targets) != 2 {
		return planAdapterPrepared{}, NewError(ErrorCodexInvalid, "Codex profile must contain config and auth bindings only").
			WithDetail("profile_id", input.Profile.ID)
	}

	targetBindings, configResource, authResource, err := loadCodexTargetResources(ctx, input.Store, configTarget, authTarget)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	currentBindings, bindingWarnings, err := activeCodexPlanBindings(ctx, input.Store)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	knownAuthPayloads, knownConfigHashes, err := loadKnownCodexResourceContent(ctx, input.Store)
	if err != nil {
		return planAdapterPrepared{}, err
	}
	prepared := codexPreparedPlan{
		ConfigTarget: configTarget, AuthTarget: authTarget,
		TargetBindings: targetBindings, CurrentBindings: currentBindings,
		ConfigResource: configResource, AuthResource: authResource,
		KnownAuthPayloads: knownAuthPayloads, KnownConfigHashes: knownConfigHashes,
		Warnings: bindingWarnings,
	}
	return planAdapterPrepared{
		Targets: []preparedTarget{
			{Spec: fileTargetSpec{ID: authTarget.TargetID, Path: authTarget.Path, NeedsContent: true, Secret: true, Label: "Codex login"}},
			{Spec: fileTargetSpec{ID: configTarget.TargetID, Path: configTarget.Path, NeedsContent: true, Label: "Codex settings"}},
		},
		Data: prepared,
	}, nil
}

func (codexPlanAdapter) Finalize(ctx context.Context, input planAdapterInput, preparedResult planAdapterPrepared, snapshots map[string]targetSnapshot) (planAdapterResult, error) {
	prepared, ok := preparedResult.Data.(codexPreparedPlan)
	if !ok {
		return planAdapterResult{}, NewError(ErrorPlanBuildFailed, "prepared Codex plan is invalid")
	}
	result := planAdapterResult{
		Operations: make([]applyPlanOperation, 0, 2),
		Warnings:   append([]string{}, prepared.Warnings...),
		Bindings: []PlanBinding{
			{TargetID: codexconfig.AuthTargetID, CurrentResourceID: prepared.CurrentBindings.CredentialID, TargetResourceID: prepared.TargetBindings.CredentialID, Changed: prepared.CurrentBindings.CredentialID != prepared.TargetBindings.CredentialID},
			{TargetID: codexconfig.TargetID, CurrentResourceID: prepared.CurrentBindings.ConfigSetID, TargetResourceID: prepared.TargetBindings.ConfigSetID, Changed: prepared.CurrentBindings.ConfigSetID != prepared.TargetBindings.ConfigSetID},
		},
	}

	authSpec := fileTargetSpec{ID: prepared.AuthTarget.TargetID, Path: prepared.AuthTarget.Path, NeedsContent: true, Secret: true, Label: "Codex login"}
	authOp, capture, err := buildCodexResourcePlanOperation(ctx, input, prepared.AuthTarget, prepared.CurrentBindings.CredentialID, prepared.AuthResource, authSpec, snapshots[codexconfig.AuthTargetID], prepared.KnownAuthPayloads, prepared.KnownConfigHashes)
	if err != nil {
		return planAdapterResult{}, err
	}
	result.Operations = append(result.Operations, authOp)
	result.Warnings = append(result.Warnings, authOp.Warnings...)
	if capture != nil {
		result.StateCaptures = append(result.StateCaptures, capture.Public)
		result.CredentialUpdates = append(result.CredentialUpdates, *capture.CredentialUpdate)
	}

	configSpec := fileTargetSpec{ID: prepared.ConfigTarget.TargetID, Path: prepared.ConfigTarget.Path, NeedsContent: true, Label: "Codex settings"}
	configOp, capture, err := buildCodexResourcePlanOperation(ctx, input, prepared.ConfigTarget, prepared.CurrentBindings.ConfigSetID, prepared.ConfigResource, configSpec, snapshots[codexconfig.TargetID], prepared.KnownAuthPayloads, prepared.KnownConfigHashes)
	if err != nil {
		return planAdapterResult{}, err
	}
	result.Operations = append(result.Operations, configOp)
	result.Warnings = append(result.Warnings, configOp.Warnings...)
	if capture != nil {
		result.StateCaptures = append(result.StateCaptures, capture.Public)
		result.ConfigSetUpdates = append(result.ConfigSetUpdates, *capture.ConfigSetUpdate)
	}
	result.Warnings = uniqueStrings(result.Warnings)
	return result, nil
}

type codexPendingCapture struct {
	Public           StateCapture
	CredentialUpdate *store.UpsertProviderCredentialParams
	ConfigSetUpdate  *store.UpsertProviderConfigSetParams
}

func buildCodexResourcePlanOperation(ctx context.Context, input planAdapterInput, target store.ProfileTarget, currentResourceID string, desired codexDesiredResource, spec targetSpec, before targetSnapshot, knownAuthPayloads []string, knownConfigHashes map[string]struct{}) (applyPlanOperation, *codexPendingCapture, error) {
	op := applyPlanOperation{PlanOperation: PlanOperation{
		ProviderID: input.Provider.ID, ProfileID: input.Profile.ID, TargetID: target.TargetID,
		BackendID: spec.BackendID(), TargetLabel: spec.SafeLabel(),
		Path: target.Path, Format: target.Format, Strategy: target.Strategy,
		locatorFingerprint: spec.LocatorFingerprint(), sensitive: spec.Sensitive(),
	}, Spec: spec, Snapshot: before}
	op.FileExists = before.Exists
	op.IsSymlink = before.IsSymlink
	op.BeforeMode = before.Mode
	if before.IsSymlink {
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetIsSymlink
		op.Warnings = append(op.Warnings, "target path is a symlink and will not be followed")
		return op, nil, nil
	}
	if before.Exists {
		op.BeforeSHA256 = before.Fingerprint
		op.BeforePreview = before.Preview
		if target.TargetID == codexconfig.AuthTargetID {
			op.BeforePreview = TextPreview{Content: codexpreset.AuthPreviewContent, Truncated: before.Preview.Truncated}
		}
	}

	currentContent, valid := validCodexWorkingCopy(target.TargetID, targetPlanRead{
		FileExists: before.Exists, IsSymlink: before.IsSymlink, SHA256: before.Fingerprint,
		Mode: before.Mode, Preview: before.Preview, Content: before.Content,
	})
	if !valid {
		if before.Exists {
			op.Warnings = append(op.Warnings, "current Codex "+target.TargetID+" working copy is invalid and was not captured")
		} else {
			op.Warnings = append(op.Warnings, "current Codex "+target.TargetID+" working copy is missing and was not captured")
		}
	}
	capture, captureWarning, currentMatchesOtherKnown, err := buildCodexPendingCapture(ctx, input.Store, target.TargetID, currentResourceID, desired, currentContent, valid, knownAuthPayloads, knownConfigHashes)
	if err != nil {
		return applyPlanOperation{}, nil, err
	}
	if captureWarning != "" {
		op.Warnings = append(op.Warnings, captureWarning)
	}

	content := desired.Content
	currentMatchesDesired := valid && codexWorkingCopyMatchesDesired(target.TargetID, currentContent, sha256HexString(currentContent), desired)
	if currentMatchesDesired || valid && currentResourceID != "" && currentResourceID == desired.ID && !currentMatchesOtherKnown {
		// The active file is the authoritative working copy for a shared binding.
		// Retain it when the binding is shared or its auth JSON already represents
		// the target resource, avoiding an unnecessary formatting-only rewrite.
		content = currentContent
	}
	preview := previewSensitiveText(content)
	if target.TargetID == codexconfig.AuthTargetID {
		op.UseDesiredMode = true
		op.DesiredMode = 0o600
		preview = TextPreview{Content: codexpreset.AuthPreviewContent}
	}
	op, err = finishCodexPlanOperation(op, targetPlanRead{
		FileExists: before.Exists, IsSymlink: before.IsSymlink, SHA256: before.Fingerprint,
		Mode: before.Mode, Preview: before.Preview, Content: before.Content,
	}, target, content, preview)
	op.privateBeforeFingerprint = before.Fingerprint
	op.privateDesiredFingerprint = sha256HexString(content)
	return op, capture, err
}

func validCodexWorkingCopy(targetID string, before targetPlanRead) (string, bool) {
	if !before.FileExists || before.IsSymlink {
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

func buildCodexPendingCapture(ctx context.Context, db *store.Store, targetID, currentResourceID string, desired codexDesiredResource, currentContent string, valid bool, knownAuthPayloads []string, knownConfigHashes map[string]struct{}) (*codexPendingCapture, string, bool, error) {
	if !valid || currentResourceID == "" {
		return nil, "", false, nil
	}
	currentHash := sha256HexString(currentContent)
	if codexWorkingCopyMatchesDesired(targetID, currentContent, currentHash, desired) {
		// Matching the target resource means the user already placed the desired
		// working copy on disk; semantic auth equality prevents harmless JSON
		// formatting from checking the target login into the outgoing binding.
		return nil, "", false, nil
	}
	switch targetID {
	case codexconfig.AuthTargetID:
		credential, err := requireCodexAuthCredential(ctx, db, currentResourceID)
		if err != nil {
			return nil, "active Codex login resource is missing or invalid; auth working copy was not captured", false, nil
		}
		if currentHash == credential.PayloadSHA256 || codexAuthPayloadsEqual(currentContent, credential.PayloadJSON) {
			return nil, "", false, nil
		}
		if codexWorkingCopyMatchesKnown(targetID, currentContent, currentHash, knownAuthPayloads, knownConfigHashes) {
			return nil, "", true, nil
		}
		return &codexPendingCapture{
			Public: StateCapture{
				ResourceKind: codexCaptureKindCredential, ResourceID: credential.ID,
				StoredSHA256: credential.PayloadSHA256, CurrentSHA256: currentHash,
			},
			CredentialUpdate: &store.UpsertProviderCredentialParams{
				ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
				PayloadJSON: currentContent, PayloadSHA256: currentHash, MetadataJSON: credential.MetadataJSON,
			},
		}, "", false, nil
	case codexconfig.TargetID:
		configSet, err := requireCodexConfigSet(ctx, db, currentResourceID)
		if err != nil {
			return nil, "active Codex config set is missing or invalid; config working copy was not captured", false, nil
		}
		if currentHash == configSet.PayloadSHA256 {
			return nil, "", false, nil
		}
		if codexWorkingCopyMatchesKnown(targetID, currentContent, currentHash, knownAuthPayloads, knownConfigHashes) {
			return nil, "", true, nil
		}
		return &codexPendingCapture{
			Public: StateCapture{
				ResourceKind: codexCaptureKindConfigSet, ResourceID: configSet.ID, ResourceName: configSet.Name,
				StoredSHA256: configSet.PayloadSHA256, CurrentSHA256: currentHash,
			},
			ConfigSetUpdate: &store.UpsertProviderConfigSetParams{
				ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind,
				Name: configSet.Name, Description: configSet.Description, PayloadText: currentContent,
				PayloadSHA256: currentHash, MetadataJSON: configSet.MetadataJSON,
			},
		}, "", false, nil
	default:
		return nil, "", false, NewError(ErrorCodexInvalid, "unsupported Codex working copy target").WithDetail("target_id", targetID)
	}
}

func loadKnownCodexResourceContent(ctx context.Context, db *store.Store) ([]string, map[string]struct{}, error) {
	credentials, err := db.ListProviderCredentials(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, nil, WrapError(ErrorStoreStatusFailed, "failed to list Codex login resources", err)
	}
	authPayloads := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		valid, credentialErr := requireCodexAuthCredential(ctx, db, credential.ID)
		if credentialErr == nil {
			authPayloads = append(authPayloads, valid.PayloadJSON)
		}
	}
	configSets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return nil, nil, WrapError(ErrorStoreStatusFailed, "failed to list Codex config resources", err)
	}
	configHashes := make(map[string]struct{}, len(configSets))
	for _, configSet := range configSets {
		valid, configErr := requireCodexConfigSet(ctx, db, configSet.ID)
		if configErr == nil {
			configHashes[valid.PayloadSHA256] = struct{}{}
		}
	}
	return authPayloads, configHashes, nil
}

func codexWorkingCopyMatchesKnown(targetID, currentContent, currentHash string, authPayloads []string, configHashes map[string]struct{}) bool {
	switch targetID {
	case codexconfig.AuthTargetID:
		for _, payload := range authPayloads {
			if codexAuthPayloadsEqual(currentContent, payload) {
				return true
			}
		}
	case codexconfig.TargetID:
		_, ok := configHashes[currentHash]
		return ok
	}
	return false
}

func codexWorkingCopyMatchesDesired(targetID, currentContent, currentHash string, desired codexDesiredResource) bool {
	if currentHash == desired.SHA256 {
		return true
	}
	if targetID != codexconfig.AuthTargetID {
		return false
	}
	return codexAuthPayloadsEqual(currentContent, desired.Content)
}

func codexAuthPayloadsEqual(left, right string) bool {
	leftValue, leftErr := decodeCodexAuthPayload(left)
	rightValue, rightErr := decodeCodexAuthPayload(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func decodeCodexAuthPayload(payload string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("Codex auth payload contains multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func loadCodexTargetResources(ctx context.Context, db *store.Store, configTarget, authTarget store.ProfileTarget) (codexPlanBindings, codexDesiredResource, codexDesiredResource, error) {
	configSetID, err := codexConfigSetIDFromTarget(configTarget)
	if err != nil {
		return codexPlanBindings{}, codexDesiredResource{}, codexDesiredResource{}, err
	}
	configSet, err := requireCodexConfigSet(ctx, db, configSetID)
	if err != nil {
		return codexPlanBindings{}, codexDesiredResource{}, codexDesiredResource{}, err
	}
	credentialID, err := codexCredentialIDFromTarget(authTarget)
	if err != nil {
		return codexPlanBindings{}, codexDesiredResource{}, codexDesiredResource{}, err
	}
	credential, err := requireCodexAuthCredential(ctx, db, credentialID)
	if err != nil {
		return codexPlanBindings{}, codexDesiredResource{}, codexDesiredResource{}, err
	}
	return codexPlanBindings{ConfigSetID: configSetID, CredentialID: credentialID},
		codexDesiredResource{
			ID: configSet.ID, Name: configSet.Name, Content: configSet.PayloadText, SHA256: configSet.PayloadSHA256,
			MetadataJSON: configSet.MetadataJSON, ConfigSet: &configSet,
		},
		codexDesiredResource{
			ID: credential.ID, Content: credential.PayloadJSON, SHA256: credential.PayloadSHA256,
			MetadataJSON: credential.MetadataJSON, Credential: &credential,
		}, nil
}

func activeCodexPlanBindings(ctx context.Context, db *store.Store) (codexPlanBindings, []string, error) {
	active, exists, err := codexActiveState(ctx, db)
	if err != nil || !exists {
		return codexPlanBindings{}, nil, err
	}
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return codexPlanBindings{}, nil, WrapError(ErrorStoreStatusFailed, "failed to read active Codex login bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return codexPlanBindings{}, nil, WrapError(ErrorStoreStatusFailed, "failed to read active Codex config bindings", err)
	}
	bindings := codexPlanBindings{}
	warnings := []string{}
	credentialBindingsUnsupported := false
	for _, binding := range credentialBindings {
		if binding.SlotID == codexpreset.CredentialSlotAuth && bindings.CredentialID == "" {
			bindings.CredentialID = binding.CredentialID
			continue
		}
		credentialBindingsUnsupported = true
	}
	if credentialBindingsUnsupported {
		bindings.CredentialID = ""
		warnings = append(warnings, "active Codex login bindings are unsupported; auth working copy will not be captured")
	}
	configBindingsUnsupported := false
	for _, binding := range configBindings {
		if binding.SlotID == codexpreset.ConfigSetSlotUserConfig && bindings.ConfigSetID == "" {
			bindings.ConfigSetID = binding.ConfigSetID
			continue
		}
		configBindingsUnsupported = true
	}
	if configBindingsUnsupported {
		bindings.ConfigSetID = ""
		warnings = append(warnings, "active Codex config bindings are unsupported; config working copy will not be captured")
	}
	if bindings.ConfigSetID == "" {
		warnings = append(warnings, "active Codex config binding is missing; config working copy will not be captured")
	}
	if bindings.CredentialID == "" {
		warnings = append(warnings, "active Codex login binding is missing; auth working copy will not be captured")
	}
	return bindings, uniqueStrings(warnings), nil
}

func finishCodexPlanOperation(op applyPlanOperation, before targetPlanRead, target store.ProfileTarget, content string, preview TextPreview) (applyPlanOperation, error) {
	if len(content) > maxTargetContentBytes {
		return applyPlanOperation{}, NewError(ErrorTargetInvalid, "desired target content is too large").
			WithDetail("target_id", target.TargetID).
			WithDetail("path", target.Path).
			WithDetail("size_bytes", len(content)).
			WithDetail("max_bytes", maxTargetContentBytes)
	}
	op.DesiredContent = content
	op.DesiredSHA256 = sha256HexString(content)
	op.DesiredPreview = preview
	op.AfterPreview = preview
	if !before.FileExists {
		op.Action = planActionCreate
		op.StatusReason = planReasonTargetMissing
		return op, nil
	}
	if op.BeforeSHA256 == op.DesiredSHA256 {
		op.Action = planActionNoop
		op.StatusReason = planReasonTargetSameContent
		return op, nil
	}
	op.Action = planActionUpdate
	op.StatusReason = planReasonTargetDifferentContent
	return op, nil
}

func validateCodexPlanTarget(provider store.Provider, target store.ProfileTarget) *AppError {
	if target.ProviderID != codexconfig.ProviderID {
		return codexTargetInvalid(target, "Codex preset only supports Codex provider targets")
	}
	if appErr := requireCodexTargetMetadata(target); appErr != nil {
		return appErr
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil {
		return WrapError(ErrorStoreSchemaInvalid, "stored Codex provider metadata is invalid", err).
			WithDetail("provider_id", provider.ID)
	}
	if !metadata.Compatible() {
		return NewError(ErrorCodexInvalid, "Codex provider was not created by the Codex preset").WithDetail("provider_id", provider.ID)
	}
	switch target.TargetID {
	case codexconfig.TargetID:
		if !codexConfigTargetFormatValid(target) || !codexConfigTargetStrategyValid(target) {
			return codexTargetInvalid(target, "Codex config target must use toml with replace-file strategy")
		}
		if target.Path != metadata.ConfigPath {
			return codexTargetInvalid(target, "Codex config target path does not match provider config path")
		}
	case codexconfig.AuthTargetID:
		if !codexAuthTargetFormatStrategyValid(target) {
			return codexTargetInvalid(target, "Codex auth target must use json with replace-file strategy")
		}
		if target.Path != metadata.AuthPath {
			return codexTargetInvalid(target, "Codex auth target path does not match provider auth path")
		}
	default:
		return codexTargetInvalid(target, "Codex preset only supports config and auth targets")
	}
	return nil
}
