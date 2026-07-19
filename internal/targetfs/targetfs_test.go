package targetfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLockAcquireRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")

	lock, err := AcquireLock(lockPath, "owner-a")
	if err != nil {
		t.Fatalf("expected lock acquire to succeed, got %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file to exist, got %v", err)
	}

	_, err = AcquireLock(lockPath, "owner-b")
	assertKind(t, err, KindLockHeld)

	lock.Release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file to remain for diagnostics, got %v", err)
	}

	second, err := AcquireLock(lockPath, "owner-b")
	if err != nil {
		t.Fatalf("expected second lock acquire to succeed after release, got %v", err)
	}
	second.Release()
}

func TestAcquireLockReusesExistingDiagnosticLockFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	if err := os.WriteFile(lockPath, []byte("stale-owner\npid=999999999\ncreated_at_unix_ms=1\n"), 0o600); err != nil {
		t.Fatalf("expected stale lock setup to succeed, got %v", err)
	}

	lock, err := AcquireLock(lockPath, "owner-a")
	if err != nil {
		t.Fatalf("expected existing diagnostic lock file to be reused, got %v", err)
	}
	defer lock.Release()

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("expected lock file read to succeed, got %v", err)
	}
	if !strings.Contains(string(raw), "owner-a") || strings.Contains(string(raw), "stale-owner") {
		t.Fatalf("expected diagnostic lock content to be replaced, got %q", string(raw))
	}
}

func TestAcquireLockHandlesEmptyDiagnosticLockFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("expected empty lock setup to succeed, got %v", err)
	}

	lock, err := AcquireLock(lockPath, "owner-a")
	if err != nil {
		t.Fatalf("expected empty diagnostic lock file to be reused, got %v", err)
	}
	defer lock.Release()

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("expected lock file read to succeed, got %v", err)
	}
	if !strings.Contains(string(raw), "owner-a") || !strings.Contains(string(raw), "pid=") {
		t.Fatalf("expected diagnostic lock content to be written, got %q", string(raw))
	}
}

func TestProbeLockDoesNotRewriteDiagnosticFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	content := "switch-stale\npid=999999999\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}

	probe, err := ProbeLock(lockPath)
	if err != nil {
		t.Fatalf("expected lock probe to succeed, got %v", err)
	}
	if !probe.Exists || probe.Held {
		t.Fatalf("unexpected lock probe: %#v", probe)
	}
	if got := readTestFile(t, lockPath); got != content {
		t.Fatalf("expected probe not to rewrite diagnostic file, got %q", got)
	}
}

func TestProbeLockReportsHeldLocalLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	lock, err := AcquireLock(lockPath, "switch-active")
	if err != nil {
		t.Fatalf("expected lock acquire to succeed, got %v", err)
	}
	defer lock.Release()

	probe, err := ProbeLock(lockPath)
	if err != nil {
		t.Fatalf("expected lock probe to succeed, got %v", err)
	}
	if !probe.Exists || !probe.Held {
		t.Fatalf("expected held lock probe, got %#v", probe)
	}
}

func TestProbeLockSupportsReadOnlyDiagnosticFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod read-only semantics are platform-specific")
	}

	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	content := "switch-stale\npid=999999999\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(lockPath, []byte(content), 0o400); err != nil {
		t.Fatalf("expected read-only lock setup to succeed, got %v", err)
	}

	probe, err := ProbeLock(lockPath)
	if err != nil {
		t.Fatalf("expected read-only lock probe to succeed, got %v", err)
	}
	if !probe.Exists || probe.Held {
		t.Fatalf("unexpected read-only lock probe: %#v", probe)
	}
}

func TestLocalLockKeyCaseNormalization(t *testing.T) {
	dir := t.TempDir()
	upperPath := filepath.Join(dir, "Switch.Lock")
	lowerPath := filepath.Join(dir, "switch.lock")

	keysMatch := localLockKey(upperPath) == localLockKey(lowerPath)
	switch runtime.GOOS {
	case "darwin", "windows":
		if !keysMatch {
			t.Fatalf("expected local lock keys to be case-normalized on %s", runtime.GOOS)
		}
	default:
		if keysMatch {
			t.Fatalf("expected local lock keys to preserve case on %s", runtime.GOOS)
		}
	}
}

