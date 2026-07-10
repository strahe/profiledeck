package backend

import "sync"

const (
	DesktopChangeInitialized           = "initialized"
	DesktopChangeCodexProfileChanged   = "codex-profile-changed"
	DesktopChangeCodexConfigSetChanged = "codex-config-set-changed"
	DesktopChangeSwitchApplied         = "switch-applied"
	DesktopChangeLockRepaired          = "lock-repaired"
	DesktopChangeRollbackApplied       = "rollback-applied"
	DesktopChangeSwitchRecovered       = "switch-recovered"

	DesktopChangeStatusSuccess  = "success"
	DesktopChangeStatusFailure  = "failure"
	DesktopChangeStatusCanceled = "canceled"
)

type DesktopChangeEvent struct {
	Kind               string        `json:"kind"`
	Source             string        `json:"source,omitempty"`
	Status             string        `json:"status,omitempty"`
	Error              *DesktopError `json:"error,omitempty"`
	ProviderID         string        `json:"provider_id,omitempty"`
	ProfileID          string        `json:"profile_id,omitempty"`
	OperationID        string        `json:"operation_id,omitempty"`
	ProfileChanged     bool          `json:"profile_changed,omitempty"`
	ConfigSetsChanged  bool          `json:"config_sets_changed,omitempty"`
	ActiveStateChanged bool          `json:"active_state_changed,omitempty"`
}

type DashboardUpdatePayload struct {
	Event     DesktopChangeEvent `json:"event"`
	Dashboard DashboardResult    `json:"dashboard"`
	Error     *DesktopError      `json:"error,omitempty"`
}

type ChangeNotifier struct {
	mu        sync.RWMutex
	nextID    int
	listeners map[int]func(DesktopChangeEvent)
}

func NewChangeNotifier() *ChangeNotifier {
	return &ChangeNotifier{listeners: map[int]func(DesktopChangeEvent){}}
}

func (n *ChangeNotifier) Subscribe(listener func(DesktopChangeEvent)) func() {
	if n == nil || listener == nil {
		return func() {}
	}
	n.mu.Lock()
	id := n.nextID
	n.nextID++
	n.listeners[id] = listener
	n.mu.Unlock()

	return func() {
		n.mu.Lock()
		delete(n.listeners, id)
		n.mu.Unlock()
	}
}

func (n *ChangeNotifier) Notify(event DesktopChangeEvent) {
	if n == nil {
		return
	}
	n.mu.RLock()
	listeners := make([]func(DesktopChangeEvent), 0, len(n.listeners))
	for _, listener := range n.listeners {
		listeners = append(listeners, listener)
	}
	n.mu.RUnlock()

	for _, listener := range listeners {
		listener(event)
	}
}
