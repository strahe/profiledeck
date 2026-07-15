// Package target defines physical switch targets and their storage backends.
package target

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
)

const (
	BackendFile               = "file"
	BackendKeyring            = "keyring"
	BackendClaudeCodeKeychain = "claude-code-keychain"
)

// Spec describes one physical target without Profile or resource ownership.
type Spec interface {
	BackendID() string
	TargetID() string
	SafeLabel() string
	LocatorFingerprint() string
	Sensitive() bool
}

// RecoveryIdentitySpec lets a target bind recovery metadata to the exact
// physical object inspected while the plan was built.
type RecoveryIdentitySpec interface {
	Spec
	RecoveryLocator() string
	ObjectFingerprint(Snapshot) string
}

// RecoveryModeSpec preserves a storage-specific permission floor while
// restoring historical content. It does not affect read-only inspection.
type RecoveryModeSpec interface {
	Spec
	RecoveryMode(os.FileMode, bool) (os.FileMode, bool)
}

// FileSpec describes one managed file target.
type FileSpec struct {
	ID                   string
	Path                 string
	NeedsContent         bool
	Secret               bool
	Label                string
	EnforcedRecoveryMode os.FileMode
}

func (spec FileSpec) BackendID() string { return BackendFile }
func (spec FileSpec) TargetID() string  { return spec.ID }
func (spec FileSpec) SafeLabel() string {
	if spec.Label != "" {
		return spec.Label
	}
	return spec.Path
}

func (spec FileSpec) LocatorFingerprint() string {
	return SHA256String(BackendFile + "\x00" + spec.Path)
}
func (spec FileSpec) Sensitive() bool { return spec.Secret }

func (spec FileSpec) RecoveryMode(mode os.FileMode, available bool) (os.FileMode, bool) {
	if spec.EnforcedRecoveryMode != 0 {
		return spec.EnforcedRecoveryMode, true
	}
	return mode, available
}

// KeyringSpec describes one system credential-store entry.
type KeyringSpec struct {
	ID      string
	Service string
	Account string
	Label   string
}

func (spec KeyringSpec) BackendID() string { return BackendKeyring }
func (spec KeyringSpec) TargetID() string  { return spec.ID }
func (spec KeyringSpec) SafeLabel() string { return spec.Label }
func (spec KeyringSpec) LocatorFingerprint() string {
	return SHA256String(BackendKeyring + "\x00" + spec.Service + "\x00" + spec.Account)
}
func (spec KeyringSpec) Sensitive() bool { return true }

// Snapshot is backend-owned state captured during plan construction.
// OpaqueState is only for internal recovery; callers must not expose it in DTOs.
type Snapshot struct {
	Exists      bool
	IsSymlink   bool
	Fingerprint string
	Mode        os.FileMode
	Preview     profiletarget.Preview
	Content     string
	OpaqueState string
}

// Backend owns inspection and safe mutation of exactly one storage medium.
type Backend interface {
	ID() string
	Inspect(context.Context, Spec) (Snapshot, error)
	Verify(context.Context, Spec, Snapshot) error
	Backup(context.Context, Spec, Snapshot, string) (string, error)
	Apply(context.Context, Spec, Snapshot, string, os.FileMode, bool) error
	Restore(context.Context, Spec, Snapshot, string, string, os.FileMode, bool) error
	Remove(context.Context, Spec, Snapshot, bool) (bool, error)
}

// Registry is immutable after construction so unrelated switches cannot alter backend resolution.
type Registry struct {
	backends map[string]Backend
}

func NewRegistry(backends ...Backend) (Registry, error) {
	registry := Registry{backends: make(map[string]Backend, len(backends))}
	for _, backend := range backends {
		if backend == nil {
			return Registry{}, fmt.Errorf("target backend is invalid")
		}
		backendID := backend.ID()
		if backendID == "" || backendID != strings.TrimSpace(backendID) {
			return Registry{}, fmt.Errorf("target backend id %q is invalid", backendID)
		}
		if _, exists := registry.backends[backendID]; exists {
			return Registry{}, fmt.Errorf("target backend %q is duplicated", backendID)
		}
		registry.backends[backendID] = backend
	}
	return registry, nil
}

func MustRegistry(backends ...Backend) Registry {
	registry, err := NewRegistry(backends...)
	if err != nil {
		panic(err)
	}
	return registry
}

func DefaultRegistry() Registry {
	return MustRegistry(FileBackend{}, NewKeyringBackend(SystemKeyringClient{}))
}

func (registry Registry) Backend(id string) (Backend, bool) {
	backend, ok := registry.backends[id]
	return backend, ok
}

// InspectAll checks target uniqueness before it touches external state.
func (registry Registry) InspectAll(ctx context.Context, specs []Spec) (map[string]Snapshot, error) {
	snapshots := make(map[string]Snapshot, len(specs))
	seen := make(map[string]struct{}, len(specs))
	seenLocators := make(map[string]string, len(specs))
	for _, spec := range specs {
		if spec == nil {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan target spec is missing")
		}
		targetID := spec.TargetID()
		if targetID == "" || targetID != strings.TrimSpace(targetID) {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan target id is missing")
		}
		if _, exists := seen[targetID]; exists {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan contains duplicate target IDs").WithDetail("target_id", targetID)
		}
		seen[targetID] = struct{}{}
		backendID := spec.BackendID()
		if backendID == "" || backendID != strings.TrimSpace(backendID) {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan target backend id is invalid").WithDetail("target_id", targetID)
		}
		locatorFingerprint := spec.LocatorFingerprint()
		if locatorFingerprint == "" {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan target locator fingerprint is missing").WithDetail("target_id", targetID)
		}
		locatorKey := backendID + "\x00" + locatorFingerprint
		if firstTargetID, exists := seenLocators[locatorKey]; exists {
			return nil, apperror.New(apperror.PlanBuildFailed, "switch plan contains duplicate target locators").
				WithDetail("target_id", targetID).WithDetail("first_target_id", firstTargetID)
		}
		seenLocators[locatorKey] = targetID
		backend, ok := registry.Backend(backendID)
		if !ok {
			return nil, apperror.New(apperror.AdapterNotFound, "target backend not found").WithDetail("backend_id", backendID)
		}
		snapshot, err := backend.Inspect(ctx, spec)
		if err != nil {
			return nil, err
		}
		snapshots[targetID] = snapshot
	}
	return snapshots, nil
}

func ResolveFileSpec(targetID, backendID, path, label string) (Spec, error) {
	if backendID == "" || backendID == BackendFile {
		return FileSpec{ID: targetID, Path: path, NeedsContent: true, Label: label}, nil
	}
	return nil, apperror.New(apperror.RecoveryUnsupported, "target backend cannot be resolved").
		WithDetail("backend_id", backendID).WithDetail("target_id", targetID)
}

func SHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func SHA256String(value string) string {
	return SHA256([]byte(value))
}
