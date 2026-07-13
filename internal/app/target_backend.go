package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	targetBackendFile               = "file"
	targetBackendKeyring            = "keyring"
	targetBackendClaudeCodeKeychain = "claude-code-keychain"
)

type targetSpec interface {
	BackendID() string
	TargetID() string
	SafeLabel() string
	LocatorFingerprint() string
	Sensitive() bool
}

type fileTargetSpec struct {
	ID           string
	Path         string
	NeedsContent bool
	Secret       bool
	Label        string
}

func (spec fileTargetSpec) BackendID() string { return targetBackendFile }
func (spec fileTargetSpec) TargetID() string  { return spec.ID }
func (spec fileTargetSpec) SafeLabel() string {
	if spec.Label != "" {
		return spec.Label
	}
	return spec.Path
}

func (spec fileTargetSpec) LocatorFingerprint() string {
	return sha256HexString(targetBackendFile + "\x00" + spec.Path)
}
func (spec fileTargetSpec) Sensitive() bool { return spec.Secret }

type keyringTargetSpec struct {
	ID      string
	Service string
	Account string
	Label   string
}

func (spec keyringTargetSpec) BackendID() string { return targetBackendKeyring }
func (spec keyringTargetSpec) TargetID() string  { return spec.ID }
func (spec keyringTargetSpec) SafeLabel() string { return spec.Label }
func (spec keyringTargetSpec) LocatorFingerprint() string {
	return sha256HexString(targetBackendKeyring + "\x00" + spec.Service + "\x00" + spec.Account)
}
func (spec keyringTargetSpec) Sensitive() bool { return true }

type targetSnapshot struct {
	Exists      bool
	IsSymlink   bool
	Fingerprint string
	Mode        os.FileMode
	Preview     TextPreview
	Content     string
	// privateLocator is backend-owned recovery state. It may be persisted only
	// in private backup data and must never cross a public output boundary.
	privateLocator string
}

type targetBackend interface {
	ID() string
	Inspect(context.Context, targetSpec) (targetSnapshot, error)
	Verify(context.Context, targetSpec, targetSnapshot) error
	Backup(context.Context, targetSpec, targetSnapshot, string) (string, error)
	Apply(context.Context, targetSpec, targetSnapshot, string, os.FileMode, bool) error
	Restore(context.Context, targetSpec, targetSnapshot, string, string, os.FileMode, bool) error
	Remove(context.Context, targetSpec, targetSnapshot, bool) (bool, error)
}

type fileTargetBackend struct{}

func (fileTargetBackend) ID() string { return targetBackendFile }

func (fileTargetBackend) Inspect(ctx context.Context, raw targetSpec) (targetSnapshot, error) {
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return targetSnapshot{}, fmt.Errorf("file backend received incompatible target spec")
	}
	read, err := readTargetForPlan(ctx, spec.Path, spec.NeedsContent)
	if err != nil {
		return targetSnapshot{}, err
	}
	return targetSnapshot{
		Exists: read.FileExists, IsSymlink: read.IsSymlink, Fingerprint: read.SHA256,
		Mode: read.Mode, Preview: read.Preview, Content: read.Content,
	}, nil
}

func (fileTargetBackend) Verify(ctx context.Context, raw targetSpec, snapshot targetSnapshot) error {
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return fmt.Errorf("file backend received incompatible target spec")
	}
	err := targetfs.VerifyExpected(ctx, targetfs.ExpectedTarget{
		TargetID: spec.ID, Path: spec.Path, Exists: snapshot.Exists, SHA256: snapshot.Fingerprint,
	})
	return mapTargetFSError(err)
}

func (fileTargetBackend) Backup(ctx context.Context, raw targetSpec, snapshot targetSnapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return "", fmt.Errorf("file backend received incompatible target spec")
	}
	hash, err := targetfs.CopyBackupFile(ctx, spec.Path, destination)
	if err != nil {
		return "", mapTargetFSError(err)
	}
	return hash, nil
}

func (fileTargetBackend) Apply(ctx context.Context, raw targetSpec, snapshot targetSnapshot, desired string, mode os.FileMode, useMode bool) error {
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return fmt.Errorf("file backend received incompatible target spec")
	}
	err := targetfs.AtomicWriteContent(ctx, targetfs.AtomicWriteContentRequest{
		Expected: targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: snapshot.Exists, SHA256: snapshot.Fingerprint},
		Content:  desired, Mode: mode, UseMode: useMode,
	})
	return mapTargetFSError(err)
}

