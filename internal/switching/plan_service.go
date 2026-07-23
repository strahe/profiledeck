package switching

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	"github.com/strahe/profiledeck/internal/validate"
)

const (
	planActionCreate      = "create"
	planActionUpdate      = "update"
	planActionNoop        = "noop"
	planActionUnsupported = "unsupported"

	planReasonTargetMissing          = "target_missing"
	planReasonTargetSameContent      = "target_same_content"
	planReasonTargetDifferentContent = "target_different_content"
	planReasonTargetModeDifferent    = "target_mode_different"
	planReasonTargetIsSymlink        = "target_is_symlink"
)

type BuildPlanRequest struct {
	ProviderID string `json:"provider_id"`
	ProfileID  string `json:"profile_id"`
}

type SwitchPlan struct {
	CreatedAtUnixMS int64           `json:"created_at_unix_ms"`
	ReadOnly        bool            `json:"read_only"`
	PlanFingerprint string          `json:"plan_fingerprint"`
	Provider        PlanProvider    `json:"provider"`
	Profile         PlanProfile     `json:"profile"`
	Bindings        []PlanBinding   `json:"bindings,omitempty"`
	StateCaptures   []StateCapture  `json:"state_captures,omitempty"`
	Operations      []PlanOperation `json:"operations"`
	Warnings        []string        `json:"warnings"`
}

type PlanBinding struct {
	TargetID          string `json:"target_id"`
	CurrentResourceID string `json:"current_resource_id,omitempty"`
	TargetResourceID  string `json:"target_resource_id"`
	Changed           bool   `json:"changed"`
}

type StateCapture struct {
	ResourceKind  string `json:"resource_kind"`
	ResourceID    string `json:"resource_id"`
	ResourceName  string `json:"resource_name,omitempty"`
	StoredSHA256  string `json:"stored_sha256"`
	CurrentSHA256 string `json:"current_sha256"`
	Changed       bool   `json:"changed,omitempty"`
}

type PlanProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AdapterID string `json:"adapter_id"`
}

type PlanProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type PlanOperation struct {
	ProviderID                string      `json:"provider_id"`
	ProfileID                 string      `json:"profile_id"`
	TargetID                  string      `json:"target_id"`
	BackendID                 string      `json:"backend_id"`
	TargetLabel               string      `json:"target_label"`
	Path                      string      `json:"path"`
	Format                    string      `json:"format"`
	Strategy                  string      `json:"strategy"`
	Action                    string      `json:"action"`
	StatusReason              string      `json:"status_reason"`
	FileExists                bool        `json:"file_exists"`
	IsSymlink                 bool        `json:"is_symlink"`
	BeforeSHA256              string      `json:"before_sha256"`
	DesiredSHA256             string      `json:"desired_sha256"`
	BeforePreview             TextPreview `json:"before_preview"`
	DesiredPreview            TextPreview `json:"desired_preview"`
	AfterPreview              TextPreview `json:"after_preview"`
	Warnings                  []string    `json:"warnings"`
	locatorFingerprint        string
	privateBeforeFingerprint  string
	privateDesiredFingerprint string
	privateRecoveryLocator    string
	privateObjectFingerprint  string
	sensitive                 bool
}

type TextPreview struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type applyPlan struct {
	SwitchPlan        SwitchPlan
	Operations        []applyPlanOperation
	CredentialUpdates []store.UpsertProviderCredentialParams
	ConfigSetUpdates  []store.UpsertProviderConfigSetParams
}

type applyPlanOperation struct {
	PlanOperation
	DesiredContent string
	BeforeMode     os.FileMode
	DesiredMode    os.FileMode
	UseDesiredMode bool
	Spec           targetSpec
	Snapshot       targetSnapshot
}

type planAdapterResult struct {
	Operations        []applyPlanOperation
	Warnings          []string
	Bindings          []PlanBinding
	StateCaptures     []StateCapture
	CredentialUpdates []store.UpsertProviderCredentialParams
	ConfigSetUpdates  []store.UpsertProviderConfigSetParams
}

