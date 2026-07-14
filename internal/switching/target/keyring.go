package target

import (
	"context"
	"errors"
	"io"
	"os"

	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/targetfs"
)

// KeyringClient is the narrow credential-store contract used by the backend.
type KeyringClient interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// SystemKeyringClient uses the host credential-store driver.
type SystemKeyringClient struct{}

func (SystemKeyringClient) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (SystemKeyringClient) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (SystemKeyringClient) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type keyringBackend struct{ client KeyringClient }

func NewKeyringBackend(client KeyringClient) Backend { return keyringBackend{client: client} }
func (keyringBackend) ID() string                    { return BackendKeyring }

func (backend keyringBackend) Inspect(_ context.Context, raw Spec) (Snapshot, error) {
	spec, ok := raw.(KeyringSpec)
	if !ok {
		return Snapshot{}, errors.New("keyring backend received incompatible target spec")
	}
	secret, err := backend.client.Get(spec.Service, spec.Account)
	if errors.Is(err, keyring.ErrNotFound) {
		return Snapshot{}, nil
	}
	if err != nil {
		return Snapshot{}, apperror.New(apperror.TargetReadFailed, "failed to read credential store").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	if len(secret) > targetfs.MaxFileBytes {
		return Snapshot{}, apperror.New(apperror.TargetReadFailed, "credential store value is too large").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	return Snapshot{Exists: true, Fingerprint: SHA256String(secret), Content: secret}, nil
}

func (backend keyringBackend) Verify(ctx context.Context, spec Spec, expected Snapshot) error {
	current, err := backend.Inspect(ctx, spec)
	if err != nil {
		return err
	}
	if current.Exists != expected.Exists || current.Fingerprint != expected.Fingerprint {
		return apperror.New(apperror.TargetChanged, "credential store value changed").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.TargetID())
	}
	return nil
}

func (backend keyringBackend) Backup(ctx context.Context, spec Spec, snapshot Snapshot, destination string) (string, error) {
	if !snapshot.Exists {
		return "", nil
	}
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return "", err
	}
	if err := writeKeyringBackup(destination, snapshot.Content, func() (*os.File, error) {
		return os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}); err != nil {
		return "", err
	}
	return SHA256String(snapshot.Content), nil
}

func writeKeyringBackup(destination, content string, open func() (*os.File, error)) error {
	file, err := open()
	if err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to create credential backup", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.WriteString(file, content); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to write credential backup", err)
	}
	if err := file.Sync(); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to sync credential backup", err)
	}
	if err := file.Close(); err != nil {
		return apperror.Wrap(apperror.BackupFailed, "failed to close credential backup", err)
	}
	complete = true
	return nil
}

func (backend keyringBackend) Apply(ctx context.Context, raw Spec, snapshot Snapshot, desired string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(KeyringSpec)
	if !ok {
		return errors.New("keyring backend received incompatible target spec")
	}
	// The system credential APIs expose no compare-and-swap primitive. Re-read
	// immediately before Set to narrow, but not claim to eliminate, the external race.
	if err := backend.Verify(ctx, spec, snapshot); err != nil {
		return err
	}
	if err := backend.client.Set(spec.Service, spec.Account, desired); err != nil {
		return apperror.New(apperror.TargetWriteFailed, "failed to update credential store").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend keyringBackend) Restore(ctx context.Context, raw Spec, current Snapshot, sourcePath, sourceSHA string, _ os.FileMode, _ bool) error {
	spec, ok := raw.(KeyringSpec)
	if !ok {
		return errors.New("keyring backend received incompatible target spec")
	}
	rawBackup, err := os.ReadFile(sourcePath)
	if err != nil {
		return apperror.Wrap(apperror.BackupInvalid, "failed to read credential backup", err)
	}
	if len(rawBackup) > targetfs.MaxFileBytes || SHA256(rawBackup) != sourceSHA {
		return apperror.New(apperror.BackupInvalid, "credential backup hash is invalid").WithDetail("target_id", spec.ID)
	}
	// Validate the backup first so the credential-store re-read remains the
	// final operation before Set, narrowing the unavoidable external race.
	if err := backend.Verify(ctx, spec, current); err != nil {
		return err
	}
	if err := backend.client.Set(spec.Service, spec.Account, string(rawBackup)); err != nil {
		return apperror.New(apperror.TargetWriteFailed, "failed to restore credential store").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	return nil
}

func (backend keyringBackend) Remove(ctx context.Context, raw Spec, current Snapshot, allowMissing bool) (bool, error) {
	spec, ok := raw.(KeyringSpec)
	if !ok {
		return false, errors.New("keyring backend received incompatible target spec")
	}
	actual, err := backend.Inspect(ctx, spec)
	if err != nil {
		return false, err
	}
	if !actual.Exists && allowMissing {
		return false, nil
	}
	if actual.Exists != current.Exists || actual.Fingerprint != current.Fingerprint {
		return false, apperror.New(apperror.TargetChanged, "credential store value changed").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	if err := backend.client.Delete(spec.Service, spec.Account); err != nil {
		if allowMissing && errors.Is(err, keyring.ErrNotFound) {
			return false, nil
		}
		return false, apperror.New(apperror.TargetWriteFailed, "failed to remove credential store value").
			WithDetail("backend_id", BackendKeyring).WithDetail("target_id", spec.ID)
	}
	return true, nil
}
