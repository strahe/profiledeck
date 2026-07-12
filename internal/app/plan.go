package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
	"unicode/utf8"

	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	planActionCreate      = "create"
	planActionUpdate      = "update"
	planActionNoop        = "noop"
	planActionUnsupported = "unsupported"

	planReasonTargetMissing          = "target_missing"
	planReasonTargetSameContent      = "target_same_content"
	planReasonTargetDifferentContent = "target_different_content"
	planReasonTargetIsSymlink        = "target_is_symlink"

	maxTargetContentBytes = targetfs.MaxFileBytes
)

type BuildPlanRequest struct {
	ConfigDir  string
	ProviderID string
	ProfileID  string
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
	sensitive                 bool
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

type planAdapter interface {
	ID() string
	Prepare(ctx context.Context, input planAdapterInput) (planAdapterPrepared, error)
	Finalize(ctx context.Context, input planAdapterInput, prepared planAdapterPrepared, snapshots map[string]targetSnapshot) (planAdapterResult, error)
	ResolveTargetSpec(providerID, targetID, backendID, path, label string) (targetSpec, error)
}

type preparedTarget struct {
	Spec targetSpec
	Data any
}

type planAdapterPrepared struct {
	Targets []preparedTarget
	Data    any
}

type planAdapterResult struct {
	Operations        []applyPlanOperation
	Warnings          []string
	Bindings          []PlanBinding
	StateCaptures     []StateCapture
	CredentialUpdates []store.UpsertProviderCredentialParams
	ConfigSetUpdates  []store.UpsertProviderConfigSetParams
}

type planAdapterInput struct {
	Provider store.Provider
	Profile  store.Profile
	Targets  []store.ProfileTarget
	// Store is provided to adapters that must materialize desired content from
	// application-owned records while still leaving all writes to the switch pipeline.
	Store *store.Store
}

var planAdapters = map[string]planAdapter{
	"generic":             genericPlanAdapter{},
	codexconfig.AdapterID: codexPlanAdapter{},
	agyconfig.AdapterID:   antigravityPlanAdapter{},
}

func BuildPlan(ctx context.Context, req BuildPlanRequest) (SwitchPlan, error) {
	providerID, appErr := validateID(req.ProviderID, ErrorProviderInvalid)
	if appErr != nil {
		return SwitchPlan{}, appErr
	}
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return SwitchPlan{}, appErr
	}

	// Plan generation must not mutate ProfileDeck state; switch apply will rebuild the plan under lock.
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return SwitchPlan{}, err
	}
	defer db.Close()

	plan, err := buildApplyPlan(ctx, db, providerID, profileID)
	if err != nil {
		return SwitchPlan{}, err
	}
	return plan.SwitchPlan, nil
}