func TestRemoveStaleLockFileUsesHashAndLockGuard(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	content := "switch-stale\npid=999999999\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}

	if err := RemoveStaleLockFile(lockPath, sha256String("different")); err == nil {
		t.Fatalf("expected stale lock remove to reject changed content")
	}
	if got := readTestFile(t, lockPath); got != content {
		t.Fatalf("expected rejected remove to keep lock file, got %q", got)
	}
	if err := RemoveStaleLockFile(lockPath, sha256String(content)); err != nil {
		t.Fatalf("expected stale lock remove to succeed, got %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
}

func TestRemoveStaleLockFileRejectsHeldLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	lock, err := AcquireLock(lockPath, "switch-active")
	if err != nil {
		t.Fatalf("expected lock acquire to succeed, got %v", err)
	}
	defer lock.Release()

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("expected lock read to succeed, got %v", err)
	}
	err = RemoveStaleLockFile(lockPath, sha256String(string(raw)))
	assertKind(t, err, KindLockHeld)
}

func TestOpenedUnlinkedLockFileIsRejectedAfterFlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow removing an open lock file in this scenario")
	}

	lockPath := filepath.Join(t.TempDir(), "switch.lock")
	content := "switch-stale\npid=999999999\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	staleFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("expected stale lock open to succeed, got %v", err)
	}
	defer staleFile.Close()

	if err := RemoveStaleLockFile(lockPath, sha256String(content)); err != nil {
		t.Fatalf("expected stale lock remove to succeed, got %v", err)
	}
	if err := tryLockFile(staleFile); err != nil {
		t.Fatalf("expected OS to allow locking the unlinked file, got %v", err)
	}
	defer func() {
		_ = unlockFileHandle(staleFile)
	}()

	_, err = verifyOpenedLockFileCurrent(lockPath, staleFile)
	assertKind(t, err, KindTargetChanged)
}

func TestInspectAndVerifyExpected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "target.txt")

	state, err := Inspect(ctx, path)
	if err != nil {
		t.Fatalf("expected missing inspect to succeed, got %v", err)
	}
	if state.Exists {
		t.Fatalf("expected missing target state, got %#v", state)
	}
	if err := VerifyExpected(ctx, ExpectedTarget{Path: path, Exists: false}); err != nil {
		t.Fatalf("expected missing verify to succeed, got %v", err)
	}

	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	state, err = Inspect(ctx, path)
	if err != nil {
		t.Fatalf("expected regular inspect to succeed, got %v", err)
	}
	if !state.Exists || !state.IsRegular || state.SHA256 != sha256String("before\n") {
		t.Fatalf("unexpected target state: %#v", state)
	}
	if err := VerifyExpected(ctx, ExpectedTarget{TargetID: "target-a", Path: path, Exists: true, SHA256: state.SHA256}); err != nil {
		t.Fatalf("expected matching verify to succeed, got %v", err)
	}

	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatalf("expected target change to succeed, got %v", err)
	}
	assertKind(t, VerifyExpected(ctx, ExpectedTarget{TargetID: "target-a", Path: path, Exists: true, SHA256: state.SHA256}), KindTargetChanged)
}

func TestInspectRejectsOversizedTarget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "large.target")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	if err := file.Truncate(MaxFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("expected target truncate to succeed, got %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("expected target close to succeed, got %v", err)
	}

	assertKind(t, VerifyExpected(ctx, ExpectedTarget{TargetID: "target-a", Path: path, Exists: true}), KindTargetChanged)
}

func TestCopyBackupFileRejectsOversizedTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "large.target")
	destination := filepath.Join(dir, "backup.bak")
	file, err := os.Create(source)
	if err != nil {
		t.Fatalf("expected source create to succeed, got %v", err)
	}
	if err := file.Truncate(MaxFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("expected source truncate to succeed, got %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("expected source close to succeed, got %v", err)
	}

	_, err = CopyBackupFile(ctx, source, destination)
	assertKind(t, err, KindBackupFailed)
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected oversized backup destination not to remain, got %v", err)
	}
}

func TestInspectReportsSymlinkAndDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is platform-specific")
	}

	ctx := context.Background()
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.txt")
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(realPath, []byte("raw\n"), 0o600); err != nil {
		t.Fatalf("expected real target setup to succeed, got %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("expected symlink setup to succeed, got %v", err)
	}

	state, err := Inspect(ctx, linkPath)
	if err != nil {
		t.Fatalf("expected symlink inspect to succeed, got %v", err)
	}
	if !state.Exists || !state.IsSymlink {
		t.Fatalf("expected symlink state, got %#v", state)
	}
	assertKind(t, VerifyExpected(ctx, ExpectedTarget{Path: linkPath, Exists: true}), KindTargetChanged)

	state, err = Inspect(ctx, dir)
	if err != nil {
		t.Fatalf("expected directory inspect to succeed, got %v", err)
	}
	if !state.Exists || !state.IsDir {
		t.Fatalf("expected directory state, got %#v", state)
	}
	assertKind(t, VerifyExpected(ctx, ExpectedTarget{Path: dir, Exists: true}), KindTargetChanged)
}

func TestBackupCopyAtomicWriteAndGuardedRemove(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.txt")
	backupPath := filepath.Join(dir, "backup.bak")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o640); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}

	copiedSHA, err := CopyBackupFile(ctx, targetPath, backupPath)
	if err != nil {
		t.Fatalf("expected backup copy to succeed, got %v", err)
	}
	if copiedSHA != sha256String("before\n") {
		t.Fatalf("unexpected backup hash: %s", copiedSHA)
	}

	if err := AtomicWriteContent(ctx, AtomicWriteContentRequest{
		Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: copiedSHA},
		Content:  "after\n",
	}); err != nil {
		t.Fatalf("expected atomic write to succeed, got %v", err)
	}
	if got := readTestFile(t, targetPath); got != "after\n" {
		t.Fatalf("unexpected target content: %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(targetPath)
		if err != nil {
			t.Fatalf("expected target stat to succeed, got %v", err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("expected POSIX target mode to be preserved, got %#o", info.Mode().Perm())
		}
	}

	removed, err := GuardedRemove(ctx, GuardedRemoveRequest{
		Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("after\n")},
	})
	if err != nil {
		t.Fatalf("expected guarded remove to succeed, got %v", err)
	}
	if !removed {
		t.Fatalf("expected guarded remove to remove target")
	}
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected target to be removed, got %v", err)
	}
}

func TestGuardedRemoveRejectsChangedTarget(t *testing.T) {
	ctx := context.Background()
	targetPath := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(targetPath, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}

	removed, err := GuardedRemove(ctx, GuardedRemoveRequest{
		Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("expected\n")},
	})
	assertKind(t, err, KindTargetChanged)
	if removed {
		t.Fatalf("expected changed target not to be removed")
	}
	if got := readTestFile(t, targetPath); got != "changed\n" {
		t.Fatalf("expected changed target to remain, got %q", got)
	}
}

func TestAtomicWriteRejectsTargetChangedDuringTempWrite(t *testing.T) {
	ctx := context.Background()
	targetPath := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}

	err := atomicWrite(ctx, ExpectedTarget{
		TargetID: "target-a",
		Path:     targetPath,
		Exists:   true,
		SHA256:   sha256String("before\n"),
	}, &mutatingReader{
		path:    targetPath,
		content: "managed\n",
	}, 0, false, sourceGuard{})
	assertKind(t, err, KindTargetChanged)
	if got := readTestFile(t, targetPath); got != "user-modified\n" {
		t.Fatalf("expected external target change to remain, got %q", got)
	}
}

func TestAtomicWriteRequiresParentSyncBeforeAndAfterRename(t *testing.T) {
	ctx := context.Background()
	targetPath := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := ExpectedTarget{
		TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("before\n"),
	}

	t.Run("pre-sync failure leaves target unchanged", func(t *testing.T) {
		calls := 0
		err := atomicWriteWithDirectorySync(
			ctx, expected, strings.NewReader("after\n"), 0, false, sourceGuard{},
			func(string) error {
				calls++
				return errors.New("pre-sync failed")
			},
		)
		assertKind(t, err, KindWriteFailed)
		if calls != 1 || readTestFile(t, targetPath) != "before\n" {
			t.Fatalf("pre-sync failure calls=%d content=%q", calls, readTestFile(t, targetPath))
		}
	})

	t.Run("post-sync failure reports changed target", func(t *testing.T) {
		calls := 0
		err := atomicWriteWithDirectorySync(
			ctx, expected, strings.NewReader("after\n"), 0, false, sourceGuard{},
			func(string) error {
				calls++
				if calls == 2 {
					return errors.New("post-sync failed")
				}
				return nil
			},
		)
		assertKind(t, err, KindWriteFailed)
		if calls != 2 || readTestFile(t, targetPath) != "after\n" {
			t.Fatalf("post-sync failure calls=%d content=%q", calls, readTestFile(t, targetPath))
		}
	})
}

