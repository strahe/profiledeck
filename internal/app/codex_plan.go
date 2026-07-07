package app

import (
	"context"

	"github.com/strahe/profiledeck/internal/codexconfig"
	"github.com/strahe/profiledeck/internal/store"
)

type codexPlanAdapter struct{}

func (codexPlanAdapter) ID() string {
	return codexconfig.AdapterID
}

func (codexPlanAdapter) Build(ctx context.Context, input planAdapterInput) ([]applyPlanOperation, []string, error) {
	operations := make([]applyPlanOperation, 0, len(input.Targets))
	warnings := []string{}
	seenWarnings := map[string]struct{}{}
	for _, target := range input.Targets {
		op, err := buildCodexPlanOperation(ctx, input, target)
		if err != nil {
			return nil, nil, err
		}
		operations = append(operations, op)
		for _, warning := range op.Warnings {
			if _, ok := seenWarnings[warning]; ok {
				continue
			}
			seenWarnings[warning] = struct{}{}
			warnings = append(warnings, warning)
		}
	}
	return operations, warnings, nil
}

func buildCodexPlanOperation(ctx context.Context, input planAdapterInput, target store.ProfileTarget) (applyPlanOperation, error) {
	provider := input.Provider
	profile := input.Profile
	if provider.ID != codexconfig.ProviderID || provider.AdapterID != codexconfig.AdapterID {
		return applyPlanOperation{}, NewError(ErrorCodexInvalid, "Codex plan adapter received an incompatible provider")
	}
	if appErr := validateCodexPlanTarget(provider, target); appErr != nil {
		return applyPlanOperation{}, appErr
	}

	op := applyPlanOperation{
		PlanOperation: PlanOperation{
			ProviderID: provider.ID,
			ProfileID:  profile.ID,
			TargetID:   target.TargetID,
			Path:       target.Path,
			Format:     target.Format,
			Strategy:   target.Strategy,
		},
	}

	before, err := readTargetForPlan(ctx, target.Path, true)
	if err != nil {
		return applyPlanOperation{}, err
	}
	op.FileExists = before.FileExists
	op.IsSymlink = before.IsSymlink
	op.BeforeMode = before.Mode
	if before.IsSymlink {
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetIsSymlink
		op.Warnings = append(op.Warnings, "target path is a symlink and will not be followed")
		return op, nil
	}
	if before.FileExists {
		op.BeforeSHA256 = before.SHA256
		op.BeforePreview = before.Preview
	}

	switch target.TargetID {
	case codexconfig.TargetID:
		return buildCodexConfigPlanOperation(op, before, target)
	case codexconfig.AuthTargetID:
		return buildCodexAuthPlanOperation(ctx, input, op, before, target)
	default:
		return applyPlanOperation{}, codexTargetInvalid(target, "Codex preset only supports config and auth targets")
	}
}

func buildCodexConfigPlanOperation(op applyPlanOperation, before targetPlanRead, target store.ProfileTarget) (applyPlanOperation, error) {
	metadata, appErr := decodeCodexTargetMetadata(target.MetadataJSON)
	if appErr != nil {
		return applyPlanOperation{}, appErr
	}

	var content string
	switch metadata.modeOrDefault() {
	case codexTargetModeManagedKeys:
		if before.FileExists {
			op.Warnings = append(op.Warnings, tomlSemanticRewriteWarning)
		}
		desired, err := codexconfig.ParseValueJSON(target.ValueJSON)
		if err != nil {
			return applyPlanOperation{}, targetContentInvalidError(target, "stored Codex target value_json is invalid", err)
		}
		built, err := codexconfig.ApplyManagedTOML(before.Content, before.FileExists, desired)
		if err != nil {
			return applyPlanOperation{}, targetContentInvalidError(target, "failed to build Codex config content", err)
		}
		content = built
	case codexTargetModeFullFile:
		built, err := replaceFileContentFromValueJSON(target.ValueJSON)
		if err != nil {
			return applyPlanOperation{}, targetContentInvalidError(target, "stored Codex config target value_json is invalid", err)
		}
		if err := codexconfig.ValidateTOML(built); err != nil {
			return applyPlanOperation{}, targetContentInvalidError(target, "stored Codex config snapshot is invalid TOML", err)
		}
		content = built
	default:
		return applyPlanOperation{}, codexTargetInvalid(target, "Codex config target mode is unsupported").
			WithDetail("mode", metadata.Mode)
	}
	return finishCodexPlanOperation(op, before, target, content, previewSensitiveText(content))
}

