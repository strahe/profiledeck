package coordination

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/providercoord"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	defaultAcquireTimeout = 10 * time.Second
	defaultStaleAfter     = 60 * time.Second
	defaultMaxHold        = 55 * time.Second
	defaultPollInterval   = 25 * time.Millisecond
)

type Coordinator struct {
	home           grokconfig.Home
	acquireTimeout time.Duration
	staleAfter     time.Duration
	maxHold        time.Duration
	pollInterval   time.Duration
	now            func() time.Time
	processAlive   func(int) bool
}

type guard struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	token      string
	localKey   string
	acquiredAt time.Time
	maxHold    time.Duration
	now        func() time.Time
	released   bool
}

var localGates = struct {
	sync.Mutex
	held map[string]struct{}
}{held: make(map[string]struct{})}

func NewCoordinator(home grokconfig.Home) *Coordinator {
	return &Coordinator{
		home:           home,
		acquireTimeout: defaultAcquireTimeout,
		staleAfter:     defaultStaleAfter,
		maxHold:        defaultMaxHold,
		pollInterval:   defaultPollInterval,
		now:            time.Now,
		processAlive:   processAlive,
	}
}

func (coordinator *Coordinator) Acquire(
	ctx context.Context,
	request providercoord.Request,
) (providercoord.Guard, error) {
	if coordinator == nil {
		return nil, apperror.New(apperror.LockAcquireFailed, "Grok Build file coordination is unavailable")
	}
	if request.ProviderID != grokconfig.ProviderID {
		return nil, apperror.New(apperror.GrokBuildInvalid, "Grok Build coordination received an incompatible Provider")
	}
	if grokconfig.UnsupportedAuthEnvironment() {
		return nil, apperror.New(
			apperror.GrokBuildInvalid,
			"Grok Build uses a custom authentication source; unset GROK_AUTH and GROK_AUTH_PATH before managing profiles",
		)
	}
	if err := coordinator.validateRequest(request); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(coordinator.acquireTimeout)
	localKey := coordinator.home.LockPath
	if err := acquireLocalGate(ctx, localKey, deadline, coordinator.pollInterval); err != nil {
		return nil, err
	}
	releaseLocal := true
	defer func() {
		if releaseLocal {
			releaseLocalGate(localKey)
		}
	}()

	for time.Now().Before(deadline) {
		acquired, retry, err := coordinator.tryAcquire(false)
		if err != nil {
			return nil, err
		}
		if acquired != nil {
			acquired.localKey = localKey
			releaseLocal = false
			return acquired, nil
		}
		if retry {
			continue
		}
		if err := waitForRetry(ctx, deadline, coordinator.pollInterval); err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, err
		}
	}

	// Match Grok's last-resort stale recovery after the bounded wait.
	for range 2 {
		acquired, retry, err := coordinator.tryAcquire(true)
		if err != nil {
			return nil, err
		}
		if acquired != nil {
			acquired.localKey = localKey
			releaseLocal = false
			return acquired, nil
		}
		if !retry {
			break
		}
	}
	return nil, apperror.New(
		apperror.LockAcquireFailed,
		"Grok Build is updating its login; wait for it to finish and try again",
	)
}

func (coordinator *Coordinator) validateRequest(request providercoord.Request) error {
	if strings.TrimSpace(request.ProviderMetadataJSON) != "" {
		metadata, err := grokpreset.DecodeProviderMetadata(request.ProviderMetadataJSON)
		if err != nil || !metadata.Compatible() {
			return apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Provider metadata is invalid")
		}
		if metadata.GrokHome != coordinator.home.Dir ||
			metadata.AuthPath != coordinator.home.AuthPath ||
			metadata.ConfigPath != coordinator.home.ConfigPath {
			return apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Home does not match this application")
		}
	}
	// Recovery metadata is executable file state. Accept only the two managed
	// files at the persisted Home before opening the coordination lock.
	for _, target := range request.Targets {
		if target.BackendID != switchtarget.BackendFile {
			return apperror.New(apperror.GrokBuildInvalid, "Grok Build coordination received an unsupported target backend")
		}
		switch target.ID {
		case grokconfig.AuthTargetID:
			if target.Path != coordinator.home.AuthPath {
				return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth target does not match the managed Home")
			}
		case grokconfig.ConfigTargetID:
			if target.Path != coordinator.home.ConfigPath {
				return apperror.New(apperror.GrokBuildInvalid, "Grok Build config target does not match the managed Home")
			}
		default:
			return apperror.New(apperror.GrokBuildInvalid, "Grok Build coordination received an unsupported target")
		}
	}
	return nil
}