func (service *Service) BuildPlan(ctx context.Context, req BuildPlanRequest) (SwitchPlan, error) {
	providerID, appErr := validate.ID(req.ProviderID, apperror.ProviderInvalid)
	if appErr != nil {
		return SwitchPlan{}, appErr
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return SwitchPlan{}, appErr
	}
	if err := service.RequireProvider(ctx, providerID); err != nil {
		return SwitchPlan{}, err
	}

	// Plan generation must not mutate ProfileDeck state; switch apply will rebuild the plan under lock.
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return SwitchPlan{}, err
	}
	defer db.Close()

	plan, err := service.buildApplyPlan(ctx, db, providerID, profileID)
	if err != nil {
		return SwitchPlan{}, err
	}
	return plan.SwitchPlan, nil
}

func (service *Service) buildApplyPlan(ctx context.Context, db *store.Store, providerID, profileID string) (applyPlan, error) {
	provider, err := db.GetProvider(ctx, providerID)
	if err != nil {
		return applyPlan{}, mapProviderStoreError(err)
	}
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return applyPlan{}, mapProfileStoreError(err)
	}
	if !provider.Enabled {
		return applyPlan{}, apperror.New(apperror.ProviderDisabled, "provider is disabled")
	}
	dependencies := service.dependencies
	adapter, ok := dependencies.Adapters.ManagedAdapter(provider.ID)
	if !ok {
		adapter, ok = dependencies.Adapters.Adapter(provider.AdapterID)
	}
	if !ok {
		return applyPlan{}, apperror.New(apperror.AdapterNotFound, "adapter not found")
	}

	targets, err := db.ListProfileTargets(ctx, profile.ID, provider.ID, false)
	if err != nil {
		return applyPlan{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to list profile targets", err)
	}
	input := newPlanInput(provider, profile, targets, db)
	input.Targets, err = adapter.LoadTargets(ctx, input)
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, apperror.Wrap(apperror.PlanBuildFailed, "failed to load switch plan targets", err)
	}
	prepared, err := adapter.Prepare(ctx, input)
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, apperror.Wrap(apperror.PlanBuildFailed, "failed to prepare switch plan", err)
	}
	snapshots, err := switchplan.InspectPrepared(ctx, dependencies.Targets, prepared)
	if err != nil {
		return applyPlan{}, err
	}
	adapterResult, err := adapter.Finalize(ctx, input, prepared, snapshots)
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, apperror.Wrap(apperror.PlanBuildFailed, "failed to build switch plan", err)
	}
	if appErr := switchplan.ValidateResult(input, prepared, snapshots, adapterResult); appErr != nil {
		return applyPlan{}, appErr
	}
	appResult := appPlanResultFromSwitching(adapterResult)

	publicOperations := make([]PlanOperation, 0, len(appResult.Operations))
	for _, op := range appResult.Operations {
		publicOperations = append(publicOperations, publicPlanOperation(op.PlanOperation))
	}
	plan := SwitchPlan{
		CreatedAtUnixMS: time.Now().UnixMilli(),
		ReadOnly:        true,
		Provider: PlanProvider{
			ID:        provider.ID,
			Name:      provider.Name,
			AdapterID: provider.AdapterID,
		},
		Profile: PlanProfile{
			ID:          profile.ID,
			Name:        profile.Name,
			Description: profile.Description,
		},
		Bindings:      appResult.Bindings,
		StateCaptures: appResult.StateCaptures,
		Operations:    publicOperations,
		Warnings:      appResult.Warnings,
	}
	// Fingerprint the complete internal state before removing secret-derived
	// capture hashes from the public DTO.
	plan.PlanFingerprint = fingerprintSwitchPlan(plan)
	plan.StateCaptures = publicStateCaptures(appResult.StateCaptures, appResult.Operations)
	return applyPlan{
		SwitchPlan:        plan,
		Operations:        appResult.Operations,
		CredentialUpdates: appResult.CredentialUpdates,
		ConfigSetUpdates:  appResult.ConfigSetUpdates,
	}, nil
}

