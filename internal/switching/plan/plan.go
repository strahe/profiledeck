package plan

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	ActionCreate      = "create"
	ActionUpdate      = "update"
	ActionNoop        = "noop"
	ActionUnsupported = "unsupported"

	ReasonTargetMissing          = "target_missing"
	ReasonTargetSameContent      = "target_same_content"
	ReasonTargetDifferentContent = "target_different_content"
	ReasonTargetModeDifferent    = "target_mode_different"
	ReasonTargetIsSymlink        = "target_is_symlink"
)

const MaxTargetContentBytes = targetfs.MaxFileBytes

type Preview struct {
	Content   string
	Truncated bool
}

func PreviewFromTarget(value profiletarget.Preview) Preview {
	return Preview{Content: value.Content, Truncated: value.Truncated}
}

func SensitivePreview(content string) Preview {
	return PreviewFromTarget(profiletarget.PreviewSensitiveText(content))
}

// Binding records one managed resource binding change for a finalized plan.
type Binding struct {
	TargetID          string
	CurrentResourceID string
	TargetResourceID  string
	Changed           bool
}

// StateCapture represents a working-copy capture that must commit with active state.
type StateCapture struct {
	ResourceKind  string
	ResourceID    string
	ResourceName  string
	StoredSHA256  string
	CurrentSHA256 string
	Changed       bool
}

// Operation is a target action before external execution. It is not a Wails DTO.
type Operation struct {
	ProviderID                string
	ProfileID                 string
	TargetID                  string
	BackendID                 string
	TargetLabel               string
	Path                      string
	Format                    string
	Strategy                  string
	Action                    string
	StatusReason              string
	FileExists                bool
	IsSymlink                 bool
	BeforeSHA256              string
	DesiredSHA256             string
	BeforePreview             Preview
	DesiredPreview            Preview
	AfterPreview              Preview
	Warnings                  []string
	LocatorFingerprint        string
	PrivateBeforeFingerprint  string
	PrivateDesiredFingerprint string
	PrivateRecoveryLocator    string
	PrivateObjectFingerprint  string
	Sensitive                 bool
}

// ApplyOperation retains execution-only state after final plan validation.
type ApplyOperation struct {
	Operation
	DesiredContent string
	BeforeMode     os.FileMode
	DesiredMode    os.FileMode
	UseDesiredMode bool
	Spec           target.Spec
	Snapshot       target.Snapshot
}

// PreparedTarget couples a physical target description with adapter-private data.
type PreparedTarget struct {
	Spec target.Spec
	Data any
}

// Prepared is the first phase of an adapter: target descriptions without I/O.
type Prepared struct {
	Targets []PreparedTarget
	Data    any
}

// Result is the second phase of an adapter, after target inspection.
type Result struct {
	Operations        []ApplyOperation
	Warnings          []string
	Bindings          []Binding
	StateCaptures     []StateCapture
	CredentialUpdates []CredentialUpdate
	ConfigSetUpdates  []ConfigSetUpdate
}

// Input is the application-owned state an adapter may read while constructing desired content.
type Input struct {
	Provider Provider
	Profile  Profile
	Targets  []Target
	State    StateReader
}

// Adapter produces a plan only; it never mutates SQLite or external targets.
type Adapter interface {
	ID() string
	ManagedProviderIDs() []string
	LoadTargets(context.Context, Input) ([]Target, error)
	Prepare(context.Context, Input) (Prepared, error)
	Finalize(context.Context, Input, Prepared, map[string]target.Snapshot) (Result, error)
	ResolveTargetSpec(providerID, targetID, backendID, path, label string) (target.Spec, error)
}

// GenericAdapter implements ProfileDeck's file-backed generic target contract.
type GenericAdapter struct{}

func (GenericAdapter) ID() string { return "generic" }

func (GenericAdapter) ManagedProviderIDs() []string { return nil }

func (GenericAdapter) LoadTargets(_ context.Context, input Input) ([]Target, error) {
	return append([]Target(nil), input.Targets...), nil
}

func (GenericAdapter) ResolveTargetSpec(_, targetID, backendID, path, label string) (target.Spec, error) {
	return target.ResolveFileSpec(targetID, backendID, path, label)
}

func (GenericAdapter) Prepare(_ context.Context, input Input) (Prepared, error) {
	targets := make([]PreparedTarget, 0, len(input.Targets))
	for _, stored := range input.Targets {
		targets = append(targets, PreparedTarget{
			Spec: target.FileSpec{
				ID: stored.TargetID, Path: stored.Path, NeedsContent: StrategyNeedsContent(stored.Strategy), Label: stored.Path,
			},
			Data: stored,
		})
	}
	return Prepared{Targets: targets}, nil
}

