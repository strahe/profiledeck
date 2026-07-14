package backend

import (
	"context"
	"sync"

	"github.com/strahe/profiledeck/internal/agent"
)

type agentManagedRuntime interface {
	startAgentRuntime(context.Context)
	stopAgentRuntime()
	Stop()
}

type agentRuntimeEntry struct {
	mu      sync.Mutex
	runtime agentManagedRuntime
	ctx     context.Context
	active  bool
}

// agentRuntimeManager owns only process-scoped Desktop automation. Agent
// preferences never change the compile-time service registry.
type agentRuntimeManager struct {
	policy *agent.Service

	mu       sync.Mutex
	runtimes map[agent.ID][]*agentRuntimeEntry
}

func newAgentRuntimeManager(policy *agent.Service) *agentRuntimeManager {
	manager := &agentRuntimeManager{policy: policy, runtimes: make(map[agent.ID][]*agentRuntimeEntry)}
	policy.Subscribe(manager.reconcile)
	return manager
}

func (manager *agentRuntimeManager) Register(id agent.ID, runtime agentManagedRuntime) {
	if manager == nil || runtime == nil {
		return
	}
	manager.mu.Lock()
	manager.runtimes[id] = append(manager.runtimes[id], &agentRuntimeEntry{runtime: runtime})
	manager.mu.Unlock()
}

func (manager *agentRuntimeManager) Activate(ctx context.Context, id agent.ID, runtime agentManagedRuntime) {
	if manager == nil || runtime == nil {
		return
	}
	manager.mu.Lock()
	entry := manager.entryLocked(id, runtime)
	manager.mu.Unlock()
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.ctx = ctx
	entry.active = true
	if err := manager.policy.RequireAgent(ctx, id); err != nil {
		runtime.Stop()
		return
	}
	runtime.startAgentRuntime(ctx)
}

func (manager *agentRuntimeManager) Deactivate(id agent.ID, runtime agentManagedRuntime) {
	if manager == nil || runtime == nil {
		return
	}
	manager.mu.Lock()
	entry := manager.entryLocked(id, runtime)
	manager.mu.Unlock()
	if entry == nil {
		runtime.Stop()
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.active = false
	entry.ctx = nil
	entry.runtime.Stop()
}

func (manager *agentRuntimeManager) reconcile(event agent.StateEvent) {
	manager.mu.Lock()
	entries := append([]*agentRuntimeEntry(nil), manager.runtimes[event.ID]...)
	manager.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if !entry.active {
			entry.mu.Unlock()
			continue
		}
		if event.Enabled {
			entry.runtime.startAgentRuntime(entry.ctx)
		} else {
			entry.runtime.stopAgentRuntime()
		}
		entry.mu.Unlock()
	}
}

func (manager *agentRuntimeManager) entryLocked(id agent.ID, runtime agentManagedRuntime) *agentRuntimeEntry {
	for _, entry := range manager.runtimes[id] {
		if entry.runtime == runtime {
			return entry
		}
	}
	return nil
}
