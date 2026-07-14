package backend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/app"
)

type fakeAgentRuntime struct {
	mu            sync.Mutex
	starts        int
	gracefulStops int
	hardStops     int
}

type blockingAgentRuntime struct {
	startEntered chan struct{}
	releaseStart chan struct{}

	mu      sync.Mutex
	running bool
}

func (runtime *blockingAgentRuntime) startAgentRuntime(context.Context) {
	close(runtime.startEntered)
	<-runtime.releaseStart
	runtime.mu.Lock()
	runtime.running = true
	runtime.mu.Unlock()
}

func (runtime *blockingAgentRuntime) stopAgentRuntime() {
	runtime.mu.Lock()
	runtime.running = false
	runtime.mu.Unlock()
}

func (runtime *blockingAgentRuntime) Stop() {
	runtime.stopAgentRuntime()
}

func (runtime *blockingAgentRuntime) isRunning() bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.running
}

func (runtime *fakeAgentRuntime) startAgentRuntime(context.Context) {
	runtime.mu.Lock()
	runtime.starts++
	runtime.mu.Unlock()
}

func (runtime *fakeAgentRuntime) stopAgentRuntime() {
	runtime.mu.Lock()
	runtime.gracefulStops++
	runtime.mu.Unlock()
}

func (runtime *fakeAgentRuntime) Stop() {
	runtime.mu.Lock()
	runtime.hardStops++
	runtime.mu.Unlock()
}

func (runtime *fakeAgentRuntime) counts() (starts, gracefulStops, hardStops int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.starts, runtime.gracefulStops, runtime.hardStops
}

func TestAgentRuntimeManagerReconcilesDesktopPreferenceChanges(t *testing.T) {
	ctx := context.Background()
	application, err := app.New(app.Config{ConfigDir: t.TempDir(), AgentAccess: agent.AccessDesktopPreferences})
	if err != nil {
		t.Fatalf("create Application: %v", err)
	}
	if _, err := application.Runtime().Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	runtime := &fakeAgentRuntime{}
	manager := newAgentRuntimeManager(application.Agents())
	manager.Register(agent.Codex, runtime)
	manager.Activate(ctx, agent.Codex, runtime)
	if starts, graceful, hard := runtime.counts(); starts != 1 || graceful != 0 || hard != 0 {
		t.Fatalf("unexpected activation counts: start=%d graceful=%d hard=%d", starts, graceful, hard)
	}

	if _, err := application.Agents().SetEnabled(ctx, agent.Codex, false); err != nil {
		t.Fatalf("disable Codex Agent: %v", err)
	}
	if starts, graceful, hard := runtime.counts(); starts != 1 || graceful != 1 || hard != 0 {
		t.Fatalf("unexpected disable counts: start=%d graceful=%d hard=%d", starts, graceful, hard)
	}

	if _, err := application.Agents().SetEnabled(ctx, agent.Codex, true); err != nil {
		t.Fatalf("enable Codex Agent: %v", err)
	}
	if starts, graceful, hard := runtime.counts(); starts != 2 || graceful != 1 || hard != 0 {
		t.Fatalf("unexpected re-enable counts: start=%d graceful=%d hard=%d", starts, graceful, hard)
	}

	manager.Deactivate(agent.Codex, runtime)
	if _, _, hard := runtime.counts(); hard != 1 {
		t.Fatalf("explicit shutdown did not hard-stop runtime: %d", hard)
	}
}

func TestAgentRuntimeManagerSerializesActivationWithDisable(t *testing.T) {
	ctx := context.Background()
	application, err := app.New(app.Config{ConfigDir: t.TempDir(), AgentAccess: agent.AccessDesktopPreferences})
	if err != nil {
		t.Fatalf("create Application: %v", err)
	}
	if _, err := application.Runtime().Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	runtime := &blockingAgentRuntime{startEntered: make(chan struct{}), releaseStart: make(chan struct{})}
	manager := newAgentRuntimeManager(application.Agents())
	manager.Register(agent.Codex, runtime)
	activated := make(chan struct{})
	go func() {
		manager.Activate(ctx, agent.Codex, runtime)
		close(activated)
	}()
	<-runtime.startEntered

	disabled := make(chan error, 1)
	go func() {
		_, err := application.Agents().SetEnabled(ctx, agent.Codex, false)
		disabled <- err
	}()
	select {
	case err := <-disabled:
		t.Fatalf("disable completed before activation was serialized: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(runtime.releaseStart)
	<-activated
	if err := <-disabled; err != nil {
		t.Fatalf("disable Codex Agent: %v", err)
	}
	if runtime.isRunning() {
		t.Fatal("runtime remained active after a concurrent Agent disable")
	}
}