func (GenericAdapter) Finalize(_ context.Context, input Input, prepared Prepared, snapshots map[string]target.Snapshot) (Result, error) {
	operations := make([]ApplyOperation, 0, len(input.Targets))
	warnings := []string{}
	seenWarnings := map[string]struct{}{}
	for _, item := range prepared.Targets {
		stored, ok := item.Data.(Target)
		if !ok {
			return Result{}, apperror.New(apperror.PlanBuildFailed, "generic plan target is invalid")
		}
		before, ok := snapshots[stored.TargetID]
		if !ok {
			return Result{}, apperror.New(apperror.PlanBuildFailed, "generic target snapshot is missing").WithDetail("target_id", stored.TargetID)
		}
		operation, err := BuildGenericOperation(input.Provider, input.Profile, stored, item.Spec, before)
		if err != nil {
			return Result{}, err
		}
		operations = append(operations, operation)
		for _, warning := range operation.Warnings {
			if _, exists := seenWarnings[warning]; exists {
				continue
			}
			seenWarnings[warning] = struct{}{}
			warnings = append(warnings, warning)
		}
	}
	return Result{Operations: operations, Warnings: warnings}, nil
}

// InspectPrepared keeps all target uniqueness and backend resolution below app.
func InspectPrepared(ctx context.Context, registry target.Registry, prepared Prepared) (map[string]target.Snapshot, error) {
	specs := make([]target.Spec, 0, len(prepared.Targets))
	for _, preparedTarget := range prepared.Targets {
		specs = append(specs, preparedTarget.Spec)
	}
	return registry.InspectAll(ctx, specs)
}

// ValidateResult verifies that an adapter's second phase cannot substitute inspected targets.
func ValidateResult(input Input, prepared Prepared, snapshots map[string]target.Snapshot, result Result) *apperror.Error {
	if len(result.Operations) != len(prepared.Targets) {
		return apperror.New(apperror.PlanBuildFailed, "finalized switch plan operation count does not match prepared targets")
	}
	preparedSpecs := make(map[string]target.Spec, len(prepared.Targets))
	for _, preparedTarget := range prepared.Targets {
		if preparedTarget.Spec == nil {
			return apperror.New(apperror.PlanBuildFailed, "prepared switch target spec is missing")
		}
		preparedSpecs[preparedTarget.Spec.TargetID()] = preparedTarget.Spec
	}
	seen := make(map[string]struct{}, len(result.Operations))
	for _, operation := range result.Operations {
		if operation.Spec == nil {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch target spec is missing").WithDetail("target_id", operation.TargetID)
		}
		targetID := operation.Spec.TargetID()
		preparedSpec, ok := preparedSpecs[targetID]
		if !ok || operation.TargetID != targetID {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch target was not prepared").WithDetail("target_id", operation.TargetID)
		}
		if _, exists := seen[targetID]; exists {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch plan contains duplicate target IDs").WithDetail("target_id", targetID)
		}
		seen[targetID] = struct{}{}
		if operation.Spec.BackendID() != preparedSpec.BackendID() ||
			operation.Spec.LocatorFingerprint() != preparedSpec.LocatorFingerprint() ||
			operation.BackendID != preparedSpec.BackendID() ||
			operation.LocatorFingerprint != preparedSpec.LocatorFingerprint() ||
			operation.TargetLabel != preparedSpec.SafeLabel() ||
			operation.Sensitive != preparedSpec.Sensitive() {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch target does not match prepared target").WithDetail("target_id", targetID)
		}
		if fileSpec, ok := preparedSpec.(target.FileSpec); ok {
			if operation.Path != fileSpec.Path {
				return apperror.New(apperror.PlanBuildFailed, "finalized file path does not match prepared target").WithDetail("target_id", targetID)
			}
		} else if operation.Path != "" || operation.Format != "" || operation.Strategy != "" {
			return apperror.New(apperror.PlanBuildFailed, "finalized non-file target contains file details").WithDetail("target_id", targetID)
		}
		expectedSnapshot, ok := snapshots[targetID]
		if !ok || operation.Snapshot != expectedSnapshot || operation.FileExists != expectedSnapshot.Exists || operation.IsSymlink != expectedSnapshot.IsSymlink {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch snapshot does not match inspected target").WithDetail("target_id", targetID)
		}
		if recoverySpec, ok := preparedSpec.(target.RecoveryIdentitySpec); ok {
			if operation.PrivateRecoveryLocator != recoverySpec.RecoveryLocator() ||
				operation.PrivateObjectFingerprint != recoverySpec.ObjectFingerprint(expectedSnapshot) {
				return apperror.New(apperror.PlanBuildFailed, "finalized switch recovery identity is invalid").WithDetail("target_id", targetID)
			}
		} else if operation.PrivateRecoveryLocator != "" || operation.PrivateObjectFingerprint != "" {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch recovery identity is unexpected").WithDetail("target_id", targetID)
		}
		if operation.ProviderID != input.Provider.ID || operation.ProfileID != input.Profile.ID {
			return apperror.New(apperror.PlanBuildFailed, "finalized switch operation scope is invalid").WithDetail("target_id", targetID)
		}
		switch operation.Action {
		case ActionCreate, ActionUpdate, ActionNoop:
			desiredFingerprint := target.SHA256String(operation.DesiredContent)
			if firstNonEmpty(operation.PrivateBeforeFingerprint, operation.BeforeSHA256) != expectedSnapshot.Fingerprint ||
				firstNonEmpty(operation.PrivateDesiredFingerprint, operation.DesiredSHA256) != desiredFingerprint {
				return apperror.New(apperror.PlanBuildFailed, "finalized switch content fingerprint is invalid").WithDetail("target_id", targetID)
			}
			switch operation.Action {
			case ActionCreate:
				if expectedSnapshot.Exists {
					return apperror.New(apperror.PlanBuildFailed, "finalized create operation targets existing state").WithDetail("target_id", targetID)
				}
			case ActionUpdate:
				if !expectedSnapshot.Exists {
					return apperror.New(apperror.PlanBuildFailed, "finalized update operation targets missing state").WithDetail("target_id", targetID)
				}
			case ActionNoop:
				if !expectedSnapshot.Exists || expectedSnapshot.Fingerprint != desiredFingerprint {
					return apperror.New(apperror.PlanBuildFailed, "finalized no-op does not match inspected target").WithDetail("target_id", targetID)
				}
			}
		case ActionUnsupported:
		default:
			return apperror.New(apperror.PlanBuildFailed, "finalized switch action is invalid").WithDetail("target_id", targetID)
		}
	}
	for _, update := range result.CredentialUpdates {
		if update.ProviderID != input.Provider.ID {
			return apperror.New(apperror.PlanBuildFailed, "finalized credential capture has invalid Provider scope")
		}
	}
	for _, update := range result.ConfigSetUpdates {
		if update.ProviderID != input.Provider.ID {
			return apperror.New(apperror.PlanBuildFailed, "finalized Config Set capture has invalid Provider scope")
		}
	}
	return nil
}

