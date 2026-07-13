package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	claudekeychain "github.com/strahe/profiledeck/internal/claudecode/keychain"
)

type claudeCodeKeychainTargetSpec struct {
	ID      string
	Service string
	Account string
	Label   string
}

func (spec claudeCodeKeychainTargetSpec) BackendID() string { return targetBackendClaudeCodeKeychain }
func (spec claudeCodeKeychainTargetSpec) TargetID() string  { return spec.ID }
func (spec claudeCodeKeychainTargetSpec) SafeLabel() string { return spec.Label }
func (spec claudeCodeKeychainTargetSpec) LocatorFingerprint() string {
	return sha256HexString(targetBackendClaudeCodeKeychain + "\x00" + spec.Service + "\x00" + spec.Account)
}
func (spec claudeCodeKeychainTargetSpec) Sensitive() bool { return true }

type claudeCodeKeychainDriver interface {
	Find(service, account string, allowInteraction bool) ([]claudekeychain.Reference, error)
	Read(persistent []byte, allowInteraction bool) (claudekeychain.Item, error)
	Update(persistent, data []byte) error
}

func newClaudeCodeKeychainDriver() claudeCodeKeychainDriver { return claudekeychain.New() }

type claudeCodeKeychainTargetBackend struct {
	driver claudeCodeKeychainDriver
}

func (claudeCodeKeychainTargetBackend) ID() string { return targetBackendClaudeCodeKeychain }

func (backend claudeCodeKeychainTargetBackend) Inspect(ctx context.Context, raw targetSpec) (targetSnapshot, error) {
	return backend.InspectWithInteraction(ctx, raw, true)
}