func publicStateCaptures(captures []StateCapture, operations []applyPlanOperation) []StateCapture {
	redactHashes := false
	for _, operation := range operations {
		if operation.sensitive {
			redactHashes = true
			break
		}
	}
	if !redactHashes {
		return captures
	}
	public := make([]StateCapture, len(captures))
	copy(public, captures)
	for index := range public {
		public[index].StoredSHA256 = ""
		public[index].CurrentSHA256 = ""
	}
	return public
}

func publicPlanOperation(operation PlanOperation) PlanOperation {
	if !operation.sensitive {
		return operation
	}
	operation.BeforeSHA256 = ""
	operation.DesiredSHA256 = ""
	if operation.BackendID == targetBackendFile {
		return operation
	}
	// Non-file secrets are internal planning inputs. Adapters may provide them
	// for fingerprints and apply, but public plans expose only safe identity,
	// action, status, warnings, and the adapter-provided label.
	operation.Path = ""
	operation.Format = ""
	operation.Strategy = ""
	operation.FileExists = false
	operation.IsSymlink = false
	operation.BeforePreview = TextPreview{}
	operation.DesiredPreview = TextPreview{}
	operation.AfterPreview = TextPreview{}
	return operation
}

func applyPlanOperationFromSwitchingPlan(operation switchplan.ApplyOperation) applyPlanOperation {
	return applyPlanOperation{
		PlanOperation: PlanOperation{
			ProviderID: operation.ProviderID, ProfileID: operation.ProfileID, TargetID: operation.TargetID,
			BackendID: operation.BackendID, TargetLabel: operation.TargetLabel, Path: operation.Path,
			Format: operation.Format, Strategy: operation.Strategy, Action: operation.Action,
			StatusReason: operation.StatusReason, FileExists: operation.FileExists, IsSymlink: operation.IsSymlink,
			BeforeSHA256: operation.BeforeSHA256, DesiredSHA256: operation.DesiredSHA256,
			BeforePreview:  textPreviewFromPlan(operation.BeforePreview),
			DesiredPreview: textPreviewFromPlan(operation.DesiredPreview),
			AfterPreview:   textPreviewFromPlan(operation.AfterPreview), Warnings: operation.Warnings,
			locatorFingerprint: operation.LocatorFingerprint, privateBeforeFingerprint: operation.PrivateBeforeFingerprint,
			privateDesiredFingerprint: operation.PrivateDesiredFingerprint, privateRecoveryLocator: operation.PrivateRecoveryLocator,
			privateObjectFingerprint: operation.PrivateObjectFingerprint, sensitive: operation.Sensitive,
		},
		DesiredContent: operation.DesiredContent, BeforeMode: operation.BeforeMode, DesiredMode: operation.DesiredMode,
		UseDesiredMode: operation.UseDesiredMode, Spec: operation.Spec, Snapshot: targetSnapshotFromSwitching(operation.Snapshot),
	}
}

func appPlanResultFromSwitching(result switchplan.Result) planAdapterResult {
	operations := make([]applyPlanOperation, 0, len(result.Operations))
	for _, operation := range result.Operations {
		operations = append(operations, applyPlanOperationFromSwitchingPlan(operation))
	}
	bindings := make([]PlanBinding, 0, len(result.Bindings))
	for _, binding := range result.Bindings {
		bindings = append(bindings, PlanBinding{
			TargetID: binding.TargetID, CurrentResourceID: binding.CurrentResourceID,
			TargetResourceID: binding.TargetResourceID, Changed: binding.Changed,
		})
	}
	captures := make([]StateCapture, 0, len(result.StateCaptures))
	for _, capture := range result.StateCaptures {
		captures = append(captures, StateCapture{
			ResourceKind: capture.ResourceKind, ResourceID: capture.ResourceID, ResourceName: capture.ResourceName,
			StoredSHA256: capture.StoredSHA256, CurrentSHA256: capture.CurrentSHA256, Changed: capture.Changed,
		})
	}
	credentialUpdates := make([]store.UpsertProviderCredentialParams, 0, len(result.CredentialUpdates))
	for _, update := range result.CredentialUpdates {
		credentialUpdates = append(credentialUpdates, store.UpsertProviderCredentialParams{
			ID: update.ID, ProviderID: update.ProviderID, CredentialKind: update.CredentialKind,
			PayloadJSON: update.PayloadJSON, PayloadSHA256: update.PayloadSHA256, MetadataJSON: update.MetadataJSON,
		})
	}
	configSetUpdates := make([]store.UpsertProviderConfigSetParams, 0, len(result.ConfigSetUpdates))
	for _, update := range result.ConfigSetUpdates {
		configSetUpdates = append(configSetUpdates, store.UpsertProviderConfigSetParams{
			ID: update.ID, ProviderID: update.ProviderID, ConfigKind: update.ConfigKind,
			Name: update.Name, Description: update.Description, PayloadText: update.PayloadText,
			PayloadSHA256: update.PayloadSHA256, MetadataJSON: update.MetadataJSON,
		})
	}
	return planAdapterResult{
		Operations: operations, Warnings: result.Warnings, Bindings: bindings, StateCaptures: captures,
		CredentialUpdates: credentialUpdates, ConfigSetUpdates: configSetUpdates,
	}
}