func buildApplyPlan(ctx context.Context, db *store.Store, providerID, profileID string) (applyPlan, error) {
	provider, err := db.GetProvider(ctx, providerID)
	if err != nil {
		return applyPlan{}, mapProviderStoreError(err)
	}
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return applyPlan{}, mapProfileStoreError(err)
	}
	if !provider.Enabled {
		return applyPlan{}, NewError(ErrorProviderDisabled, "provider is disabled")
	}
	if appErr := validateManagedProviderAdapter(provider); appErr != nil {
		return applyPlan{}, appErr
	}

	adapter, ok := planAdapters[provider.AdapterID]
	if !ok {
		return applyPlan{}, NewError(ErrorAdapterNotFound, "adapter not found")
	}

	targets, err := db.ListProfileTargets(ctx, profile.ID, provider.ID, false)
	if err != nil {
		return applyPlan{}, WrapError(ErrorStoreStatusFailed, "failed to list profile targets", err)
	}
	if provider.AdapterID == codexconfig.AdapterID {
		targets, err = storedCodexBindingTargets(ctx, db, profile.ID)
		if err != nil {
			return applyPlan{}, err
		}
	}

	input := planAdapterInput{
		Provider: provider,
		Profile:  profile,
		Targets:  targets,
		Store:    db,
	}
	prepared, err := adapter.Prepare(ctx, input)
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, WrapError(ErrorPlanBuildFailed, "failed to prepare switch plan", err)
	}
	snapshots, err := inspectPreparedTargets(ctx, prepared.Targets)
	if err != nil {
		return applyPlan{}, err
	}
	adapterResult, err := adapter.Finalize(ctx, input, prepared, snapshots)
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, WrapError(ErrorPlanBuildFailed, "failed to build switch plan", err)
	}
	if appErr := validateFinalizedPlanResult(input, prepared, snapshots, adapterResult); appErr != nil {
		return applyPlan{}, appErr
	}

	publicOperations := make([]PlanOperation, 0, len(adapterResult.Operations))
	for _, op := range adapterResult.Operations {
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
		Bindings:      adapterResult.Bindings,
		StateCaptures: adapterResult.StateCaptures,
		Operations:    publicOperations,
		Warnings:      adapterResult.Warnings,
	}
	// Fingerprint the complete internal state before removing secret-derived
	// capture hashes from the public DTO.
	plan.PlanFingerprint = fingerprintSwitchPlan(plan)
	plan.StateCaptures = publicStateCaptures(adapterResult.StateCaptures, adapterResult.Operations)
	return applyPlan{
		SwitchPlan:        plan,
		Operations:        adapterResult.Operations,
		CredentialUpdates: adapterResult.CredentialUpdates,
		ConfigSetUpdates:  adapterResult.ConfigSetUpdates,
	}, nil
}

