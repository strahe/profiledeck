package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	codexprofile "github.com/strahe/profiledeck/internal/codex/profile"
	profilebundle "github.com/strahe/profiledeck/internal/codex/profilebundle"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/transferfile"
)

const (
	CodexProfileImportActionCreate    = "create"
	CodexProfileImportActionUnchanged = "unchanged"
	CodexProfileImportActionConflict  = "conflict"

	CodexProfileImportKindProvider   = "provider"
	CodexProfileImportKindProfile    = "profile"
	CodexProfileImportKindCredential = "credential"
	CodexProfileImportKindConfigSet  = "config_set"
)

type ExportCodexProfilesRequest struct {
	ProfileIDs []string `json:"profile_ids,omitempty"`
	OutputPath string   `json:"output_path"`
	Overwrite  bool     `json:"overwrite"`
}

type CodexProfileExportResult struct {
	Path            string   `json:"path"`
	SHA256          string   `json:"sha256"`
	FileMode        string   `json:"file_mode"`
	ProfileCount    int      `json:"profile_count"`
	CredentialCount int      `json:"credential_count"`
	ConfigSetCount  int      `json:"config_set_count"`
	Warnings        []string `json:"warnings"`
}

type InspectCodexProfileImportRequest struct {
	InputPath string `json:"input_path"`
}

type ImportCodexProfilesRequest struct {
	InputPath               string `json:"input_path"`
	ExpectedPlanFingerprint string `json:"expected_plan_fingerprint"`
	Confirm                 bool   `json:"confirm"`
}

type CodexProfileImportItem struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	ConfigSetID  string `json:"config_set_id,omitempty"`
	Action       string `json:"action"`
	Reason       string `json:"reason,omitempty"`
}

type CodexProfileImportCounts struct {
	Create    int `json:"create"`
	Unchanged int `json:"unchanged"`
	Conflict  int `json:"conflict"`
}

type CodexProfileImportPlan struct {
	Path            string                   `json:"path"`
	FileSHA256      string                   `json:"file_sha256"`
	PlanFingerprint string                   `json:"plan_fingerprint"`
	CodexDir        string                   `json:"codex_dir"`
	CanApply        bool                     `json:"can_apply"`
	NoChanges       bool                     `json:"no_changes"`
	Counts          CodexProfileImportCounts `json:"counts"`
	Items           []CodexProfileImportItem `json:"items"`
	ProfileCount    int                      `json:"profile_count"`
	CredentialCount int                      `json:"credential_count"`
	ConfigSetCount  int                      `json:"config_set_count"`
	Warnings        []string                 `json:"warnings"`
}

type CodexProfileImportResult struct {
	OperationID     string                   `json:"operation_id,omitempty"`
	FileSHA256      string                   `json:"file_sha256"`
	PlanFingerprint string                   `json:"plan_fingerprint"`
	Changed         bool                     `json:"changed"`
	Counts          CodexProfileImportCounts `json:"counts"`
	ProfileCount    int                      `json:"profile_count"`
	CredentialCount int                      `json:"credential_count"`
	ConfigSetCount  int                      `json:"config_set_count"`
	Warnings        []string                 `json:"warnings"`
}

type decodedCodexProfileBundle struct {
	Path   string
	SHA256 string
	Bundle profilebundle.Bundle
}