func (coordinator *Coordinator) tryAcquire(allowStale bool) (*guard, bool, error) {
	file, err := openLockFile(coordinator.home.LockPath)
	if err != nil {
		return nil, false, apperror.Wrap(apperror.LockAcquireFailed, "couldn't open the Grok Build login lock", err)
	}
	locked, err := tryExclusiveLock(file)
	if err != nil {
		_ = file.Close()
		return nil, false, apperror.Wrap(apperror.LockAcquireFailed, "couldn't acquire the Grok Build login lock", err)
	}
	if !locked {
		retry := false
		if allowStale {
			retry = retireStaleLock(coordinator, file)
		}
		_ = file.Close()
		return nil, retry, nil
	}

	token := fmt.Sprintf("%d:%d", os.Getpid(), coordinator.now().Unix())
	if err := writeHolder(file, token); err != nil {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, false, apperror.Wrap(apperror.LockAcquireFailed, "couldn't record Grok Build login lock ownership", err)
	}
	if err := validateLiveFile(coordinator.home.LockPath, file); err != nil {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, true, nil
	}
	return &guard{
		file: file, path: coordinator.home.LockPath, token: token,
		acquiredAt: coordinator.now(), maxHold: coordinator.maxHold, now: coordinator.now,
	}, false, nil
}

func (coordinator *Coordinator) holderIsStale(file *os.File) bool {
	if _, err := file.Seek(0, 0); err != nil {
		return coordinator.unidentifiedHolderIsStale(file)
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return coordinator.unidentifiedHolderIsStale(file)
	}
	parts := strings.Split(strings.TrimSpace(string(raw)), ":")
	if len(parts) != 2 {
		return coordinator.unidentifiedHolderIsStale(file)
	}
	pid, pidErr := strconv.Atoi(parts[0])
	timestamp, timestampErr := strconv.ParseInt(parts[1], 10, 64)
	if pidErr != nil || timestampErr != nil || pid <= 0 || timestamp < 0 {
		return coordinator.unidentifiedHolderIsStale(file)
	}
	if !coordinator.processAlive(pid) {
		return true
	}
	return coordinator.now().Sub(time.Unix(timestamp, 0)) > coordinator.staleAfter
}

func (coordinator *Coordinator) unidentifiedHolderIsStale(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return coordinator.now().Sub(info.ModTime()) > coordinator.staleAfter
}

func (guard *guard) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.released || guard.file == nil {
		return apperror.New(apperror.TargetChanged, "Grok Build login lock is no longer held")
	}
	// Grok may unlink holders older than 60 seconds. Fail before that boundary
	// so a live ProfileDeck operation cannot become a stale holder.
	if guard.now().Sub(guard.acquiredAt) >= guard.maxHold {
		return apperror.New(apperror.TargetChanged, "Grok Build login coordination took too long; no further changes were made")
	}
	if err := validateLiveFile(guard.path, guard.file); err != nil {
		return apperror.Wrap(apperror.TargetChanged, "Grok Build login lock changed; no further changes were made", err)
	}
	if err := validateHolder(guard.file, guard.token); err != nil {
		return apperror.Wrap(apperror.TargetChanged, "Grok Build login lock ownership changed; no further changes were made", err)
	}
	return nil
}

func (guard *guard) Release() {
	guard.mu.Lock()
	if guard.released {
		guard.mu.Unlock()
		return
	}
	guard.released = true
	file := guard.file
	guard.file = nil
	localKey := guard.localKey
	guard.localKey = ""
	guard.mu.Unlock()
	if file != nil {
		_ = unlockFile(file)
		_ = file.Close()
	}
	releaseLocalGate(localKey)
}

func acquireLocalGate(ctx context.Context, key string, deadline time.Time, poll time.Duration) error {
	for {
		localGates.Lock()
		_, held := localGates.held[key]
		if !held {
			localGates.held[key] = struct{}{}
			localGates.Unlock()
			return nil
		}
		localGates.Unlock()
		if err := waitForRetry(ctx, deadline, poll); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return apperror.New(
					apperror.LockAcquireFailed,
					"Grok Build profile work is already in progress; wait for it to finish and try again",
				)
			}
			return err
		}
	}
}

func releaseLocalGate(key string) {
	if key == "" {
		return
	}
	localGates.Lock()
	delete(localGates.held, key)
	localGates.Unlock()
}

func waitForRetry(ctx context.Context, deadline time.Time, poll time.Duration) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.DeadlineExceeded
	}
	if poll > remaining {
		poll = remaining
	}
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeHolder(file *os.File, token string) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := io.WriteString(file, token); err != nil {
		return err
	}
	return file.Sync()
}

func validateHolder(file *os.File, token string) error {
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(len(token)+1)))
	if err != nil {
		return err
	}
	if string(raw) != token {
		return errors.New("lock holder changed")
	}
	return nil
}

var (
	_ providercoord.Coordinator = (*Coordinator)(nil)
	_ providercoord.Guard       = (*guard)(nil)
)