func TestGuardedRemoveRequiresParentSyncBeforeAndAfterRemoval(t *testing.T) {
	ctx := context.Background()

	t.Run("pre-sync failure leaves target", func(t *testing.T) {
		targetPath := filepath.Join(t.TempDir(), "target.txt")
		if err := os.WriteFile(targetPath, []byte("managed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		removed, err := guardedRemoveWithDirectorySync(ctx, GuardedRemoveRequest{
			Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("managed\n")},
		}, func(string) error { return errors.New("pre-sync failed") })
		assertKind(t, err, KindWriteFailed)
		if removed || readTestFile(t, targetPath) != "managed\n" {
			t.Fatalf("pre-sync failure removed=%t", removed)
		}
	})

	t.Run("target change during pre-sync is preserved", func(t *testing.T) {
		targetPath := filepath.Join(t.TempDir(), "target.txt")
		if err := os.WriteFile(targetPath, []byte("managed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		removed, err := guardedRemoveWithDirectorySync(ctx, GuardedRemoveRequest{
			Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("managed\n")},
		}, func(string) error {
			return os.WriteFile(targetPath, []byte("external\n"), 0o600)
		})
		assertKind(t, err, KindTargetChanged)
		if removed || readTestFile(t, targetPath) != "external\n" {
			t.Fatalf("concurrent target change removed=%t content=%q", removed, readTestFile(t, targetPath))
		}
	})

	t.Run("post-sync failure reports completed removal", func(t *testing.T) {
		targetPath := filepath.Join(t.TempDir(), "target.txt")
		if err := os.WriteFile(targetPath, []byte("managed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		calls := 0
		removed, err := guardedRemoveWithDirectorySync(ctx, GuardedRemoveRequest{
			Expected: ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("managed\n")},
		}, func(string) error {
			calls++
			if calls == 2 {
				return errors.New("post-sync failed")
			}
			return nil
		})
		assertKind(t, err, KindWriteFailed)
		if !removed || !errors.Is(statError(targetPath), os.ErrNotExist) {
			t.Fatalf("post-sync failure removed=%t stat=%v", removed, statError(targetPath))
		}
	})
}

func statError(path string) error {
	_, err := os.Stat(path)
	return err
}

func TestAtomicWriteFileRejectsSourceHashMismatch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.txt")
	sourcePath := filepath.Join(dir, "backup.bak")
	if err := os.WriteFile(targetPath, []byte("managed\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatalf("expected source setup to succeed, got %v", err)
	}

	err := AtomicWriteFile(ctx, AtomicWriteFileRequest{
		Expected:     ExpectedTarget{TargetID: "target-a", Path: targetPath, Exists: true, SHA256: sha256String("managed\n")},
		SourcePath:   sourcePath,
		SourceSHA256: sha256String("original\n"),
	})
	assertKind(t, err, KindBackupInvalid)
	if got := readTestFile(t, targetPath); got != "managed\n" {
		t.Fatalf("expected target to remain unchanged, got %q", got)
	}
}

type mutatingReader struct {
	path    string
	content string
	mutated bool
}

func (r *mutatingReader) Read(p []byte) (int, error) {
	if !r.mutated {
		if err := os.WriteFile(r.path, []byte("user-modified\n"), 0o600); err != nil {
			return 0, err
		}
		r.mutated = true
	}
	if r.content == "" {
		return 0, io.EOF
	}
	n := copy(p, r.content)
	r.content = r.content[n:]
	return n, nil
}

func assertKind(t *testing.T, err error, kind Kind) {
	t.Helper()

	var targetErr *Error
	if !errors.As(err, &targetErr) {
		t.Fatalf("expected targetfs error %s, got %T: %v", kind, err, err)
	}
	if targetErr.Kind != kind {
		t.Fatalf("expected error kind %s, got %s", kind, targetErr.Kind)
	}
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file read to succeed, got %v", err)
	}
	return string(raw)
}
