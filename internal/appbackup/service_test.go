package appbackup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type memoryKeyStore struct {
	mu     sync.Mutex
	value  string
	getErr error
	setErr error
}

func (keys *memoryKeyStore) Get(_, _ string) (string, error) {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if keys.getErr != nil {
		return "", keys.getErr
	}
	if keys.value == "" {
		return "", keyring.ErrNotFound
	}
	return keys.value, nil
}

func (keys *memoryKeyStore) Set(_, _, value string) error {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if keys.setErr != nil {
		return keys.setErr
	}
	keys.value = value
	return nil
}

type directExclusiveRunner struct{}

func (directExclusiveRunner) RunExclusive(ctx context.Context, _ string, run func(context.Context) error) error {
	return run(ctx)
}

func TestCreatePublishesEncryptedMinimalPrivateArchive(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	setTestSetting(t, paths, "backup.secret", `"do-not-leak"`)

	created, err := service.Create(ctx, CreateRequest{Kind: KindManual, Reason: ReasonManual})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	backupPath := filepath.Join(paths.Backups, created.ID+Extension)
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("backup mode = %o, want 600", got)
	}
	ciphertext, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("SQLite format 3")) || bytes.Contains(ciphertext, []byte("do-not-leak")) {
		t.Fatal("encrypted backup exposes database content")
	}

	identity, err := age.ParseX25519Identity(keys.value)
	if err != nil {
		t.Fatalf("parse generated identity: %v", err)
	}
	decrypted, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		t.Fatalf("decrypt backup: %v", err)
	}
	archive := tar.NewReader(decrypted)
	manifestHeader, err := archive.Next()
	if err != nil || manifestHeader.Name != manifestName {
		t.Fatalf("first archive entry = %#v, err = %v", manifestHeader, err)
	}
	manifestJSON, err := io.ReadAll(archive)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifestFields map[string]any
	if err := json.Unmarshal(manifestJSON, &manifestFields); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	wantFields := []string{"backup_id", "created_at_unix_ms", "format_version", "reason"}
	gotFields := make([]string, 0, len(manifestFields))
	for key := range manifestFields {
		gotFields = append(gotFields, key)
	}
	sort.Strings(gotFields)
	if fmt.Sprint(gotFields) != fmt.Sprint(wantFields) {
		t.Fatalf("manifest fields = %v, want %v", gotFields, wantFields)
	}
	if manifestFields["backup_id"] != created.ID || manifestFields["reason"] != ReasonManual {
		t.Fatalf("unexpected manifest: %s", manifestJSON)
	}

	databaseHeader, err := archive.Next()
	if err != nil || databaseHeader.Name != databaseName {
		t.Fatalf("second archive entry = %#v, err = %v", databaseHeader, err)
	}
	database, err := io.ReadAll(archive)
	if err != nil {
		t.Fatalf("read archived database: %v", err)
	}
	if !bytes.HasPrefix(database, []byte("SQLite format 3")) {
		t.Fatal("archive database entry is not SQLite")
	}
	if _, err := archive.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected extra archive entry: %v", err)
	}

	entries, err := os.ReadDir(paths.Backups)
	if err != nil {
		t.Fatalf("list backup directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != created.ID+Extension {
		t.Fatalf("backup directory contains staging material: %v", entryNames(entries))
	}
}

func TestCreateUsesCompactBackupIDsAndAddsSuffixOnlyForCollision(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	createdAt := time.Date(2026, 7, 15, 11, 11, 4, 71_308_000, time.UTC)
	service.now = func() time.Time { return createdAt }

	request := CreateRequest{Kind: KindAutomatic, Reason: ReasonScheduled}
	first, err := service.Create(ctx, request)
	if err != nil {
		t.Fatalf("create first backup: %v", err)
	}
	if first.ID != "auto-20260715-111104Z" {
		t.Fatalf("first backup id = %q", first.ID)
	}
	second, err := service.Create(ctx, request)
	if err != nil {
		t.Fatalf("create second backup: %v", err)
	}
	if second.ID != "auto-20260715-111104Z-0002" {
		t.Fatalf("second backup id = %q", second.ID)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := os.Stat(filepath.Join(paths.Backups, id+Extension)); err != nil {
			t.Fatalf("stat backup %q: %v", id, err)
		}
	}
	if _, err := service.Show(ctx, second.ID); err != nil {
		t.Fatalf("show colliding backup: %v", err)
	}
}