func (service *Service) ExportProfiles(ctx context.Context, req ExportCodexProfilesRequest) (CodexProfileExportResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexProfileExportResult{}, err
	}
	outputPath, appErr := normalizeTransferPath(req.OutputPath, apperror.ExportFailed, "output path")
	if appErr != nil {
		return CodexProfileExportResult{}, appErr
	}
	profileIDs, appErr := normalizeOptionalProfileIDs(req.ProfileIDs)
	if appErr != nil {
		return CodexProfileExportResult{}, appErr
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexProfileExportResult{}, err
	}
	defer db.Close()
	if _, err := requireCodexProvider(ctx, db); err != nil {
		return CodexProfileExportResult{}, err
	}

	var bundle profilebundle.Bundle
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		var buildErr error
		bundle, buildErr = buildCodexProfileBundle(ctx, txStore, profileIDs)
		return buildErr
	})
	if err != nil {
		return CodexProfileExportResult{}, apperror.Wrap(apperror.ExportFailed, "failed to build Codex profile bundle", err)
	}
	if appErr := rejectUnsafeCodexExportPath(ctx, db, service.runtime.Paths(), outputPath); appErr != nil {
		return CodexProfileExportResult{}, appErr
	}
	raw, err := profilebundle.Encode(bundle)
	if err != nil {
		return CodexProfileExportResult{}, apperror.Wrap(apperror.ExportFailed, "failed to encode Codex profile bundle", err)
	}
	written, err := transferfile.WritePrivateAtomic(ctx, transferfile.WriteRequest{
		Path: outputPath, Content: raw, Overwrite: req.Overwrite, Mode: 0o600, MaxBytes: profilebundle.MaxBundleBytes,
	})
	if err != nil {
		return CodexProfileExportResult{}, codexTransferFileError(apperror.ExportFailed, "failed to write Codex profile bundle", outputPath, err)
	}
	return CodexProfileExportResult{
		Path: outputPath, SHA256: written.SHA256, FileMode: written.Mode.String(),
		ProfileCount: len(bundle.Profiles), CredentialCount: len(bundle.Credentials), ConfigSetCount: len(bundle.ConfigSets),
		Warnings: codexProfileTransferWarnings(),
	}, nil
}

func (service *Service) InspectProfileImport(ctx context.Context, req InspectCodexProfileImportRequest) (CodexProfileImportPlan, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexProfileImportPlan{}, err
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return CodexProfileImportPlan{}, err
	}
	decoded, err := readCodexProfileBundle(ctx, req.InputPath)
	if err != nil {
		return CodexProfileImportPlan{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexProfileImportPlan{}, err
	}
	defer db.Close()
	if _, err := requireCodexProviderIfPresent(ctx, db); err != nil {
		return CodexProfileImportPlan{}, err
	}
	var plan CodexProfileImportPlan
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		plan = buildCodexProfileImportPlan(ctx, txStore, home, decoded)
		return nil
	})
	if err != nil {
		return CodexProfileImportPlan{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Codex profile import state", err)
	}
	return plan, nil
}