func validateFinalizedPlanResult(input planAdapterInput, prepared planAdapterPrepared, snapshots map[string]targetSnapshot, result planAdapterResult) *AppError {
	if len(result.Operations) != len(prepared.Targets) {
		return NewError(ErrorPlanBuildFailed, "finalized switch plan operation count does not match prepared targets")
	}
	preparedSpecs := make(map[string]targetSpec, len(prepared.Targets))
	for _, target := range prepared.Targets {
		if target.Spec == nil {
			return NewError(ErrorPlanBuildFailed, "prepared switch target spec is missing")
		}
		preparedSpecs[target.Spec.TargetID()] = target.Spec
	}
	seen := make(map[string]struct{}, len(result.Operations))
	for _, operation := range result.Operations {
		if operation.Spec == nil {
			return NewError(ErrorPlanBuildFailed, "finalized switch target spec is missing").WithDetail("target_id", operation.TargetID)
		}
		targetID := operation.Spec.TargetID()
		preparedSpec, ok := preparedSpecs[targetID]
		if !ok || operation.TargetID != targetID {
			return NewError(ErrorPlanBuildFailed, "finalized switch target was not prepared").WithDetail("target_id", operation.TargetID)
		}
		if _, exists := seen[targetID]; exists {
			return NewError(ErrorPlanBuildFailed, "finalized switch plan contains duplicate target IDs").WithDetail("target_id", targetID)
		}
		seen[targetID] = struct{}{}
		if operation.Spec.BackendID() != preparedSpec.BackendID() ||
			operation.Spec.LocatorFingerprint() != preparedSpec.LocatorFingerprint() ||
			operation.BackendID != preparedSpec.BackendID() ||
			operation.locatorFingerprint != preparedSpec.LocatorFingerprint() ||
			operation.TargetLabel != preparedSpec.SafeLabel() ||
			operation.sensitive != preparedSpec.Sensitive() {
			return NewError(ErrorPlanBuildFailed, "finalized switch target does not match prepared target").WithDetail("target_id", targetID)
		}
		if fileSpec, ok := preparedSpec.(fileTargetSpec); ok {
			if operation.Path != fileSpec.Path {
				return NewError(ErrorPlanBuildFailed, "finalized file path does not match prepared target").WithDetail("target_id", targetID)
			}
		} else if operation.Path != "" || operation.Format != "" || operation.Strategy != "" {
			return NewError(ErrorPlanBuildFailed, "finalized non-file target contains file details").WithDetail("target_id", targetID)
		}
		expectedSnapshot, ok := snapshots[targetID]
		if !ok || operation.Snapshot != expectedSnapshot || operation.FileExists != expectedSnapshot.Exists || operation.IsSymlink != expectedSnapshot.IsSymlink {
			return NewError(ErrorPlanBuildFailed, "finalized switch snapshot does not match inspected target").WithDetail("target_id", targetID)
		}
		if operation.ProviderID != input.Provider.ID || operation.ProfileID != input.Profile.ID {
			return NewError(ErrorPlanBuildFailed, "finalized switch operation scope is invalid").WithDetail("target_id", targetID)
		}
		switch operation.Action {
		case planActionCreate, planActionUpdate, planActionNoop:
			desiredFingerprint := sha256HexString(operation.DesiredContent)
			if firstNonEmpty(operation.privateBeforeFingerprint, operation.BeforeSHA256) != expectedSnapshot.Fingerprint ||
				firstNonEmpty(operation.privateDesiredFingerprint, operation.DesiredSHA256) != desiredFingerprint {
				return NewError(ErrorPlanBuildFailed, "finalized switch content fingerprint is invalid").WithDetail("target_id", targetID)
			}
			switch operation.Action {
			case planActionCreate:
				if expectedSnapshot.Exists {
					return NewError(ErrorPlanBuildFailed, "finalized create operation targets existing state").WithDetail("target_id", targetID)
				}
			case planActionUpdate:
				if !expectedSnapshot.Exists {
					return NewError(ErrorPlanBuildFailed, "finalized update operation targets missing state").WithDetail("target_id", targetID)
				}
			case planActionNoop:
				if !expectedSnapshot.Exists || expectedSnapshot.Fingerprint != desiredFingerprint {
					return NewError(ErrorPlanBuildFailed, "finalized no-op does not match inspected target").WithDetail("target_id", targetID)
				}
			}
		case planActionUnsupported:
		default:
			return NewError(ErrorPlanBuildFailed, "finalized switch action is invalid").WithDetail("target_id", targetID)
		}
	}
	for _, update := range result.CredentialUpdates {
		if update.ProviderID != input.Provider.ID {
			return NewError(ErrorPlanBuildFailed, "finalized credential capture has invalid Provider scope")
		}
	}
	for _, update := range result.ConfigSetUpdates {
		if update.ProviderID != input.Provider.ID {
			return NewError(ErrorPlanBuildFailed, "finalized Config Set capture has invalid Provider scope")
		}
	}
	return nil
}

func validateManagedProviderAdapter(provider store.Provider) *AppError {
	switch provider.ID {
	case codexconfig.ProviderID:
		if provider.AdapterID != codexconfig.AdapterID {
			return NewError(ErrorCodexInvalid, "Codex provider uses an incompatible adapter")
		}
	case agyconfig.ProviderID:
		if provider.AdapterID != agyconfig.AdapterID {
			return NewError(ErrorAntigravityInvalid, "Antigravity provider uses an incompatible adapter")
		}
	}
	return nil
}

