// Package target owns Claude Code's native Keychain switch target.
package target

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	claudekeychain "github.com/strahe/profiledeck/internal/claudecode/keychain"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const AuthorizationReason = "keychain_authorization_required"

// KeychainSpec identifies one existing Claude Code login item.
type KeychainSpec struct {
	ID      string
	Service string
	Account string
	Label   string
}

func (spec KeychainSpec) BackendID() string { return switchtarget.BackendClaudeCodeKeychain }
func (spec KeychainSpec) TargetID() string  { return spec.ID }
func (spec KeychainSpec) SafeLabel() string { return spec.Label }
func (spec KeychainSpec) LocatorFingerprint() string {
	return switchtarget.SHA256String(switchtarget.BackendClaudeCodeKeychain + "\x00" + spec.Service + "\x00" + spec.Account)
}
func (spec KeychainSpec) Sensitive() bool { return true }
func (spec KeychainSpec) RecoveryLocator() string {
	return spec.Account
}

func (spec KeychainSpec) ObjectFingerprint(snapshot switchtarget.Snapshot) string {
	if snapshot.OpaqueState == "" {
		return ""
	}
	return switchtarget.SHA256String(snapshot.OpaqueState)
}

// Driver is the narrow Security.framework contract used by the backend.
type Driver interface {
	Find(service, account string, allowInteraction bool) ([]claudekeychain.Reference, error)
	Read(persistent []byte, allowInteraction bool) (claudekeychain.Item, error)
	Update(persistent, data []byte) error
}

func NewDriver() Driver { return claudekeychain.New() }

// Backend safely updates a pre-existing native Keychain entry.
type Backend struct{ driver Driver }

func NewBackend(driver Driver) Backend { return Backend{driver: driver} }
func NewSystemBackend() Backend        { return NewBackend(NewDriver()) }
func (Backend) ID() string             { return switchtarget.BackendClaudeCodeKeychain }

func (backend Backend) Inspect(ctx context.Context, raw switchtarget.Spec) (switchtarget.Snapshot, error) {
	return backend.InspectWithInteraction(ctx, raw, true)
}