func (fileTargetBackend) Restore(ctx context.Context, raw targetSpec, current targetSnapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return fmt.Errorf("file backend received incompatible target spec")
	}
	err := targetfs.AtomicWriteFile(ctx, targetfs.AtomicWriteFileRequest{
		Expected:   targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: current.Exists, SHA256: current.Fingerprint},
		SourcePath: sourcePath, SourceSHA256: sourceSHA, Mode: mode, UseMode: useMode,
	})
	return mapTargetFSError(err)
}

func (fileTargetBackend) Remove(ctx context.Context, raw targetSpec, current targetSnapshot, allowMissing bool) (bool, error) {
	spec, ok := raw.(fileTargetSpec)
	if !ok {
		return false, fmt.Errorf("file backend received incompatible target spec")
	}
	removed, err := targetfs.GuardedRemove(ctx, targetfs.GuardedRemoveRequest{
		Expected:     targetfs.ExpectedTarget{TargetID: spec.ID, Path: spec.Path, Exists: current.Exists, SHA256: current.Fingerprint},
		AllowMissing: allowMissing,
	})
	if err != nil {
		return false, mapTargetFSError(err)
	}
	return removed, nil
}

type keyringClient interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type systemKeyringClient struct{}

func (systemKeyringClient) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyringClient) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (systemKeyringClient) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type keyringTargetBackend struct {
	client keyringClient
}

func (keyringTargetBackend) ID() string { return targetBackendKeyring }

func (backend keyringTargetBackend) Inspect(_ context.Context, raw targetSpec) (targetSnapshot, error) {
	spec, ok := raw.(keyringTargetSpec)
	if !ok {
		return targetSnapshot{}, fmt.Errorf("keyring backend received incompatible target spec")
	}
	secret, err := backend.client.Get(spec.Service, spec.Account)
	if errors.Is(err, keyring.ErrNotFound) {
		return targetSnapshot{}, nil
	}
	if err != nil {
		// Credential-store implementations are external error boundaries; do not
		// propagate driver text that may echo attributes or supplied values.
		return targetSnapshot{}, NewError(ErrorTargetReadFailed, "failed to read credential store").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	if len(secret) > maxTargetContentBytes {
		return targetSnapshot{}, NewError(ErrorTargetReadFailed, "credential store value is too large").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	return targetSnapshot{Exists: true, Fingerprint: sha256HexString(secret), Content: secret}, nil
}

func (backend keyringTargetBackend) Verify(ctx context.Context, spec targetSpec, expected targetSnapshot) error {
	current, err := backend.Inspect(ctx, spec)
	if err != nil {
		return err
	}
	if current.Exists != expected.Exists || current.Fingerprint != expected.Fingerprint {
		return NewError(ErrorTargetChanged, "credential store value changed").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.TargetID())
	}
	return nil
}

func (backend keyringTargetBackend) Backup(ctx context.Context, spec targetSpec, snapshot targetSnapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return "", err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to create credential backup", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := io.WriteString(file, snapshot.Content); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to write credential backup", err)
	}
	if err := file.Sync(); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to sync credential backup", err)
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		return "", WrapError(ErrorBackupFailed, "failed to close credential backup", closeErr)
	}
	return sha256HexString(snapshot.Content), nil
}