func (service *Service) ImportProfiles(ctx context.Context, req ImportCodexProfilesRequest) (CodexProfileImportResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexProfileImportResult{}, err
	}
	if !req.Confirm {
		return CodexProfileImportResult{}, apperror.New(apperror.ConfirmationRequired, "Codex profile import apply requires confirmation")
	}
	expectedFingerprint := strings.TrimSpace(req.ExpectedPlanFingerprint)
	if expectedFingerprint == "" {
		return CodexProfileImportResult{}, apperror.New(apperror.ConfirmationRequired, "Codex profile import apply requires a reviewed plan fingerprint")
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return CodexProfileImportResult{}, err
	}
	decoded, err := readCodexProfileBundle(ctx, req.InputPath)
	if err != nil {
		return CodexProfileImportResult{}, err
	}
	var plan CodexProfileImportPlan
	changed := false
	var operationID string
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "codex-profile-import", ProviderID: codexconfig.ProviderID, Record: false,
	}, func(ctx context.Context, txStore *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		if _, err := requireCodexProviderIfPresent(ctx, txStore); err != nil {
			return err
		}
		plan = buildCodexProfileImportPlan(ctx, txStore, home, decoded)
		if plan.PlanFingerprint != expectedFingerprint {
			return apperror.New(apperror.ImportPlanChanged, "Codex profile import plan changed after preview").
				WithDetail("expected_plan_fingerprint", expectedFingerprint).
				WithDetail("actual_plan_fingerprint", plan.PlanFingerprint)
		}
		if plan.Counts.Conflict > 0 {
			return apperror.New(apperror.ImportConflict, "Codex profile import contains conflicts").WithDetail("conflict_count", plan.Counts.Conflict)
		}
		if plan.Counts.Create == 0 {
			return nil
		}
		if err := applyCodexProfileBundle(ctx, txStore, home, decoded.Bundle, plan); err != nil {
			return err
		}
		metadata, err := marshalCodexProfileImportMetadata(decoded.SHA256, plan)
		if err != nil {
			return err
		}
		profileIDs := make([]string, 0, plan.ProfileCount)
		for _, item := range plan.Items {
			if item.Kind == CodexProfileImportKindProfile && item.Action == CodexProfileImportActionCreate {
				profileIDs = append(profileIDs, item.ID)
			}
		}
		if _, err := txStore.CreateAppliedImportOperation(ctx, store.CreateAppliedImportOperationParams{
			ID: operationID, ProviderID: codexconfig.ProviderID, ProfileIDs: profileIDs,
			MetadataSchemaVersion: store.OperationMetadataSchemaVersion, MetadataJSON: metadata,
		}); err != nil {
			return apperror.Wrap(apperror.OperationCreateFailed, "failed to record Codex profile import", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return CodexProfileImportResult{}, err
	}
	if !changed {
		operationID = ""
	}
	return CodexProfileImportResult{
		OperationID: operationID, FileSHA256: decoded.SHA256, PlanFingerprint: plan.PlanFingerprint,
		Changed: changed, Counts: plan.Counts, ProfileCount: plan.ProfileCount,
		CredentialCount: plan.CredentialCount, ConfigSetCount: plan.ConfigSetCount,
		Warnings: codexProfileTransferWarnings(),
	}, nil
}

func buildCodexProfileBundle(ctx context.Context, db *store.Store, profileIDs []string) (profilebundle.Bundle, error) {
	return codexprofile.BuildBundle(ctx, db, profileIDs)
}

func readCodexProfileBundle(ctx context.Context, rawPath string) (decodedCodexProfileBundle, error) {
	path, appErr := normalizeTransferPath(rawPath, apperror.ImportInvalid, "input path")
	if appErr != nil {
		return decodedCodexProfileBundle{}, appErr
	}
	read, err := transferfile.ReadPrivate(ctx, path, profilebundle.MaxBundleBytes)
	if err != nil {
		return decodedCodexProfileBundle{}, codexTransferFileError(apperror.ImportInvalid, "failed to read Codex profile bundle", path, err)
	}
	bundle, err := profilebundle.Decode(read.Content)
	if err != nil {
		return decodedCodexProfileBundle{}, apperror.Wrap(apperror.ImportInvalid, "Codex profile bundle is invalid", err).WithDetail("path", path)
	}
	return decodedCodexProfileBundle{Path: path, SHA256: read.SHA256, Bundle: bundle}, nil
}

func buildCodexProfileImportPlan(ctx context.Context, db *store.Store, home codexconfig.Home, decoded decodedCodexProfileBundle) CodexProfileImportPlan {
	items := []CodexProfileImportItem{planCodexProviderImport(ctx, db, home)}
	for _, credential := range decoded.Bundle.Credentials {
		items = append(items, planCodexCredentialImport(ctx, db, credential))
	}
	for _, configSet := range decoded.Bundle.ConfigSets {
		items = append(items, planCodexConfigSetImport(ctx, db, configSet))
	}
	for _, profile := range decoded.Bundle.Profiles {
		items = append(items, planCodexProfileImport(ctx, db, profile))
	}
	counts := countCodexProfileImportItems(items)
	plan := CodexProfileImportPlan{
		Path: decoded.Path, FileSHA256: decoded.SHA256, CodexDir: home.Dir,
		Counts: counts, Items: items, ProfileCount: len(decoded.Bundle.Profiles),
		CredentialCount: len(decoded.Bundle.Credentials), ConfigSetCount: len(decoded.Bundle.ConfigSets),
		Warnings: codexProfileTransferWarnings(),
	}
	plan.NoChanges = counts.Create == 0 && counts.Conflict == 0
	plan.CanApply = counts.Create > 0 && counts.Conflict == 0
	plan.PlanFingerprint = fingerprintCodexProfileImportPlan(plan)
	return plan
}

func planCodexProviderImport(ctx context.Context, db *store.Store, home codexconfig.Home) CodexProfileImportItem {
	item := CodexProfileImportItem{Kind: CodexProfileImportKindProvider, ID: codexconfig.ProviderID, Name: codexpreset.ProviderName}
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		item.Action = CodexProfileImportActionCreate
	} else if err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "provider_inspect_failed"
	} else if _, _, err := codexPreflightProvider(ctx, db, home); err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "provider_incompatible"
	} else {
		item.Action = CodexProfileImportActionUnchanged
		_ = provider
	}
	if item.Action != CodexProfileImportActionConflict {
		targets, err := allStoredCodexBindingTargets(ctx, db)
		if err != nil {
			item.Action, item.Reason = CodexProfileImportActionConflict, "targets_inspect_failed"
		} else {
			for _, target := range targets {
				if requireCodexTargetForHome(target, home) != nil {
					item.Action, item.Reason = CodexProfileImportActionConflict, "target_home_incompatible"
					break
				}
			}
		}
	}
	if item.Action != CodexProfileImportActionConflict {
		if profiletarget.EnsurePathOwnership(ctx, db, home.ConfigPath, profiletarget.PathOwnershipKey(home.ConfigPath), codexconfig.ProviderID, codexconfig.TargetID, nil) != nil ||
			profiletarget.EnsurePathOwnership(ctx, db, home.AuthPath, profiletarget.PathOwnershipKey(home.AuthPath), codexconfig.ProviderID, codexconfig.AuthTargetID, nil) != nil {
			item.Action, item.Reason = CodexProfileImportActionConflict, "path_owned"
		}
	}
	return item
}

