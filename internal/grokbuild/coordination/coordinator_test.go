package coordination

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/providercoord"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

func TestCoordinatorWritesCompatibleHolderAndReleases(t *testing.T) {
	home := testHome(t)
	coordinator := NewCoordinator(home)
	held, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	raw, err := os.ReadFile(home.LockPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		t.Fatalf("holder = %q", raw)
	}
	if pid, err := strconv.Atoi(parts[0]); err != nil || pid != os.Getpid() {
		t.Fatalf("holder pid = %q", parts[0])
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		t.Fatalf("holder timestamp = %q", parts[1])
	}
	if err := held.Validate(context.Background()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	held.Release()

	reacquired, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	reacquired.Release()
}

func TestCoordinatorRejectsCustomAuthEnvironmentBeforeLocking(t *testing.T) {
	home := testHome(t)
	t.Setenv("GROK_AUTH", "synthetic")
	t.Setenv("GROK_AUTH_PATH", "")
	_, err := NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	assertCode(t, err, apperror.GrokBuildInvalid)
	if _, statErr := os.Stat(home.LockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("lock file was touched: %v", statErr)
	}
}

func TestCoordinatorRejectsPersistedHomeMismatch(t *testing.T) {
	home := testHome(t)
	other := testHome(t)
	metadata, err := grokpreset.ProviderMetadataJSON(other)
	if err != nil {
		t.Fatalf("ProviderMetadataJSON: %v", err)
	}
	_, err = NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID, ProviderMetadataJSON: metadata,
	})
	assertCode(t, err, apperror.GrokBuildInvalid)
}

func TestCoordinatorRejectsUnsupportedTargetsBeforeLocking(t *testing.T) {
	t.Setenv("GROK_AUTH", "")
	t.Setenv("GROK_AUTH_PATH", "")
	for _, testCase := range []struct {
		name   string
		target providercoord.Target
	}{
		{
			name: "auth outside Home",
			target: providercoord.Target{
				ID: grokconfig.AuthTargetID, BackendID: switchtarget.BackendFile,
			},
		},
		{
			name: "config outside Home",
			target: providercoord.Target{
				ID: grokconfig.ConfigTargetID, BackendID: switchtarget.BackendFile,
			},
		},
		{
			name: "unknown target",
			target: providercoord.Target{
				ID: "other", BackendID: switchtarget.BackendFile,
			},
		},
		{
			name: "non-file backend",
			target: providercoord.Target{
				ID: grokconfig.AuthTargetID, BackendID: "keyring",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := testHome(t)
			testCase.target.Path = filepath.Join(t.TempDir(), "outside")
			_, err := NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
				ProviderID: grokconfig.ProviderID,
				Targets:    []providercoord.Target{testCase.target},
			})
			assertCode(t, err, apperror.GrokBuildInvalid)
			if _, statErr := os.Stat(home.LockPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("lock file was touched: %v", statErr)
			}
		})
	}
}