func TestBeforeMigrationBackupIsEncryptedDecryptableAndRetainedSeparately(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	manual, err := service.Create(ctx, CreateRequest{Kind: KindManual, Reason: ReasonManual})
	if err != nil {
		t.Fatalf("create manual backup: %v", err)
	}
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	var newest BackupDetail
	for index := range 5 {
		service.now = func() time.Time { return base.Add(time.Duration(index) * time.Hour) }
		newest, err = service.Create(ctx, CreateRequest{Kind: KindAutomatic, Reason: ReasonBeforeMigration})
		if err != nil {
			t.Fatalf("create before-migration backup %d: %v", index, err)
		}
	}
	identity, err := age.ParseX25519Identity(keys.value)
	if err != nil {
		t.Fatalf("parse test recovery key: %v", err)
	}
	migrationBackups, err := listBeforeMigrationBackups(ctx, paths.Backups, identity)
	if err != nil {
		t.Fatalf("list before-migration backups: %v", err)
	}
	if len(migrationBackups) != migrationRetention {
		t.Fatalf("before-migration backups = %d, want %d", len(migrationBackups), migrationRetention)
	}
	if _, err := os.Stat(filepath.Join(paths.Backups, manual.ID+Extension)); err != nil {
		t.Fatalf("manual backup was removed: %v", err)
	}

	extracted := filepath.Join(t.TempDir(), "before-migration.db")
	inspected, err := inspectArchive(ctx, filepath.Join(paths.Backups, newest.ID+Extension), identity, extracted)
	if err != nil {
		t.Fatalf("decrypt before-migration backup: %v", err)
	}
	if inspected.Manifest.Reason != ReasonBeforeMigration {
		t.Fatalf("before-migration manifest = %#v", inspected.Manifest)
	}
	if err := validateDatabase(ctx, extracted, false); err != nil {
		t.Fatalf("decrypted database failed applied-baseline validation: %v", err)
	}
}

func TestCreateRejectsIntegrityDefectsWithoutPublishing(t *testing.T) {
	for _, defect := range []string{"quick", "foreign_keys", "schema", "json", "references"} {
		t.Run(defect, func(t *testing.T) {
			ctx := context.Background()
			service, paths, _ := newTestService(t)
			applyDatabaseIntegrityDefect(t, paths.Database, defect)

			_, err := service.Create(ctx, CreateRequest{Kind: KindManual, Reason: ReasonManual})
			if err == nil {
				t.Fatal("backup unexpectedly accepted invalid database")
			}
			if strings.Contains(err.Error(), "SECRET_INTEGRITY_SENTINEL") {
				t.Fatalf("backup error exposed stored data: %v", err)
			}
			entries, readErr := os.ReadDir(paths.Backups)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid database published backups: %v", entryNames(entries))
			}
		})
	}
}

func TestRestorePreviewRejectsIntegrityDefectsWithoutChangingCurrentDatabase(t *testing.T) {
	for _, defect := range []string{"quick", "foreign_keys", "schema", "json", "references"} {
		t.Run(defect, func(t *testing.T) {
			ctx := context.Background()
			service, paths, _ := newTestService(t)
			setTestSetting(t, paths, "restore.guard", `"current"`)
			candidate := filepath.Join(t.TempDir(), "candidate.db")
			if err := store.NewFactory(paths.Database).CreateSnapshot(ctx, candidate); err != nil {
				t.Fatalf("create restore candidate: %v", err)
			}
			applyDatabaseIntegrityDefect(t, candidate, defect)
			identity, err := service.loadOrCreateIdentity()
			if err != nil {
				t.Fatal(err)
			}
			createdAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
			id := formatBackupID(KindManual, createdAt, 1)
			if err := writeArchive(ctx, candidate, filepath.Join(paths.Backups, id+Extension), archiveManifest{
				FormatVersion: formatVersion, BackupID: id, Reason: ReasonManual,
				CreatedAtUnixMS: createdAt.UnixMilli(),
			}, identity.Recipient()); err != nil {
				t.Fatalf("write invalid restore candidate: %v", err)
			}

			_, err = service.PreviewRestore(ctx, RestoreSource{BackupID: id})
			assertAppErrorCode(t, err, apperror.BackupInvalid)
			if strings.Contains(err.Error(), "SECRET_INTEGRITY_SENTINEL") {
				t.Fatalf("restore error exposed stored data: %v", err)
			}
			assertSettingValue(t, paths, "restore.guard", `"current"`)
		})
	}
}