func planCodexCredentialImport(ctx context.Context, db *store.Store, incoming profilebundle.Credential) CodexProfileImportItem {
	item := CodexProfileImportItem{Kind: CodexProfileImportKindCredential, ID: incoming.ID}
	existing, err := db.GetProviderCredential(ctx, incoming.ID)
	if errors.Is(err, store.ErrNotFound) {
		item.Action = CodexProfileImportActionCreate
		return item
	}
	if err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "credential_inspect_failed"
		return item
	}
	if existing.ProviderID == codexconfig.ProviderID && existing.CredentialKind == incoming.Kind && codexAuthPayloadsEqual(existing.PayloadJSON, incoming.PayloadJSON) {
		item.Action = CodexProfileImportActionUnchanged
		return item
	}
	item.Action, item.Reason = CodexProfileImportActionConflict, "credential_differs"
	return item
}

func planCodexConfigSetImport(ctx context.Context, db *store.Store, incoming profilebundle.ConfigSet) CodexProfileImportItem {
	item := CodexProfileImportItem{Kind: CodexProfileImportKindConfigSet, ID: incoming.ID, Name: incoming.Name}
	existing, err := db.GetProviderConfigSet(ctx, codexconfig.ProviderID, incoming.ID)
	if errors.Is(err, store.ErrNotFound) {
		item.Action = CodexProfileImportActionCreate
		return item
	}
	if err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "config_set_inspect_failed"
		return item
	}
	if existing.ProviderID == codexconfig.ProviderID && existing.ConfigKind == incoming.Kind &&
		existing.Name == incoming.Name && existing.Description == incoming.Description && existing.PayloadText == incoming.PayloadText {
		item.Action = CodexProfileImportActionUnchanged
		return item
	}
	item.Action, item.Reason = CodexProfileImportActionConflict, "config_set_differs"
	return item
}