func (backend claudeCodeKeychainTargetBackend) InspectWithInteraction(_ context.Context, raw targetSpec, allowInteraction bool) (targetSnapshot, error) {
	spec, ok := raw.(claudeCodeKeychainTargetSpec)
	if !ok {
		return targetSnapshot{}, fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	references, err := backend.driver.Find(spec.Service, spec.Account, allowInteraction)
	if err != nil {
		return targetSnapshot{}, claudeCodeKeychainReadError(spec.ID, err)
	}
	switch len(references) {
	case 0:
		return targetSnapshot{}, nil
	case 1:
	default:
		return targetSnapshot{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Keychain login is ambiguous; exactly one matching item is required")
	}
	reference := references[0]
	if reference.Service != spec.Service || reference.Account != spec.Account || len(reference.Persistent) == 0 {
		return targetSnapshot{}, NewError(ErrorTargetChanged, "Claude Code Keychain item attributes changed").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(reference.Persistent, allowInteraction)
	if err != nil {
		return targetSnapshot{}, claudeCodeKeychainReadError(spec.ID, err)
	}
	if item.Service != spec.Service || item.Account != spec.Account {
		return targetSnapshot{}, NewError(ErrorTargetChanged, "Claude Code Keychain item attributes changed").WithDetail("target_id", spec.ID)
	}
	if len(item.Data) > maxTargetContentBytes {
		return targetSnapshot{}, NewError(ErrorTargetReadFailed, "Claude Code Keychain value is too large").WithDetail("target_id", spec.ID)
	}
	return targetSnapshot{
		Exists: true, Fingerprint: sha256Hex(item.Data), Content: string(item.Data),
		privateLocator: base64.RawStdEncoding.EncodeToString(reference.Persistent),
	}, nil
}

func (backend claudeCodeKeychainTargetBackend) Verify(_ context.Context, raw targetSpec, expected targetSnapshot) error {
	spec, ok := raw.(claudeCodeKeychainTargetSpec)
	if !ok {
		return fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	if !expected.Exists {
		references, err := backend.driver.Find(spec.Service, spec.Account, true)
		if err != nil {
			return claudeCodeKeychainReadError(spec.ID, err)
		}
		if len(references) != 0 {
			return NewError(ErrorTargetChanged, "Claude Code Keychain item appeared").WithDetail("target_id", spec.ID)
		}
		return nil
	}
	persistent, err := decodeClaudeCodePersistentRef(expected.privateLocator)
	if err != nil {
		return NewError(ErrorTargetChanged, "Claude Code Keychain item reference is unavailable").WithDetail("target_id", spec.ID)
	}
	references, err := backend.driver.Find(spec.Service, spec.Account, true)
	if err != nil {
		return claudeCodeKeychainReadError(spec.ID, err)
	}
	if len(references) != 1 || !bytes.Equal(references[0].Persistent, persistent) {
		return NewError(ErrorTargetChanged, "Claude Code Keychain item was replaced").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(persistent, true)
	if err != nil {
		if errors.Is(err, claudekeychain.ErrNotFound) {
			return NewError(ErrorTargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", spec.ID)
		}
		return claudeCodeKeychainReadError(spec.ID, err)
	}
	if item.Service != spec.Service || item.Account != spec.Account || sha256Hex(item.Data) != expected.Fingerprint {
		return NewError(ErrorTargetChanged, "Claude Code Keychain item changed").WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend claudeCodeKeychainTargetBackend) Backup(ctx context.Context, raw targetSpec, snapshot targetSnapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	if err := backend.Verify(ctx, raw, snapshot); err != nil {
		return "", err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to back up Claude Code Keychain login", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.WriteString(file, snapshot.Content); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to write Claude Code Keychain backup", err)
	}
	if err := file.Sync(); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to sync Claude Code Keychain backup", err)
	}
	if err := file.Close(); err != nil {
		return "", WrapError(ErrorBackupFailed, "failed to close Claude Code Keychain backup", err)
	}
	complete = true
	return sha256HexString(snapshot.Content), nil
}

func (backend claudeCodeKeychainTargetBackend) Apply(ctx context.Context, raw targetSpec, snapshot targetSnapshot, desired string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(claudeCodeKeychainTargetSpec)
	if !ok {
		return fmt.Errorf("incompatible target spec for Claude Code Keychain backend: %T", raw)
	}
	if !snapshot.Exists {
		return NewError(ErrorClaudeCodeInvalid, "Claude Code Keychain item is missing; run Claude Code /login first")
	}
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return err
	}
	persistent, err := decodeClaudeCodePersistentRef(snapshot.privateLocator)
	if err != nil {
		return NewError(ErrorTargetChanged, "Claude Code Keychain item reference is unavailable").WithDetail("target_id", spec.ID)
	}
	// Security.framework has no content-level CAS. This exact-reference re-read
	// narrows, but cannot eliminate, the final race before SecItemUpdate.
	if err := backend.driver.Update(persistent, []byte(desired)); err != nil {
		if errors.Is(err, claudekeychain.ErrNotFound) {
			return NewError(ErrorTargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", spec.ID)
		}
		return NewError(ErrorTargetWriteFailed, "failed to update Claude Code Keychain login").WithDetail("target_id", spec.ID)
	}
	item, err := backend.driver.Read(persistent, true)
	if err != nil || item.Service != spec.Service || item.Account != spec.Account || sha256Hex(item.Data) != sha256HexString(desired) {
		return NewError(ErrorTargetWriteFailed, "Claude Code Keychain update could not be verified").WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend claudeCodeKeychainTargetBackend) Restore(ctx context.Context, raw targetSpec, current targetSnapshot, sourcePath, sourceSHA string, _ os.FileMode, _ bool) error {
	content, err := os.ReadFile(sourcePath)
	if err != nil || len(content) > maxTargetContentBytes || sha256Hex(content) != sourceSHA {
		return NewError(ErrorBackupInvalid, "Claude Code Keychain backup is invalid")
	}
	return backend.Apply(ctx, raw, current, string(content), 0, false)
}

func (claudeCodeKeychainTargetBackend) Remove(context.Context, targetSpec, targetSnapshot, bool) (bool, error) {
	return false, NewError(ErrorRollbackUnsupported, "Claude Code Keychain items cannot be created or deleted by ProfileDeck")
}

func decodeClaudeCodePersistentRef(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("persistent reference is empty")
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func claudeCodeKeychainReadError(targetID string, err error) error {
	if errors.Is(err, claudekeychain.ErrInteractionRequired) {
		return NewError(ErrorTargetReadFailed, "Claude Code Keychain authorization is required").
			WithDetail("reason", claudeCodeKeychainAuthorizationReason).
			WithDetail("target_id", targetID)
	}
	if errors.Is(err, claudekeychain.ErrUnavailable) {
		return NewError(ErrorTargetReadFailed, "Claude Code Keychain access is unavailable").WithDetail("target_id", targetID)
	}
	if errors.Is(err, claudekeychain.ErrNotFound) {
		return NewError(ErrorTargetChanged, "Claude Code Keychain item was removed").WithDetail("target_id", targetID)
	}
	return NewError(ErrorTargetReadFailed, "failed to read Claude Code Keychain login").WithDetail("target_id", targetID)
}

const claudeCodeKeychainAuthorizationReason = "keychain_authorization_required"

func isClaudeCodeKeychainAuthorizationRequired(err error) bool {
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorTargetReadFailed {
		return false
	}
	reason, _ := appErr.Details["reason"].(string)
	return reason == claudeCodeKeychainAuthorizationReason
}

func inspectClaudeCodeTarget(ctx context.Context, spec targetSpec, allowKeychainInteraction bool) (targetSnapshot, error) {
	backend, ok := targetBackends[spec.BackendID()]
	if !ok {
		return targetSnapshot{}, NewError(ErrorTargetReadFailed, "Claude Code credential backend is unavailable")
	}
	if keychainBackend, ok := backend.(interface {
		InspectWithInteraction(context.Context, targetSpec, bool) (targetSnapshot, error)
	}); ok {
		return keychainBackend.InspectWithInteraction(ctx, spec, allowKeychainInteraction)
	}
	return backend.Inspect(ctx, spec)
}
