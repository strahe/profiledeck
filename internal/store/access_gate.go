package store

import (
	"context"
	"sync"
)

// AccessGate keeps database connections opened by one process away from a
// database-file replacement performed by that same process.
type AccessGate struct {
	mu sync.RWMutex
}

type exclusiveAccessContextKey struct{}

func newAccessGate() *AccessGate {
	return &AccessGate{}
}

// RunExclusive waits for every Store opened through the gate to close, blocks
// new opens, and gives the callback a context that may open Stores itself.
func (gate *AccessGate) RunExclusive(ctx context.Context, run func(context.Context) error) error {
	if gate == nil {
		return run(ctx)
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return run(context.WithValue(ctx, exclusiveAccessContextKey{}, gate))
}

func (gate *AccessGate) acquireShared(ctx context.Context) *accessLease {
	if gate == nil || ctx.Value(exclusiveAccessContextKey{}) == gate {
		return nil
	}
	gate.mu.RLock()
	return &accessLease{release: gate.mu.RUnlock}
}

type accessLease struct {
	once    sync.Once
	release func()
}

func (lease *accessLease) close() {
	if lease == nil {
		return
	}
	lease.once.Do(lease.release)
}