func planCodexProfileImport(ctx context.Context, db *store.Store, incoming profilebundle.Profile) CodexProfileImportItem {
	item := CodexProfileImportItem{
		Kind: CodexProfileImportKindProfile, ID: incoming.ID, Name: incoming.Name,
		CredentialID: incoming.CredentialID, ConfigSetID: incoming.ConfigSetID,
	}
	_, err := db.GetProfile(ctx, incoming.ID)
	if errors.Is(err, store.ErrNotFound) {
		credentialBindings, credentialErr := db.ListProfileCredentialBindings(ctx, incoming.ID, codexconfig.ProviderID)
		configBindings, configErr := db.ListProfileConfigSetBindings(ctx, incoming.ID, codexconfig.ProviderID)
		if credentialErr != nil || configErr != nil || len(credentialBindings) > 0 || len(configBindings) > 0 {
			item.Action, item.Reason = CodexProfileImportActionConflict, "profile_orphaned_bindings"
		} else {
			item.Action = CodexProfileImportActionCreate
		}
		return item
	}
	if err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "profile_inspect_failed"
		return item
	}
	credentialBindings, credentialErr := db.ListProfileCredentialBindings(ctx, incoming.ID, codexconfig.ProviderID)
	configBindings, configErr := db.ListProfileConfigSetBindings(ctx, incoming.ID, codexconfig.ProviderID)
	if credentialErr != nil || configErr != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "profile_bindings_inspect_failed"
		return item
	}
	if len(credentialBindings) == 0 && len(configBindings) == 0 {
		// Profiles are global composition objects. Importing a new Codex facet
		// preserves metadata already owned by the existing Profile.
		item.Action, item.Reason = CodexProfileImportActionCreate, "attach_existing_profile"
		return item
	}
	targets, err := storedCodexBindingTargets(ctx, db, incoming.ID)
	if err != nil {
		item.Action, item.Reason = CodexProfileImportActionConflict, "profile_bindings_inspect_failed"
		return item
	}
	configTarget, authTarget, err := requireCodexFullProfileTargets(incoming.ID, targets)
	if err != nil || len(targets) != 2 {
		item.Action, item.Reason = CodexProfileImportActionConflict, "profile_bindings_differ"
		return item
	}
	credentialID, credentialErr := codexCredentialIDFromTarget(authTarget)
	configSetID, configErr := codexConfigSetIDFromTarget(configTarget)
	if credentialErr == nil && configErr == nil && credentialID == incoming.CredentialID && configSetID == incoming.ConfigSetID {
		// Display metadata belongs to the global Profile and may differ because
		// another Provider workspace updated it after this bundle was created.
		item.Action = CodexProfileImportActionUnchanged
		return item
	}
	item.Action, item.Reason = CodexProfileImportActionConflict, "profile_differs"
	return item
}

func applyCodexProfileBundle(ctx context.Context, db *store.Store, home codexconfig.Home, bundle profilebundle.Bundle, plan CodexProfileImportPlan) error {
	actions := make(map[string]string, len(plan.Items))
	for _, item := range plan.Items {
		actions[item.Kind+"\x00"+item.ID] = item.Action
	}
	if actions[CodexProfileImportKindProvider+"\x00"+codexconfig.ProviderID] == CodexProfileImportActionCreate {
		metadata, err := codexpreset.ProviderMetadataJSON(home)
		if err != nil {
			return err
		}
		if _, err := upsertCodexProvider(ctx, db, metadata, false); err != nil {
			return err
		}
	}
	for _, credential := range bundle.Credentials {
		if actions[CodexProfileImportKindCredential+"\x00"+credential.ID] != CodexProfileImportActionCreate {
			continue
		}
		if _, err := upsertCodexAuthCredential(ctx, db, credential.ID, credential.PayloadJSON); err != nil {
			return err
		}
	}
	for _, configSet := range bundle.ConfigSets {
		if actions[CodexProfileImportKindConfigSet+"\x00"+configSet.ID] != CodexProfileImportActionCreate {
			continue
		}
		if _, err := upsertCodexConfigSet(ctx, db, configSet.ID, configSet.Name, configSet.Description, configSet.PayloadText); err != nil {
			return err
		}
	}
	for _, profile := range bundle.Profiles {
		if actions[CodexProfileImportKindProfile+"\x00"+profile.ID] != CodexProfileImportActionCreate {
			continue
		}
		_, profileErr := db.GetProfile(ctx, profile.ID)
		hasProfile := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, store.ErrNotFound) {
			return mapProfileStoreError(profileErr)
		}
		stored, err := upsertCodexProfile(ctx, db, profile.ID, managedProfileFields{CreateName: profile.Name, CreateDescription: profile.Description}, hasProfile)
		if err != nil {
			return err
		}
		if _, _, err := createCodexProfileTargets(ctx, db, stored.ID, home, profile.ConfigSetID, profile.CredentialID); err != nil {
			return err
		}
	}
	return nil
}

func marshalCodexProfileImportMetadata(bundleSHA256 string, plan CodexProfileImportPlan) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"action": "codex-profile-import", "provider_id": codexconfig.ProviderID,
		"bundle_sha256": bundleSHA256, "plan_fingerprint": plan.PlanFingerprint,
		"created": plan.Counts.Create, "unchanged": plan.Counts.Unchanged,
		"profiles": plan.ProfileCount, "credentials": plan.CredentialCount, "config_sets": plan.ConfigSetCount,
	})
	return string(raw), err
}