func (backend keyringTargetBackend) Apply(ctx context.Context, raw targetSpec, snapshot targetSnapshot, desired string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(keyringTargetSpec)
	if !ok {
		return fmt.Errorf("keyring backend received incompatible target spec")
	}
	// The system credential APIs expose no compare-and-swap primitive. Re-read
	// immediately before Set to narrow, but not claim to eliminate, the external race.
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return err
	}
	if err := backend.client.Set(spec.Service, spec.Account, desired); err != nil {
		return NewError(ErrorTargetWriteFailed, "failed to update credential store").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend keyringTargetBackend) Restore(ctx context.Context, raw targetSpec, current targetSnapshot, sourcePath, sourceSHA string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(keyringTargetSpec)
	if !ok {
		return fmt.Errorf("keyring backend received incompatible target spec")
	}
	rawBackup, err := os.ReadFile(sourcePath)
	if err != nil {
		return WrapError(ErrorBackupInvalid, "failed to read credential backup", err)
	}
	if len(rawBackup) > maxTargetContentBytes || sha256Hex(rawBackup) != sourceSHA {
		return NewError(ErrorBackupInvalid, "credential backup hash is invalid").WithDetail("target_id", spec.ID)
	}
	// Validate the backup first so the credential-store re-read remains the
	// final operation before Set, narrowing the unavoidable external race.
	if err := backend.Verify(ctx, spec, current); err != nil {
		return err
	}
	if err := backend.client.Set(spec.Service, spec.Account, string(rawBackup)); err != nil {
		return NewError(ErrorTargetWriteFailed, "failed to restore credential store").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend keyringTargetBackend) Remove(ctx context.Context, raw targetSpec, current targetSnapshot, allowMissing bool) (bool, error) {
	spec, ok := raw.(keyringTargetSpec)
	if !ok {
		return false, fmt.Errorf("keyring backend received incompatible target spec")
	}
	actual, err := backend.Inspect(ctx, spec)
	if err != nil {
		return false, err
	}
	if !actual.Exists && allowMissing {
		return false, nil
	}
	if actual.Exists != current.Exists || actual.Fingerprint != current.Fingerprint {
		return false, NewError(ErrorTargetChanged, "credential store value changed").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	if err := backend.client.Delete(spec.Service, spec.Account); err != nil {
		if allowMissing && errors.Is(err, keyring.ErrNotFound) {
			return false, nil
		}
		return false, NewError(ErrorTargetWriteFailed, "failed to remove credential store value").
			WithDetail("backend_id", targetBackendKeyring).WithDetail("target_id", spec.ID)
	}
	return true, nil
}

func resolveFileTargetSpec(targetID, backendID, path, label string) (targetSpec, error) {
	if backendID == "" || backendID == targetBackendFile {
		return fileTargetSpec{ID: targetID, Path: path, NeedsContent: true, Label: label}, nil
	}
	return nil, NewError(ErrorRollbackUnsupported, "target backend cannot be resolved").
		WithDetail("backend_id", backendID).WithDetail("target_id", targetID)
}

var targetBackends = map[string]targetBackend{
	targetBackendFile:               fileTargetBackend{},
	targetBackendKeyring:            keyringTargetBackend{client: systemKeyringClient{}},
	targetBackendClaudeCodeKeychain: claudeCodeKeychainTargetBackend{driver: newClaudeCodeKeychainDriver()},
}

func inspectPreparedTargets(ctx context.Context, targets []preparedTarget) (map[string]targetSnapshot, error) {
	snapshots := make(map[string]targetSnapshot, len(targets))
	seen := make(map[string]struct{}, len(targets))
	seenLocators := make(map[string]string, len(targets))
	for _, target := range targets {
		if target.Spec == nil {
			return nil, NewError(ErrorPlanBuildFailed, "switch plan target spec is missing")
		}
		targetID := target.Spec.TargetID()
		if targetID == "" {
			return nil, NewError(ErrorPlanBuildFailed, "switch plan target id is missing")
		}
		if _, exists := seen[targetID]; exists {
			// Target IDs key snapshots and backup entries, so accepting a duplicate
			// would make verification and recovery address the wrong target state.
			return nil, NewError(ErrorPlanBuildFailed, "switch plan contains duplicate target IDs").WithDetail("target_id", targetID)
		}
		seen[targetID] = struct{}{}
		locatorKey := target.Spec.BackendID() + "\x00" + target.Spec.LocatorFingerprint()
		if firstTargetID, exists := seenLocators[locatorKey]; exists {
			// One plan may address a physical target only once. Otherwise backup,
			// partial-write recovery, and per-target verification become ambiguous.
			return nil, NewError(ErrorPlanBuildFailed, "switch plan contains duplicate target locators").
				WithDetail("target_id", targetID).WithDetail("first_target_id", firstTargetID)
		}
		seenLocators[locatorKey] = targetID
		backend, ok := targetBackends[target.Spec.BackendID()]
		if !ok {
			return nil, NewError(ErrorAdapterNotFound, "target backend not found").WithDetail("backend_id", target.Spec.BackendID())
		}
		snapshot, err := backend.Inspect(ctx, target.Spec)
		if err != nil {
			return nil, err
		}
		snapshots[targetID] = snapshot
	}
	return snapshots, nil
}