// InspectWithInteraction lets doctor read login state without prompting.
func (backend Backend) InspectWithInteraction(_ context.Context, raw switchtarget.Spec, allowInteraction bool) (switchtarget.Snapshot, error) {
	spec, ok := raw.(KeychainSpec)
	if !ok {
		return switchtarget.Snapshot{}, fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	references, err := backend.driver.Find(spec.Service, spec.Account, allowInteraction)
	if err != nil {
		return switchtarget.Snapshot{}, readError(spec.ID, err)
	}
	switch len(references) {
	case 0:
		return switchtarget.Snapshot{}, nil
	case 1:
	default:
		return switchtarget.Snapshot{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Keychain login is ambiguous; exactly one matching item is required")
	}
	reference := references[0]
	if reference.Service != spec.Service || reference.Account != spec.Account || len(reference.Persistent) == 0 {
		return switchtarget.Snapshot{}, apperror.New(apperror.TargetChanged, "Claude Code Keychain item attributes changed").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(reference.Persistent, allowInteraction)
	if err != nil {
		return switchtarget.Snapshot{}, readError(spec.ID, err)
	}
	if item.Service != spec.Service || item.Account != spec.Account {
		return switchtarget.Snapshot{}, apperror.New(apperror.TargetChanged, "Claude Code Keychain item attributes changed").WithDetail("target_id", spec.ID)
	}
	if len(item.Data) > targetfs.MaxFileBytes {
		return switchtarget.Snapshot{}, apperror.New(apperror.TargetReadFailed, "Claude Code Keychain value is too large").WithDetail("target_id", spec.ID)
	}
	return switchtarget.Snapshot{
		Exists: true, Fingerprint: switchtarget.SHA256(item.Data), Content: string(item.Data),
		OpaqueState: base64.RawStdEncoding.EncodeToString(reference.Persistent),
	}, nil
}

func (backend Backend) Verify(_ context.Context, raw switchtarget.Spec, expected switchtarget.Snapshot) error {
	spec, ok := raw.(KeychainSpec)
	if !ok {
		return fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	if !expected.Exists {
		references, err := backend.driver.Find(spec.Service, spec.Account, true)
		if err != nil {
			return readError(spec.ID, err)
		}
		if len(references) != 0 {
			return apperror.New(apperror.TargetChanged, "Claude Code Keychain item appeared").WithDetail("target_id", spec.ID)
		}
		return nil
	}
	persistent, err := DecodePersistentRef(expected.OpaqueState)
	if err != nil {
		return apperror.New(apperror.TargetChanged, "Claude Code Keychain item reference is unavailable").WithDetail("target_id", spec.ID)
	}
	references, err := backend.driver.Find(spec.Service, spec.Account, true)
	if err != nil {
		return readError(spec.ID, err)
	}
	if len(references) != 1 || !bytes.Equal(references[0].Persistent, persistent) {
		return apperror.New(apperror.TargetChanged, "Claude Code Keychain item was replaced").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(persistent, true)
	if err != nil {
		if errors.Is(err, claudekeychain.ErrNotFound) {
			return apperror.New(apperror.TargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", spec.ID)
		}
		return readError(spec.ID, err)
	}
	if item.Service != spec.Service || item.Account != spec.Account || switchtarget.SHA256(item.Data) != expected.Fingerprint {
		return apperror.New(apperror.TargetChanged, "Claude Code Keychain item changed").WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend Backend) Backup(ctx context.Context, raw switchtarget.Spec, snapshot switchtarget.Snapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	if err := backend.Verify(ctx, raw, snapshot); err != nil {
		return "", err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", apperror.Wrap(apperror.BackupFailed, "failed to back up Claude Code Keychain login", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.WriteString(file, snapshot.Content); err != nil {
		return "", apperror.Wrap(apperror.BackupFailed, "failed to write Claude Code Keychain backup", err)
	}
	if err := file.Sync(); err != nil {
		return "", apperror.Wrap(apperror.BackupFailed, "failed to sync Claude Code Keychain backup", err)
	}
	if err := file.Close(); err != nil {
		return "", apperror.Wrap(apperror.BackupFailed, "failed to close Claude Code Keychain backup", err)
	}
	complete = true
	return switchtarget.SHA256String(snapshot.Content), nil
}

func (backend Backend) Apply(ctx context.Context, raw switchtarget.Spec, snapshot switchtarget.Snapshot, desired string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(KeychainSpec)
	if !ok {
		return fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	if !snapshot.Exists {
		return apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Keychain item is missing; run Claude Code /login first")
	}
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return err
	}
	persistent, err := DecodePersistentRef(snapshot.OpaqueState)
	if err != nil {
		return apperror.New(apperror.TargetChanged, "Claude Code Keychain item reference is unavailable").WithDetail("target_id", spec.ID)
	}
	// Security.framework has no content-level CAS. This exact-reference re-read
	// narrows, but cannot eliminate, the final race before SecItemUpdate.
	if err := backend.driver.Update(persistent, []byte(desired)); err != nil {
		if errors.Is(err, claudekeychain.ErrNotFound) {
			return apperror.New(apperror.TargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", spec.ID)
		}
		return apperror.New(apperror.TargetWriteFailed, "failed to update Claude Code Keychain login").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(persistent, true)
	if err != nil || item.Service != spec.Service || item.Account != spec.Account || switchtarget.SHA256(item.Data) != switchtarget.SHA256String(desired) {
		return apperror.New(apperror.TargetWriteFailed, "Claude Code Keychain update could not be verified").WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend Backend) Restore(ctx context.Context, raw switchtarget.Spec, current switchtarget.Snapshot, sourcePath, sourceSHA string, _ os.FileMode, _ bool) error {
	content, err := os.ReadFile(sourcePath)
	if err != nil || len(content) > targetfs.MaxFileBytes || switchtarget.SHA256(content) != sourceSHA {
		return apperror.New(apperror.BackupInvalid, "Claude Code Keychain backup is invalid")
	}
	return backend.Apply(ctx, raw, current, string(content), 0, false)
}

func (Backend) Remove(context.Context, switchtarget.Spec, switchtarget.Snapshot, bool) (bool, error) {
	return false, apperror.New(apperror.RollbackUnsupported, "Claude Code Keychain items cannot be created or deleted by ProfileDeck")
}

func DecodePersistentRef(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("persistent reference is empty")
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func IsAuthorizationRequired(err error) bool {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.TargetReadFailed {
		return false
	}
	reason, _ := appErr.Details["reason"].(string)
	return reason == AuthorizationReason
}

func readError(targetID string, err error) error {
	if errors.Is(err, claudekeychain.ErrInteractionRequired) {
		return apperror.New(apperror.TargetReadFailed, "Claude Code Keychain authorization is required").
			WithDetail("reason", AuthorizationReason).WithDetail("target_id", targetID)
	}
	if errors.Is(err, claudekeychain.ErrUnavailable) {
		return apperror.New(apperror.TargetReadFailed, "Claude Code Keychain access is unavailable").WithDetail("target_id", targetID)
	}
	if errors.Is(err, claudekeychain.ErrNotFound) {
		return apperror.New(apperror.TargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", targetID)
	}
	return apperror.New(apperror.TargetReadFailed, "failed to read Claude Code Keychain login").WithDetail("target_id", targetID)
}