func fingerprintCodexProfileImportPlan(plan CodexProfileImportPlan) string {
	type fingerprintPayload struct {
		FileSHA256 string                   `json:"file_sha256"`
		CodexDir   string                   `json:"codex_dir"`
		Items      []CodexProfileImportItem `json:"items"`
	}
	raw, err := json.Marshal(fingerprintPayload{FileSHA256: plan.FileSHA256, CodexDir: plan.CodexDir, Items: plan.Items})
	if err != nil {
		return ""
	}
	return sha256HexString(string(raw))
}

func countCodexProfileImportItems(items []CodexProfileImportItem) CodexProfileImportCounts {
	var counts CodexProfileImportCounts
	for _, item := range items {
		switch item.Action {
		case CodexProfileImportActionCreate:
			counts.Create++
		case CodexProfileImportActionUnchanged:
			counts.Unchanged++
		case CodexProfileImportActionConflict:
			counts.Conflict++
		}
	}
	return counts
}

func normalizeOptionalProfileIDs(values []string) ([]string, *apperror.Error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		id, appErr := validateID(raw, apperror.ProfileInvalid)
		if appErr != nil {
			return nil, appErr
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeTransferPath(raw string, code apperror.Code, label string) (string, *apperror.Error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", apperror.New(code, label+" is required")
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", apperror.Wrap(code, "failed to resolve "+label, err)
	}
	return filepath.Clean(path), nil
}

func rejectUnsafeCodexExportPath(ctx context.Context, db *store.Store, paths runtime.Paths, outputPath string) *apperror.Error {
	for _, reserved := range []string{paths.Database, paths.Lock} {
		if sameCleanPath(outputPath, reserved) {
			return apperror.New(apperror.ExportFailed, "Codex profile bundle cannot replace ProfileDeck runtime state").WithDetail("path", outputPath)
		}
	}
	home, err := codexStoredHome(ctx, db)
	if err == nil {
		for _, targetPath := range []string{home.ConfigPath, home.AuthPath} {
			if sameCleanPath(outputPath, targetPath) {
				return apperror.New(apperror.ExportFailed, "Codex profile bundle cannot replace a Codex working file").WithDetail("path", outputPath)
			}
		}
	}
	return nil
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs) {
		return true
	}
	leftInfo, leftStatErr := os.Stat(left)
	rightInfo, rightStatErr := os.Stat(right)
	return leftStatErr == nil && rightStatErr == nil && os.SameFile(leftInfo, rightInfo)
}

func codexTransferFileError(code apperror.Code, message, path string, err error) *apperror.Error {
	appErr := apperror.Wrap(code, message, err).WithDetail("path", path)
	switch {
	case errors.Is(err, transferfile.ErrExists):
		appErr.Message = "Codex profile bundle already exists; confirm overwrite explicitly"
		appErr = appErr.WithDetail("reason", "exists")
	case errors.Is(err, transferfile.ErrNotPrivate):
		appErr.Message = "Codex profile bundle permissions must not allow group or other access"
		appErr = appErr.WithDetail("reason", "permissions")
	case errors.Is(err, transferfile.ErrNotRegular):
		appErr.Message = "Codex profile bundle path must be a regular file and not a symlink"
		appErr = appErr.WithDetail("reason", "not_regular")
	case errors.Is(err, transferfile.ErrChanged):
		appErr.Message = "Codex profile bundle changed during the operation"
		appErr = appErr.WithDetail("reason", "changed")
	case errors.Is(err, transferfile.ErrTooLarge):
		appErr.Message = "Codex profile bundle is too large"
		appErr = appErr.WithDetail("reason", "too_large")
	}
	return appErr
}

func codexProfileTransferWarnings() []string {
	return []string{
		"This bundle contains raw Codex auth.json and complete config.toml payloads; keep it private.",
		"Export reads stored ProfileDeck state only; save the active Codex profile state first when needed.",
		"Import does not activate a profile or write Codex working files.",
	}
}