func TestListRequestsCleanupWhenBeforeMigrationRetentionIsExceeded(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	identity, err := service.loadOrCreateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	for index := range migrationRetention + 1 {
		createdAt := base.Add(time.Duration(index) * time.Hour)
		id := formatBackupID(KindAutomatic, createdAt, 1)
		if err := writeArchive(ctx, paths.Database, filepath.Join(paths.Backups, id+Extension), archiveManifest{
			FormatVersion: formatVersion, BackupID: id, Reason: ReasonBeforeMigration,
			CreatedAtUnixMS: createdAt.UnixMilli(),
		}, identity.Recipient()); err != nil {
			t.Fatalf("write before-migration archive %d: %v", index, err)
		}
	}

	listed, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Backups) != migrationRetention+1 || !listed.AutomaticCleanupRequired {
		t.Fatalf("list result = %#v", listed)
	}
}

func TestInspectRejectsWrongKeyTamperingAndTruncation(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	backupPath := filepath.Join(paths.Backups, created.ID+Extension)
	identity, err := age.ParseX25519Identity(keys.value)
	if err != nil {
		t.Fatal(err)
	}
	wrongIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	_, err = inspectArchive(ctx, backupPath, wrongIdentity, "")
	assertAppErrorCode(t, err, apperror.BackupInvalid)

	ciphertext, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)/2] ^= 0xff
	tamperedPath := filepath.Join(t.TempDir(), "tampered"+Extension)
	if err := os.WriteFile(tamperedPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = inspectArchive(ctx, tamperedPath, identity, "")
	assertAppErrorCode(t, err, apperror.BackupInvalid)
	if strings.Contains(err.Error(), tamperedPath) {
		t.Fatal("public archive error exposes the input path")
	}

	truncatedPath := filepath.Join(t.TempDir(), "truncated"+Extension)
	if err := os.WriteFile(truncatedPath, ciphertext[:len(ciphertext)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = inspectArchive(ctx, truncatedPath, identity, "")
	assertAppErrorCode(t, err, apperror.BackupInvalid)
}

func TestRecoveryKeyExportImportAndReplacement(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	status, err := service.KeyStatus(ctx)
	if err != nil || !status.Available || status.Recipient == "" {
		t.Fatalf("key status = %#v, err = %v", status, err)
	}

	exportedPath := filepath.Join(t.TempDir(), "profiledeck-recovery-key.txt")
	exported, err := service.ExportKey(ctx, ExportKeyRequest{OutputPath: exportedPath, Confirm: true})
	if err != nil {
		t.Fatalf("export key: %v", err)
	}
	if exported.Recipient != status.Recipient {
		t.Fatalf("exported recipient = %q, want %q", exported.Recipient, status.Recipient)
	}
	info, err := os.Stat(exportedPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("exported key mode = %v, err = %v", info.Mode().Perm(), err)
	}
	same, err := service.ImportKey(ctx, ImportKeyRequest{InputPath: exportedPath, Confirm: true})
	if err != nil || same.Changed {
		t.Fatalf("same key import = %#v, err = %v", same, err)
	}

	replacement, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(t.TempDir(), "replacement-key.txt")
	if err := os.WriteFile(replacementPath, []byte(replacement.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = service.ImportKey(ctx, ImportKeyRequest{InputPath: replacementPath, Confirm: true})
	assertAppErrorCode(t, err, apperror.ConfirmationRequired)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Details["reason"] != "replace_required" {
		t.Fatalf("replacement error details = %#v", appErr)
	}
	replaced, err := service.ImportKey(ctx, ImportKeyRequest{
		InputPath: replacementPath, Replace: true, Confirm: true,
	})
	if err != nil || !replaced.Changed || keys.value != replacement.String() {
		t.Fatalf("replacement result = %#v, err = %v", replaced, err)
	}
	_, err = service.Show(ctx, created.ID)
	assertAppErrorCode(t, err, apperror.BackupInvalid)
	if _, err := os.Stat(filepath.Join(paths.Backups, created.ID+Extension)); err != nil {
		t.Fatalf("key replacement unexpectedly deleted old backup: %v", err)
	}
}

func TestCreateFailsWithoutWritableCredentialStoreAndPublishesNothing(t *testing.T) {
	_, paths, _ := newTestService(t)
	keys := &memoryKeyStore{setErr: errors.New("credential store unavailable")}
	service := newService(paths, store.NewFactory(paths.Database), keys, directExclusiveRunner{})

	_, err := service.Create(context.Background(), CreateRequest{})
	assertAppErrorCode(t, err, apperror.BackupFailed)
	entries, readErr := os.ReadDir(paths.Backups)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed key persistence published files: %v", entryNames(entries))
	}
}

func TestCreateRefusesConcurrentCrossProcessBackupOperation(t *testing.T) {
	service, paths, keys := newTestService(t)
	lock, err := targetfs.AcquireLock(paths.BackupLock, "competing-backup-process")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	_, err = service.Create(context.Background(), CreateRequest{})
	assertAppErrorCode(t, err, apperror.LockAcquireFailed)
	if keys.value != "" {
		t.Fatal("blocked backup operation changed the recovery key")
	}
	entries, readErr := os.ReadDir(paths.Backups)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("blocked backup operation published files: %v", entryNames(entries))
	}
}

func TestAutomaticRetentionKeepsTenNewestAndAllManualBackups(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for index := range 12 {
		name := testBackupID("auto", base.Add(time.Duration(index)*time.Hour)) + Extension
		if err := os.WriteFile(filepath.Join(dir, name), []byte("auto"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manualNames := []string{
		testBackupID("manual", base) + Extension,
		testBackupID("manual", base.Add(24*time.Hour)) + Extension,
	}
	for _, name := range manualNames {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("manual"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	malformedName := "auto-not-a-backup-id" + Extension
	if err := os.WriteFile(filepath.Join(dir, malformedName), []byte("unrecognized"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := retainAutomaticBackups(dir, 10); err != nil {
		t.Fatalf("retain automatic backups: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(entries)
	if len(names) != 13 {
		t.Fatalf("retained files = %d, want 13: %v", len(names), names)
	}
	if !containsString(names, malformedName) {
		t.Fatalf("unrecognized file %q was deleted", malformedName)
	}
	for _, name := range manualNames {
		if !containsString(names, name) {
			t.Fatalf("manual backup %q was removed", name)
		}
	}
	for index := range 2 {
		oldest := testBackupID("auto", base.Add(time.Duration(index)*time.Hour)) + Extension
		if containsString(names, oldest) {
			t.Fatalf("old automatic backup %q was retained", oldest)
		}
	}
}

func TestAutomaticRetentionFailureDoesNotReversePublishedBackup(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for index := range automaticRetention {
		name := testBackupID("auto", base.Add(time.Duration(index)*time.Hour)) + Extension
		if err := os.WriteFile(filepath.Join(paths.Backups, name), []byte("auto"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service.now = func() time.Time { return base.Add(24 * time.Hour) }
	service.retain = func(string, int) error { return errors.New("private cleanup failure") }

	created, err := service.Create(ctx, CreateRequest{Kind: KindAutomatic, Reason: ReasonScheduled})
	if err != nil {
		t.Fatalf("create automatic backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Backups, created.ID+Extension)); err != nil {
		t.Fatalf("published backup is missing: %v", err)
	}
	listed, err := service.List(ctx)
	if err != nil || len(listed.Backups) != automaticRetention+1 || !listed.AutomaticCleanupRequired {
		t.Fatalf("list after cleanup failure = %#v, err = %v", listed, err)
	}

	service.retain = retainAutomaticBackups
	due, err := service.CreateAutomaticIfDue(ctx)
	if err != nil || due != nil {
		t.Fatalf("automatic cleanup retry created backup = %#v, err = %v", due, err)
	}
	listed, err = service.List(ctx)
	if err != nil || len(listed.Backups) != automaticRetention || listed.AutomaticCleanupRequired {
		t.Fatalf("list after cleanup retry = %#v, err = %v", listed, err)
	}
}

func TestAutomaticBackupRunsWhenTwentyFourHoursHaveElapsed(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newTestService(t)
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	first, err := service.CreateAutomaticIfDue(ctx)
	if err != nil || first == nil {
		t.Fatalf("first automatic backup = %#v, err = %v", first, err)
	}
	service.now = func() time.Time { return base.Add(23 * time.Hour) }
	second, err := service.CreateAutomaticIfDue(ctx)
	if err != nil || second != nil {
		t.Fatalf("backup before due = %#v, err = %v", second, err)
	}
	service.now = func() time.Time { return base.Add(25 * time.Hour) }
	third, err := service.CreateAutomaticIfDue(ctx)
	if err != nil || third == nil {
		t.Fatalf("overdue automatic backup = %#v, err = %v", third, err)
	}
}

func TestConcurrentAutomaticBackupChecksCreateOneDueBackup(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }
	start := make(chan struct{})
	results := make(chan *BackupDetail, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			result, err := service.CreateAutomaticIfDue(ctx)
			results <- result
			errors <- err
		}()
	}
	close(start)
	created := 0
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("automatic backup check: %v", err)
		}
		if result := <-results; result != nil {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent due checks created %d backups, want 1", created)
	}
}

func TestRestoreResetsRuntimeStateAndPreservesExternalFiles(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	db := openHealthyStore(t, paths)
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: "codex", Name: "Codex", AdapterID: "codex", Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID: "before-profile", Name: "Before", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "restore.value", ValueJSON: `"before"`}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "applied-switch", ProfileID: "before-profile", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID: "applied-switch", ProfileID: "before-profile", ProviderID: "codex", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "pending-switch", ProfileID: "next-profile", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatalf("create restore source: %v", err)
	}
	db = openHealthyStore(t, paths)
	if err := db.ResolveOperation(ctx, "pending-switch", "closed_before_restore_test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "restore.value", ValueJSON: `"after"`}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	recoveryPoint := filepath.Join(paths.Recovery, "pending-switch", "target.json")
	if err := os.MkdirAll(filepath.Dir(recoveryPoint), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryPoint, []byte("recovery material"), 0o600); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(t.TempDir(), "tool-owned.json")
	if err := os.WriteFile(externalPath, []byte("external state"), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil || !preview.CurrentDatabaseHealthy || preview.SchemaUpgradeRequired {
		t.Fatalf("restore preview = %#v, err = %v", preview, err)
	}
	result, err := service.Restore(ctx, RestoreRequest{
		Source: RestoreSource{BackupID: created.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("restore application data: %v", err)
	}
	if result.SafetyBackup == nil || result.SafetyBackupSkipped || !result.RestartRequired || !result.RecoveryCleanupCompleted {
		t.Fatalf("restore result = %#v", result)
	}

	db = openHealthyStore(t, paths)
	defer db.Close()
	setting, err := db.GetSetting(ctx, "restore.value")
	if err != nil || setting.ValueJSON != `"before"` {
		t.Fatalf("restored setting = %#v, err = %v", setting, err)
	}
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "codex"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored active state was not cleared: %v", err)
	}
	operation, err := db.GetOperation(ctx, "pending-switch")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != store.OperationStatusPending || operation.ResolutionKind != "application_restore" || operation.ResolvedAtUnixMS == 0 {
		t.Fatalf("restored incomplete operation = %#v", operation)
	}
	recoveryEntries, err := os.ReadDir(paths.Recovery)
	if err != nil || len(recoveryEntries) != 0 {
		t.Fatalf("recovery directory = %v, err = %v", entryNames(recoveryEntries), err)
	}
	externalContent, err := os.ReadFile(externalPath)
	if err != nil || string(externalContent) != "external state" {
		t.Fatalf("external file changed: %q, err = %v", externalContent, err)
	}
}

func TestRestoreBlocksWhilePartialSwitchRecoveryIsUnresolved(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	setTestSetting(t, paths, "restore.value", `"before"`)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	setTestSetting(t, paths, "restore.value", `"after"`)
	db := openHealthyStore(t, paths)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "partial-switch", ProfileID: "profile-a", MetadataJSON: `{"checkpoint":"recovery_created"}`,
	}); err != nil {
		t.Fatal(err)
	}
	metadata := `{"checkpoint":"recovery_created"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID: "partial-switch", ErrorCode: "TARGET_WRITE_FAILED", ErrorMessage: "target could not be updated", MetadataJSON: &metadata,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	recoveryPath := filepath.Join(paths.Recovery, "partial-switch", "target.json")
	if err := os.MkdirAll(filepath.Dir(recoveryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryPath, []byte("pre-switch"), 0o600); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(t.TempDir(), "tool-owned.json")
	if err := os.WriteFile(externalPath, []byte("partially switched"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Restore(ctx, RestoreRequest{
		Source: RestoreSource{BackupID: created.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	})
	assertAppErrorCode(t, err, apperror.OperationRecoveryRequired)
	db = openHealthyStore(t, paths)
	defer db.Close()
	setting, err := db.GetSetting(ctx, "restore.value")
	if err != nil || setting.ValueJSON != `"after"` {
		t.Fatalf("blocked restore changed database: %#v, %v", setting, err)
	}
	if operation, err := db.GetOperation(ctx, "partial-switch"); err != nil || operation.ResolvedAtUnixMS != 0 {
		t.Fatalf("blocked restore resolved recovery source: %#v, %v", operation, err)
	}
	if raw, err := os.ReadFile(recoveryPath); err != nil || string(raw) != "pre-switch" {
		t.Fatalf("blocked restore changed recovery material: %q, %v", raw, err)
	}
	if raw, err := os.ReadFile(externalPath); err != nil || string(raw) != "partially switched" {
		t.Fatalf("blocked restore changed external target: %q, %v", raw, err)
	}
	list, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, backup := range list.Backups {
		if backup.Kind == KindAutomatic {
			t.Fatalf("blocked restore created safety backup %#v", backup)
		}
	}
}

func TestRestoreCanReplaceDamagedCurrentDatabaseWithoutSafetyBackup(t *testing.T) {
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	setTestSetting(t, paths, "restore.value", `"recoverable"`)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	removeDatabaseFiles(paths.Database)
	if err := os.WriteFile(paths.Database, []byte("damaged database"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err = service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil || preview.CurrentDatabaseHealthy {
		t.Fatalf("damaged database preview = %#v, err = %v", preview, err)
	}
	result, err := service.Restore(ctx, RestoreRequest{
		Source: RestoreSource{BackupID: created.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("restore over damaged database: %v", err)
	}
	if !result.SafetyBackupSkipped || result.SafetyBackup != nil {
		t.Fatalf("damaged database restore safety result = %#v", result)
	}
	db := openHealthyStore(t, paths)
	defer db.Close()
	setting, err := db.GetSetting(ctx, "restore.value")
	if err != nil || setting.ValueJSON != `"recoverable"` {
		t.Fatalf("restored setting = %#v, err = %v", setting, err)
	}
}

func TestCommittedRestoreKeepsCleanupRequirementAndBlocksSecondRestore(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("symlink setup is platform-specific")
	}
	ctx := context.Background()
	service, paths, _ := newTestService(t)
	setTestSetting(t, paths, "restore.value", `"recoverable"`)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	removeDatabaseFiles(paths.Database)
	if err := os.WriteFile(paths.Database, []byte("damaged database"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.Recovery); err != nil {
		t.Fatal(err)
	}
	preview, err = service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil || preview.CurrentDatabaseHealthy {
		t.Fatalf("damaged preview = %#v, %v", preview, err)
	}
	request := RestoreRequest{
		Source: RestoreSource{BackupID: created.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	}
	result, err := service.Restore(ctx, request)
	if err != nil {
		t.Fatalf("first Restore() error = %v", err)
	}
	if result.RecoveryCleanupCompleted || !result.RestartRequired || !result.SafetyBackupSkipped {
		t.Fatalf("first Restore() = %#v", result)
	}
	db := openHealthyStore(t, paths)
	required, stateErr := db.RecoveryCleanupRequired(ctx)
	_ = db.Close()
	if stateErr != nil || !required {
		t.Fatalf("restored cleanup requirement = %t, %v", required, stateErr)
	}

	_, err = service.Restore(ctx, request)
	assertAppErrorCode(t, err, apperror.OperationRecoveryCleanupRequired)
	list, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, backup := range list.Backups {
		if backup.Kind == KindAutomatic {
			t.Fatalf("blocked second restore created safety backup %#v", backup)
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside" {
		t.Fatalf("recovery symlink target changed: %q, %v", raw, err)
	}
}

func TestRestorePreparationMigratesOldSchemaAndRejectsUnsupportedSchema(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keys.value = identity.String()

	oldDatabasePath := filepath.Join(t.TempDir(), "old.db")
	oldDatabase, err := sql.Open("sqlite", oldDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, createErr := oldDatabase.ExecContext(ctx, `CREATE TABLE legacy_marker (id INTEGER PRIMARY KEY)`)
	if err := errors.Join(createErr, oldDatabase.Close()); err != nil {
		t.Fatal(err)
	}
	oldArchive := writeTestArchive(t, oldDatabasePath, identity, KindManual, ReasonManual)
	preview, err := service.PreviewRestore(ctx, RestoreSource{FilePath: oldArchive})
	if err != nil || !preview.SchemaUpgradeRequired {
		t.Fatalf("old schema preview = %#v, err = %v", preview, err)
	}

	futureDatabasePath := filepath.Join(t.TempDir(), "future.db")
	if err := store.NewFactory(paths.Database).CreateSnapshot(ctx, futureDatabasePath); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("sqlite", futureDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := sqlDB.ExecContext(ctx, `
		INSERT INTO bun_migrations (name, group_id, migrated_at)
		VALUES ('209912310001', 999, CURRENT_TIMESTAMP)
	`)
	closeErr := sqlDB.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatalf("mark future schema: %v", err)
	}
	futureArchive := writeTestArchive(t, futureDatabasePath, identity, KindManual, ReasonManual)
	_, err = service.PreviewRestore(ctx, RestoreSource{FilePath: futureArchive})
	assertAppErrorCode(t, err, apperror.BackupSchemaUnsupported)
	archiveContent, err := os.ReadFile(futureArchive)
	if err != nil {
		t.Fatalf("read future archive: %v", err)
	}
	_, err = service.Restore(ctx, RestoreRequest{
		Source: RestoreSource{FilePath: futureArchive}, ExpectedFingerprint: fmt.Sprintf("%x", sha256.Sum256(archiveContent)), Confirm: true,
	})
	assertAppErrorCode(t, err, apperror.BackupSchemaUnsupported)
}

func TestRestoreRefusesExclusiveLockConflictAndCanRetry(t *testing.T) {
	ctx := context.Background()
	service, paths, keys := newTestService(t)
	setTestSetting(t, paths, "restore.value", `"before"`)
	created, err := service.Create(ctx, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	setTestSetting(t, paths, "restore.value", `"after"`)
	preview, err := service.PreviewRestore(ctx, RestoreSource{BackupID: created.ID})
	if err != nil {
		t.Fatal(err)
	}

	lease, err := runtime.AcquireDataLease(paths.DataLock)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	competingLease, err := runtime.AcquireDataLease(paths.DataLock)
	if err != nil {
		t.Fatal(err)
	}
	lockedService := newService(paths, store.NewFactory(paths.Database), keys, lease)
	request := RestoreRequest{
		Source: RestoreSource{BackupID: created.ID}, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
	}
	_, err = lockedService.Restore(ctx, request)
	assertAppErrorCode(t, err, apperror.LockAcquireFailed)
	assertSettingValue(t, paths, "restore.value", `"after"`)

	competingLease.Close()
	if _, err := lockedService.Restore(ctx, request); err != nil {
		t.Fatalf("retry restore after lock release: %v", err)
	}
	assertSettingValue(t, paths, "restore.value", `"before"`)
}

func TestCanceledArchiveWriteLeavesNoPublishedOrStagingFile(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(databasePath, bytes.Repeat([]byte("database"), 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	id := testBackupID("manual", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	destination := filepath.Join(dir, id+Extension)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = writeArchive(ctx, databasePath, destination, archiveManifest{
		FormatVersion: formatVersion, BackupID: id, Reason: ReasonManual, CreatedAtUnixMS: time.Now().UnixMilli(),
	}, identity.Recipient())
	if err == nil {
		t.Fatal("expected canceled archive write to fail")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled archive was published: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".profiledeck-backup-") {
			t.Fatalf("canceled archive left staging file %q", entry.Name())
		}
	}
}

func newTestService(t *testing.T) (*Service, runtime.Paths, *memoryKeyStore) {
	t.Helper()
	runtimeService, err := runtime.NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeService.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	paths := runtimeService.Paths()
	db, err := store.NewFactory(paths.Database).Open(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	keys := &memoryKeyStore{}
	return newService(paths, store.NewFactory(paths.Database), keys, directExclusiveRunner{}), paths, keys
}

func openHealthyStore(t *testing.T, paths runtime.Paths) *store.Store {
	t.Helper()
	db, err := store.NewFactory(paths.Database).OpenHealthy(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func setTestSetting(t *testing.T, paths runtime.Paths, key, value string) {
	t.Helper()
	db := openHealthyStore(t, paths)
	if _, err := db.UpsertSetting(context.Background(), store.UpsertSettingParams{Key: key, ValueJSON: value}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertSettingValue(t *testing.T, paths runtime.Paths, key, want string) {
	t.Helper()
	db := openHealthyStore(t, paths)
	defer db.Close()
	setting, err := db.GetSetting(context.Background(), key)
	if err != nil || setting.ValueJSON != want {
		t.Fatalf("setting %q = %#v, err = %v; want %q", key, setting, err, want)
	}
}

func writeTestArchive(
	t *testing.T,
	databasePath string,
	identity *age.X25519Identity,
	kind string,
	reason string,
) string {
	t.Helper()
	createdAt := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	id := formatBackupID(kind, createdAt, 1)
	destination := filepath.Join(t.TempDir(), id+Extension)
	if err := writeArchive(context.Background(), databasePath, destination, archiveManifest{
		FormatVersion: formatVersion, BackupID: id, Reason: reason, CreatedAtUnixMS: createdAt.UnixMilli(),
	}, identity.Recipient()); err != nil {
		t.Fatalf("write test archive: %v", err)
	}
	return destination
}

func testBackupID(prefix string, createdAt time.Time) string {
	return fmt.Sprintf("%s-%s", prefix, createdAt.UTC().Format(backupTimeLayout))
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertAppErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func applyDatabaseIntegrityDefect(t *testing.T, path, defect string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var corruptRootPage, pageSize int64
	switch defect {
	case "quick":
		if _, err := db.Exec(`CREATE TABLE corruption_probe (value BLOB)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO corruption_probe (value) VALUES (zeroblob(262144))`); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE name = 'corruption_probe'`).Scan(&corruptRootPage); err != nil {
			t.Fatal(err)
		}
	case "foreign_keys":
		if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO profile_credential_bindings
			(profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 'auth', 'missing-credential', 1, 1)`); err != nil {
			t.Fatal(err)
		}
	case "schema":
		if _, err := db.Exec(`DROP INDEX idx_usage_models_source_model`); err != nil {
			t.Fatal(err)
		}
	case "json":
		if _, err := db.Exec(`INSERT INTO settings (key, value_json, updated_at_unix_ms)
			VALUES ('invalid-json', '{"token":"SECRET_INTEGRITY_SENTINEL"', 1)`); err != nil {
			t.Fatal(err)
		}
	case "references":
		if _, err := db.Exec(`INSERT INTO provider_profile_settings
			(profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 0, 0, 1)`); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown database defect %q", defect)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if corruptRootPage > 0 {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt(make([]byte, pageSize), (corruptRootPage-1)*pageSize); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