// BuildGenericOperation finalizes generic file content from an inspected target.
func BuildGenericOperation(provider Provider, profile Profile, stored Target, spec target.Spec, before target.Snapshot) (ApplyOperation, error) {
	if spec == nil {
		return ApplyOperation{}, apperror.New(apperror.PlanBuildFailed, "generic target spec is missing")
	}
	operation := ApplyOperation{
		Operation: Operation{
			ProviderID: provider.ID, ProfileID: profile.ID, TargetID: stored.TargetID,
			BackendID: spec.BackendID(), TargetLabel: spec.SafeLabel(), Path: stored.Path,
			Format: stored.Format, Strategy: stored.Strategy, LocatorFingerprint: spec.LocatorFingerprint(), Sensitive: spec.Sensitive(),
		},
		Spec: spec, Snapshot: before,
	}
	operation.FileExists = before.Exists
	operation.IsSymlink = before.IsSymlink
	operation.BeforeMode = before.Mode
	if before.IsSymlink {
		operation.Action = ActionUnsupported
		operation.StatusReason = ReasonTargetIsSymlink
		operation.Warnings = append(operation.Warnings, "target path is a symlink and will not be followed")
		return operation, nil
	}
	if before.Exists {
		operation.BeforeSHA256 = before.Fingerprint
		operation.BeforePreview = PreviewFromTarget(before.Preview)
	}
	content, warnings, err := profiletarget.DesiredContent(profiletarget.Target{
		Path: stored.Path, Format: stored.Format, Strategy: stored.Strategy, ValueJSON: stored.ValueJSON,
	}, profiletarget.Read{Exists: before.Exists, Content: before.Content})
	if err != nil {
		return ApplyOperation{}, err
	}
	if len(content) > targetfs.MaxFileBytes {
		return ApplyOperation{}, apperror.New(apperror.TargetInvalid, "desired target content is too large").
			WithDetail("target_id", stored.TargetID).WithDetail("path", stored.Path).
			WithDetail("size_bytes", len(content)).WithDetail("max_bytes", targetfs.MaxFileBytes)
	}
	operation.DesiredContent = content
	operation.Warnings = append(operation.Warnings, warnings...)
	operation.DesiredSHA256 = target.SHA256String(content)
	operation.PrivateBeforeFingerprint = before.Fingerprint
	operation.PrivateDesiredFingerprint = operation.DesiredSHA256
	operation.DesiredPreview = SensitivePreview(content)
	operation.AfterPreview = operation.DesiredPreview
	if !before.Exists {
		operation.Action = ActionCreate
		operation.StatusReason = ReasonTargetMissing
		return operation, nil
	}
	if operation.BeforeSHA256 == operation.DesiredSHA256 {
		operation.Action = ActionNoop
		operation.StatusReason = ReasonTargetSameContent
		return operation, nil
	}
	operation.Action = ActionUpdate
	operation.StatusReason = ReasonTargetDifferentContent
	return operation, nil
}

func StrategyNeedsContent(strategy string) bool {
	switch strategy {
	case profiletarget.StrategyJSONMerge, profiletarget.StrategyTOMLMerge, profiletarget.StrategyEnvMerge:
		return true
	default:
		return false
	}
}

func IsAppError(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