func TestGuardRejectsUnlinkAndRecreate(t *testing.T) {
	home := testHome(t)
	held, err := NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()
	if err := os.Remove(home.LockPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.WriteFile(home.LockPath, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err = held.Validate(context.Background())
	assertCode(t, err, apperror.TargetChanged)
}

func TestGuardFailsBeforeGrokStaleThreshold(t *testing.T) {
	home := testHome(t)
	now := time.Unix(1_800_000_000, 0)
	coordinator := NewCoordinator(home)
	coordinator.now = func() time.Time { return now }
	held, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()
	now = now.Add(defaultMaxHold)
	err = held.Validate(context.Background())
	assertCode(t, err, apperror.TargetChanged)
}

func TestHolderStalenessRules(t *testing.T) {
	home := testHome(t)
	coordinator := NewCoordinator(home)
	now := time.Unix(1_800_000_000, 0)
	coordinator.now = func() time.Time { return now }
	coordinator.processAlive = func(pid int) bool { return pid != 1234 }

	tests := []struct {
		name    string
		holder  string
		modTime time.Time
		stale   bool
	}{
		{name: "dead pid", holder: "1234:1800000000", modTime: now, stale: true},
		{name: "expired holder", holder: "5678:1799999939", modTime: now, stale: true},
		{name: "live holder", holder: "5678:1799999940", modTime: now, stale: false},
		{name: "old unparseable", holder: "garbage", modTime: now.Add(-61 * time.Second), stale: true},
		{name: "fresh unparseable", holder: "garbage", modTime: now, stale: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(home.LockPath, []byte(test.holder), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := os.Chtimes(home.LockPath, test.modTime, test.modTime); err != nil {
				t.Fatalf("Chtimes: %v", err)
			}
			file, err := os.OpenFile(home.LockPath, os.O_RDWR, 0)
			if err != nil {
				t.Fatalf("OpenFile: %v", err)
			}
			got := coordinator.holderIsStale(file)
			_ = file.Close()
			if got != test.stale {
				t.Fatalf("holderIsStale = %t, want %t", got, test.stale)
			}
		})
	}
}

func TestCoordinatorCrossProcessContentionAndCrashRelease(t *testing.T) {
	home := testHome(t)
	child := startLockHelper(t, home, "normal")
	coordinator := NewCoordinator(home)
	coordinator.acquireTimeout = 150 * time.Millisecond
	coordinator.pollInterval = 5 * time.Millisecond
	_, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	assertCode(t, err, apperror.LockAcquireFailed)

	child.killAndWait(t)
	held, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire after crash: %v", err)
	}
	held.Release()
}

func TestCoordinatorBreaksStaleCrossProcessHolder(t *testing.T) {
	home := testHome(t)
	child := startLockHelper(t, home, "stale")
	defer child.killAndWait(t)
	before, err := os.Stat(home.LockPath)
	if err != nil {
		t.Fatalf("Stat held lock: %v", err)
	}
	coordinator := NewCoordinator(home)
	coordinator.acquireTimeout = 100 * time.Millisecond
	coordinator.pollInterval = 5 * time.Millisecond
	coordinator.now = func() time.Time {
		return time.Now().Add(defaultStaleAfter + time.Second)
	}
	held, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if runtime.GOOS == "windows" {
		assertCode(t, err, apperror.LockAcquireFailed)
		after, statErr := os.Stat(home.LockPath)
		if statErr != nil || !os.SameFile(before, after) {
			t.Fatalf("Windows stale contention changed the canonical lock: before=%#v after=%#v err=%v", before, after, statErr)
		}
		child.killAndWait(t)
		reacquired, reacquireErr := coordinator.Acquire(context.Background(), providercoord.Request{
			ProviderID: grokconfig.ProviderID,
		})
		if reacquireErr != nil {
			t.Fatalf("Acquire after stale holder crash: %v", reacquireErr)
		}
		reacquired.Release()
		return
	}
	if err != nil {
		t.Fatalf("Acquire after stale unlink: %v", err)
	}
	held.Release()
}

func TestInstalledGrokBinaryInteroperability(t *testing.T) {
	if os.Getenv("PROFILEDECK_TEST_GROK_BINARY") != "1" {
		t.Skip("set PROFILEDECK_TEST_GROK_BINARY=1 to test the installed Grok binary")
	}
	binary, err := exec.LookPath("grok")
	if err != nil {
		t.Fatal("installed Grok binary is unavailable")
	}
	home := testHome(t)
	if err := os.WriteFile(
		home.ConfigPath,
		[]byte("[ui]\nscreen_mode = \"minimal\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	const authFixture = `{
  "https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828": {
    "key": "synthetic-access-token",
    "auth_mode": "oidc",
    "create_time": "2026-07-29T00:00:00Z",
    "user_id": "synthetic-user",
    "email": null,
    "refresh_token": "synthetic-refresh-token"
  }
}
`
	writeAuth := func() {
		t.Helper()
		if err := os.WriteFile(home.AuthPath, []byte(authFixture), 0o600); err != nil {
			t.Fatalf("WriteFile auth: %v", err)
		}
	}
	runGrok := func(args ...string) {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Env = grokBinaryTestEnvironment(home.Dir)
		if err := command.Run(); err != nil {
			t.Fatalf("installed Grok command failed: %v", err)
		}
	}

	if expected := strings.TrimSpace(os.Getenv("PROFILEDECK_EXPECTED_GROK_VERSION")); expected != "" {
		command := exec.Command(binary, "--version")
		command.Env = grokBinaryTestEnvironment(home.Dir)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("installed Grok version check failed: %v", err)
		}
		if actual := strings.TrimSpace(string(output)); actual != expected {
			t.Fatalf("installed Grok version = %q, want %q", actual, expected)
		}
	}

	// Prove the binary resolves config.toml from the isolated GROK_HOME through
	// its production config inspection path. Captured output is never logged.
	inspect := exec.Command(binary, "--cwd", home.Dir, "inspect", "--json")
	inspect.Env = grokBinaryTestEnvironment(home.Dir)
	output, err := inspect.Output()
	if err != nil {
		t.Fatalf("installed Grok config inspection failed: %v", err)
	}
	var report struct {
		ConfigSources struct {
			Layers []struct {
				Role string `json:"role"`
				Path string `json:"path"`
				Note string `json:"note"`
			} `json:"layers"`
		} `json:"configSources"`
	}
	if json.Unmarshal(output, &report) != nil {
		t.Fatal("installed Grok config inspection returned invalid JSON")
	}
	configRead := false
	for _, layer := range report.ConfigSources.Layers {
		if layer.Role == "user" {
			configRead = filepath.Clean(layer.Path) == home.ConfigPath && layer.Note == ""
			break
		}
	}
	if !configRead {
		t.Fatal("installed Grok binary did not load config.toml from the isolated GROK_HOME")
	}

	// Prove the binary resolves auth.json from the isolated GROK_HOME.
	writeAuth()
	runGrok("logout")
	if raw, err := os.ReadFile(home.AuthPath); err == nil && string(raw) == authFixture {
		t.Fatal("installed Grok binary did not read the isolated GROK_HOME")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile after baseline logout: %v", err)
	}

	// ProfileDeck's guard must make Grok skip its non-blocking logout write.
	writeAuth()
	held, err := NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	runGrok("logout")
	if err := held.Validate(context.Background()); err != nil {
		held.Release()
		t.Fatalf("Validate: %v", err)
	}
	held.Release()
	raw, err := os.ReadFile(home.AuthPath)
	if err != nil {
		t.Fatalf("ReadFile after contended logout: %v", err)
	}
	if string(raw) != authFixture {
		t.Fatal("installed Grok binary changed auth.json while ProfileDeck held the guard")
	}
}

func grokBinaryTestEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GROK_") ||
			key == "XAI_API_KEY" ||
			key == "GROK_CODE_XAI_API_KEY" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GROK_HOME="+home)
}

func TestCoordinatorProcessHelper(t *testing.T) {
	if os.Getenv("PROFILEDECK_GROK_LOCK_HELPER") != "1" {
		return
	}
	home, err := grokconfig.ResolveHome(os.Getenv("PROFILEDECK_GROK_LOCK_HOME"))
	if err != nil {
		t.Fatalf("ResolveHome: %v", err)
	}
	held, err := NewCoordinator(home).Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()
	if os.Getenv("PROFILEDECK_GROK_LOCK_MODE") == "stale" {
		concrete, ok := held.(*guard)
		if !ok {
			t.Fatalf("guard type = %T", held)
		}
		concrete.mu.Lock()
		concrete.token = "2147483647:1"
		err := writeHolder(concrete.file, concrete.token)
		concrete.mu.Unlock()
		if err != nil {
			t.Fatalf("write stale holder: %v", err)
		}
	}
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type lockHelper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
}

func startLockHelper(t *testing.T, home grokconfig.Home, mode string) lockHelper {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCoordinatorProcessHelper$")
	command.Env = append(os.Environ(),
		"PROFILEDECK_GROK_LOCK_HELPER=1",
		"PROFILEDECK_GROK_LOCK_HOME="+home.Dir,
		"PROFILEDECK_GROK_LOCK_MODE="+mode,
		"GROK_AUTH=",
		"GROK_AUTH_PATH=",
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("helper readiness = %q, %v", line, err)
	}
	return lockHelper{command: command, stdin: stdin}
}

func (helper lockHelper) killAndWait(t *testing.T) {
	t.Helper()
	if helper.stdin != nil {
		_ = helper.stdin.Close()
	}
	if helper.command == nil || helper.command.Process == nil {
		return
	}
	if err := helper.command.Process.Kill(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "finished") {
		t.Fatalf("Kill helper: %v", err)
	}
	_ = helper.command.Wait()
}

func testHome(t *testing.T) grokconfig.Home {
	t.Helper()
	dir := t.TempDir()
	return grokconfig.Home{
		Dir:        dir,
		ConfigPath: filepath.Join(dir, grokconfig.ConfigFileName),
		AuthPath:   filepath.Join(dir, grokconfig.AuthFileName),
		LockPath:   filepath.Join(dir, grokconfig.LockFileName),
	}
}

func assertCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}
