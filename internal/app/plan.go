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

	"github.com/strahe/profiledeck/internal/store"
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

	maxTargetContentBytes = 16 * 1024 * 1024
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
	Operations      []PlanOperation `json:"operations"`
	Warnings        []string        `json:"warnings"`
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
	ProviderID     string      `json:"provider_id"`
	ProfileID      string      `json:"profile_id"`
	TargetID       string      `json:"target_id"`
	Path           string      `json:"path"`
	Format         string      `json:"format"`
	Strategy       string      `json:"strategy"`
	Action         string      `json:"action"`
	StatusReason   string      `json:"status_reason"`
	FileExists     bool        `json:"file_exists"`
	IsSymlink      bool        `json:"is_symlink"`
	BeforeSHA256   string      `json:"before_sha256"`
	DesiredSHA256  string      `json:"desired_sha256"`
	BeforePreview  TextPreview `json:"before_preview"`
	DesiredPreview TextPreview `json:"desired_preview"`
	AfterPreview   TextPreview `json:"after_preview"`
	Warnings       []string    `json:"warnings"`
}

type applyPlan struct {
	SwitchPlan SwitchPlan
	Operations []applyPlanOperation
}

type applyPlanOperation struct {
	PlanOperation
	DesiredContent string
	BeforeMode     os.FileMode
}

type planAdapter interface {
	ID() string
	Build(ctx context.Context, input planAdapterInput) ([]applyPlanOperation, []string, error)
}

type planAdapterInput struct {
	Provider store.Provider
	Profile  store.Profile
	Targets  []store.ProfileTarget
}

var planAdapters = map[string]planAdapter{
	"generic": genericPlanAdapter{},
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

func buildApplyPlan(ctx context.Context, db *store.Store, providerID string, profileID string) (applyPlan, error) {
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

	adapter, ok := planAdapters[provider.AdapterID]
	if !ok {
		return applyPlan{}, NewError(ErrorAdapterNotFound, "adapter not found")
	}

	targets, err := db.ListProfileTargets(ctx, profile.ID, provider.ID, false)
	if err != nil {
		return applyPlan{}, WrapError(ErrorStoreStatusFailed, "failed to list profile targets", err)
	}

	operations, warnings, err := adapter.Build(ctx, planAdapterInput{
		Provider: provider,
		Profile:  profile,
		Targets:  targets,
	})
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			return applyPlan{}, appErr
		}
		return applyPlan{}, WrapError(ErrorPlanBuildFailed, "failed to build switch plan", err)
	}

	publicOperations := make([]PlanOperation, 0, len(operations))
	for _, op := range operations {
		publicOperations = append(publicOperations, op.PlanOperation)
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
		Operations: publicOperations,
		Warnings:   warnings,
	}
	plan.PlanFingerprint = fingerprintSwitchPlan(plan)
	return applyPlan{
		SwitchPlan: plan,
		Operations: operations,
	}, nil
}

type genericPlanAdapter struct{}

func (genericPlanAdapter) ID() string {
	return "generic"
}

func (genericPlanAdapter) Build(ctx context.Context, input planAdapterInput) ([]applyPlanOperation, []string, error) {
	operations := make([]applyPlanOperation, 0, len(input.Targets))
	warnings := []string{}
	seenWarnings := map[string]struct{}{}
	for _, target := range input.Targets {
		op, err := buildGenericPlanOperation(ctx, input.Provider, input.Profile, target)
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

func buildGenericPlanOperation(ctx context.Context, provider store.Provider, profile store.Profile, target store.ProfileTarget) (applyPlanOperation, error) {
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

	before, err := readTargetForPlan(ctx, target.Path, targetStrategyNeedsContent(target.Strategy))
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

	content, warnings, err := desiredTargetContent(target, before)
	if err != nil {
		return applyPlanOperation{}, err
	}
	op.DesiredContent = content
	op.Warnings = append(op.Warnings, warnings...)
	op.DesiredSHA256 = sha256HexString(content)
	op.DesiredPreview = previewSensitiveText(content)
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
		Operations []fingerprintOperation `json:"operations"`
		Warnings   []string               `json:"warnings"`
	}

	operations := make([]fingerprintOperation, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		operations = append(operations, fingerprintOperation{
			ProviderID:    op.ProviderID,
			ProfileID:     op.ProfileID,
			TargetID:      op.TargetID,
			Path:          op.Path,
			Format:        op.Format,
			Strategy:      op.Strategy,
			Action:        op.Action,
			StatusReason:  op.StatusReason,
			FileExists:    op.FileExists,
			IsSymlink:     op.IsSymlink,
			BeforeSHA256:  op.BeforeSHA256,
			DesiredSHA256: op.DesiredSHA256,
			Warnings:      op.Warnings,
		})
	}

	raw, err := json.Marshal(fingerprintPayload{
		ProviderID: plan.Provider.ID,
		AdapterID:  plan.Provider.AdapterID,
		ProfileID:  plan.Profile.ID,
		Operations: operations,
		Warnings:   plan.Warnings,
	})
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
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