func buildCodexAuthPlanOperation(ctx context.Context, input planAdapterInput, op applyPlanOperation, before targetPlanRead, target store.ProfileTarget) (applyPlanOperation, error) {
	if input.Store == nil {
		return applyPlanOperation{}, NewError(ErrorPlanBuildFailed, "Codex auth plan requires store access")
	}
	if before.FileExists {
		op.BeforePreview = TextPreview{Content: codexAuthPreviewContent, Truncated: before.Preview.Truncated}
	}
	accountID, err := parseCodexAuthTargetValueJSON(target.ValueJSON)
	if err != nil {
		return applyPlanOperation{}, targetContentInvalidError(target, "stored Codex auth target value_json is invalid", err)
	}
	secret, err := input.Store.GetProviderAccountSecret(ctx, codexconfig.ProviderID, accountID)
	if err != nil {
		return applyPlanOperation{}, mapCodexAccountStoreError(err)
	}
	if secret.SecretKind != codexSecretKindAuthJSON {
		return applyPlanOperation{}, NewError(ErrorCodexInvalid, "Codex account secret has unsupported kind").
			WithDetail("account_id", accountID).
			WithDetail("secret_kind", secret.SecretKind)
	}
	if _, appErr := normalizeCodexAuthPayload([]byte(secret.PayloadJSON)); appErr != nil {
		return applyPlanOperation{}, appErr.WithDetail("account_id", accountID)
	}
	op.UseDesiredMode = true
	op.DesiredMode = 0o600
	return finishCodexPlanOperation(op, before, target, secret.PayloadJSON, TextPreview{Content: codexAuthPreviewContent})
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
	op.AfterPreview = op.DesiredPreview

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
	metadata, appErr := decodeCodexProviderMetadata(provider.MetadataJSON)
	if appErr != nil {
		return appErr.WithDetail("provider_id", provider.ID)
	}
	if !metadata.compatible() {
		return NewError(ErrorCodexInvalid, "Codex provider was not created by the Codex preset").
			WithDetail("provider_id", provider.ID)
	}
	switch target.TargetID {
	case codexconfig.TargetID:
		if !codexConfigTargetFormatValid(target) || !codexConfigTargetStrategyValid(target) {
			return codexTargetInvalid(target, "Codex config target must use toml with toml-merge or replace-file strategy")
		}
		if target.Path != metadata.ConfigPath {
			return codexTargetInvalid(target, "Codex config target path does not match provider config path").
				WithDetail("provider_config_path", metadata.ConfigPath).
				WithDetail("target_path", target.Path)
		}
	case codexconfig.AuthTargetID:
		if !codexAuthTargetFormatStrategyValid(target) {
			return codexTargetInvalid(target, "Codex auth target must use json with replace-file strategy")
		}
		if metadata.AuthPath == "" {
			return NewError(ErrorCodexInvalid, "Codex provider metadata is missing auth path").
				WithDetail("provider_id", provider.ID)
		}
		if target.Path != metadata.AuthPath {
			return codexTargetInvalid(target, "Codex auth target path does not match provider auth path").
				WithDetail("provider_auth_path", metadata.AuthPath).
				WithDetail("target_path", target.Path)
		}
	default:
		return codexTargetInvalid(target, "Codex preset only supports config and auth targets")
	}
	return nil
}