func publicStateCaptures(captures []StateCapture, operations []applyPlanOperation) []StateCapture {
	redactHashes := false
	for _, operation := range operations {
		if operation.sensitive && operation.BackendID != targetBackendFile {
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
	if !operation.sensitive || operation.BackendID == targetBackendFile {
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
	operation.BeforeSHA256 = ""
	operation.DesiredSHA256 = ""
	operation.BeforePreview = TextPreview{}
	operation.DesiredPreview = TextPreview{}
	operation.AfterPreview = TextPreview{}
	return operation
}

type genericPlanAdapter struct{}

func (genericPlanAdapter) ID() string {
	return "generic"
}

func (genericPlanAdapter) ResolveTargetSpec(_, targetID, backendID, path, label string) (targetSpec, error) {
	return resolveFileTargetSpec(targetID, backendID, path, label)
}

func (genericPlanAdapter) Prepare(_ context.Context, input planAdapterInput) (planAdapterPrepared, error) {
	targets := make([]preparedTarget, 0, len(input.Targets))
	for _, target := range input.Targets {
		targets = append(targets, preparedTarget{
			Spec: fileTargetSpec{
				ID: target.TargetID, Path: target.Path,
				NeedsContent: targetStrategyNeedsContent(target.Strategy), Label: target.Path,
			},
			Data: target,
		})
	}
	return planAdapterPrepared{Targets: targets}, nil
}

func (genericPlanAdapter) Finalize(_ context.Context, input planAdapterInput, prepared planAdapterPrepared, snapshots map[string]targetSnapshot) (planAdapterResult, error) {
	operations := make([]applyPlanOperation, 0, len(input.Targets))
	warnings := []string{}
	seenWarnings := map[string]struct{}{}
	for _, item := range prepared.Targets {
		target, ok := item.Data.(store.ProfileTarget)
		if !ok {
			return planAdapterResult{}, NewError(ErrorPlanBuildFailed, "generic plan target is invalid")
		}
		op, err := buildGenericPlanOperationFromSnapshot(input.Provider, input.Profile, target, item.Spec, snapshots[target.TargetID])
		if err != nil {
			return planAdapterResult{}, err
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
	return planAdapterResult{Operations: operations, Warnings: warnings}, nil
}

func buildGenericPlanOperation(ctx context.Context, provider store.Provider, profile store.Profile, target store.ProfileTarget) (applyPlanOperation, error) {
	spec := fileTargetSpec{ID: target.TargetID, Path: target.Path, NeedsContent: targetStrategyNeedsContent(target.Strategy), Label: target.Path}
	snapshot, err := (fileTargetBackend{}).Inspect(ctx, spec)
	if err != nil {
		return applyPlanOperation{}, err
	}
	return buildGenericPlanOperationFromSnapshot(provider, profile, target, spec, snapshot)
}

func buildGenericPlanOperationFromSnapshot(provider store.Provider, profile store.Profile, target store.ProfileTarget, spec targetSpec, before targetSnapshot) (applyPlanOperation, error) {
	op := applyPlanOperation{
		PlanOperation: PlanOperation{
			ProviderID:         provider.ID,
			ProfileID:          profile.ID,
			TargetID:           target.TargetID,
			BackendID:          spec.BackendID(),
			TargetLabel:        spec.SafeLabel(),
			Path:               target.Path,
			Format:             target.Format,
			Strategy:           target.Strategy,
			locatorFingerprint: spec.LocatorFingerprint(),
			sensitive:          spec.Sensitive(),
		},
		Spec: spec, Snapshot: before,
	}
	op.FileExists = before.Exists
	op.IsSymlink = before.IsSymlink
	op.BeforeMode = before.Mode
	if before.IsSymlink {
		op.Action = planActionUnsupported
		op.StatusReason = planReasonTargetIsSymlink
		op.Warnings = append(op.Warnings, "target path is a symlink and will not be followed")
		return op, nil
	}
	if before.Exists {
		op.BeforeSHA256 = before.Fingerprint
		op.BeforePreview = before.Preview
	}

	content, warnings, err := desiredTargetContent(target, targetPlanRead{
		FileExists: before.Exists, IsSymlink: before.IsSymlink, SHA256: before.Fingerprint,
		Mode: before.Mode, Preview: before.Preview, Content: before.Content,
	})
	if err != nil {
		return applyPlanOperation{}, err
	}
	if len(content) > maxTargetContentBytes {
		return applyPlanOperation{}, NewError(ErrorTargetInvalid, "desired target content is too large").
			WithDetail("target_id", target.TargetID).
			WithDetail("path", target.Path).
			WithDetail("size_bytes", len(content)).
			WithDetail("max_bytes", maxTargetContentBytes)
	}
	op.DesiredContent = content
	op.Warnings = append(op.Warnings, warnings...)
	op.DesiredSHA256 = sha256HexString(content)
	op.privateBeforeFingerprint = before.Fingerprint
	op.privateDesiredFingerprint = op.DesiredSHA256
	op.DesiredPreview = previewSensitiveText(content)
	op.AfterPreview = op.DesiredPreview

	if !before.Exists {
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

type targetPlanRead struct {
	FileExists bool
	IsSymlink  bool
	SHA256     string
	Mode       os.FileMode
	Preview    TextPreview
	Content    string
}

func targetStrategyNeedsContent(strategy string) bool {
	switch strategy {
	case targetStrategyJSONMerge, targetStrategyTOMLMerge, targetStrategyEnvMerge:
		return true
	default:
		return false
	}
}

func readTargetForPlan(ctx context.Context, path string, needsContent bool) (targetPlanRead, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return targetPlanRead{}, nil
		}
		return targetPlanRead{}, WrapError(ErrorTargetReadFailed, "failed to inspect target file", err).WithDetail("path", path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return targetPlanRead{FileExists: true, IsSymlink: true, Mode: info.Mode()}, nil
	}
	if info.IsDir() {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, NewError(ErrorTargetReadFailed, "target path is a directory").WithDetail("path", path)
	}
	if !info.Mode().IsRegular() {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, NewError(ErrorTargetReadFailed, "target path is not a regular file").WithDetail("path", path)
	}
	if info.Size() > maxTargetContentBytes {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, NewError(ErrorTargetReadFailed, "target file is too large").
			WithDetail("path", path).
			WithDetail("size_bytes", info.Size()).
			WithDetail("max_bytes", maxTargetContentBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, WrapError(ErrorTargetReadFailed, "failed to read target file", err).WithDetail("path", path)
	}
	defer file.Close()

	hash := sha256.New()
	preview := &prefixPreviewWriter{maxBytes: maxPreviewBytes}
	var content bytes.Buffer
	reader := io.Reader(io.LimitReader(contextReader{ctx: ctx, reader: file}, maxTargetContentBytes+1))
	writer := io.MultiWriter(hash, preview)
	if needsContent {
		writer = io.MultiWriter(hash, preview, &content)
	}
	read, err := io.Copy(writer, reader)
	if err != nil {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, WrapError(ErrorTargetReadFailed, "failed to read target file", err).WithDetail("path", path)
	}
	if read > maxTargetContentBytes {
		return targetPlanRead{FileExists: true, Mode: info.Mode()}, NewError(ErrorTargetReadFailed, "target file is too large").
			WithDetail("path", path).
			WithDetail("max_bytes", maxTargetContentBytes)
	}
	return targetPlanRead{
		FileExists: true,
		SHA256:     hex.EncodeToString(hash.Sum(nil)),
		Mode:       info.Mode(),
		Preview:    preview.TextPreview(),
		Content:    content.String(),
	}, nil
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

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if r.ctx != nil {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
	}
	return r.reader.Read(p)
}

type prefixPreviewWriter struct {
	maxBytes int
	total    int64
	buf      []byte
}

func (w *prefixPreviewWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	limit := w.maxBytes + utf8.UTFMax
	if len(w.buf) < limit {
		remaining := limit - len(w.buf)
		if remaining > len(p) {
			remaining = len(p)
		}
		w.buf = append(w.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (w *prefixPreviewWriter) TextPreview() TextPreview {
	preview := previewSensitiveText(string(w.buf))
	preview.Truncated = preview.Truncated || w.total > int64(w.maxBytes)
	return preview
}

func replaceFileContentFromValueJSON(raw string) (string, error) {
	value, appErr := decodeSingleJSONObject(raw, ErrorTargetInvalid, "stored value_json")
	if appErr != nil {
		return "", appErr
	}
	content, ok := value["content"].(string)
	if !ok || len(value) != 1 {
		return "", NewError(ErrorTargetInvalid, `stored replace-file value_json must be {"content": string}`)
	}
	return content, nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func sha256HexString(value string) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, value)
	return hex.EncodeToString(hash.Sum(nil))
}