func textPreviewFromProfileTarget(preview profiletarget.Preview) TextPreview {
	return TextPreview{Content: preview.Content, Truncated: preview.Truncated}
}

func textPreviewFromPlan(preview switchplan.Preview) TextPreview {
	return TextPreview{Content: preview.Content, Truncated: preview.Truncated}
}

func fingerprintSwitchPlan(plan SwitchPlan) string {
	type fingerprintOperation struct {
		ProviderID    string   `json:"provider_id"`
		ProfileID     string   `json:"profile_id"`
		TargetID      string   `json:"target_id"`
		BackendID     string   `json:"backend_id"`
		Locator       string   `json:"locator_fingerprint"`
		Path          string   `json:"path"`
		Format        string   `json:"format"`
		Strategy      string   `json:"strategy"`
		Action        string   `json:"action"`
		StatusReason  string   `json:"status_reason"`
		FileExists    bool     `json:"file_exists"`
		IsSymlink     bool     `json:"is_symlink"`
		BeforeSHA256  string   `json:"before_sha256"`
		DesiredSHA256 string   `json:"desired_sha256"`
		ObjectSHA256  string   `json:"object_sha256,omitempty"`
		Warnings      []string `json:"warnings"`
	}
	type fingerprintPayload struct {
		ProviderID string                 `json:"provider_id"`
		AdapterID  string                 `json:"adapter_id"`
		ProfileID  string                 `json:"profile_id"`
		Bindings   []PlanBinding          `json:"bindings,omitempty"`
		Captures   []StateCapture         `json:"state_captures,omitempty"`
		Operations []fingerprintOperation `json:"operations"`
		Warnings   []string               `json:"warnings"`
	}

	operations := make([]fingerprintOperation, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		operations = append(operations, fingerprintOperation{
			ProviderID:    op.ProviderID,
			ProfileID:     op.ProfileID,
			TargetID:      op.TargetID,
			BackendID:     op.BackendID,
			Locator:       op.locatorFingerprint,
			Path:          op.Path,
			Format:        op.Format,
			Strategy:      op.Strategy,
			Action:        op.Action,
			StatusReason:  op.StatusReason,
			FileExists:    op.FileExists,
			IsSymlink:     op.IsSymlink,
			BeforeSHA256:  firstNonEmpty(op.privateBeforeFingerprint, op.BeforeSHA256),
			DesiredSHA256: firstNonEmpty(op.privateDesiredFingerprint, op.DesiredSHA256),
			ObjectSHA256:  op.privateObjectFingerprint,
			Warnings:      op.Warnings,
		})
	}

	raw, err := json.Marshal(fingerprintPayload{
		ProviderID: plan.Provider.ID,
		AdapterID:  plan.Provider.AdapterID,
		ProfileID:  plan.Profile.ID,
		Bindings:   plan.Bindings,
		Captures:   plan.StateCaptures,
		Operations: operations,
		Warnings:   plan.Warnings,
	})
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func mapProviderStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Provider already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProviderInUse, "Provider is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Profile store operation failed", err)
	}
}
