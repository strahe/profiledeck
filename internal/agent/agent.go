// Package agent defines compile-time Agent ownership and Desktop access policy.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

const desktopSettingPrefix = "desktop.agent."

type ID string

const (
	Codex       ID = "codex"
	Antigravity ID = "antigravity"
	ClaudeCode  ID = "claude-code"
)

type Manifest struct {
	ID                    ID       `json:"id"`
	DisplayName           string   `json:"display_name"`
	ProviderIDs           []string `json:"provider_ids"`
	DefaultDesktopEnabled bool     `json:"default_desktop_enabled"`
}

type AccessMode string

const (
	AccessUnrestricted       AccessMode = "unrestricted"
	AccessDesktopPreferences AccessMode = "desktop-preferences"
)

type Policy interface {
	RequireAgent(context.Context, ID) error
	RequireProvider(context.Context, string) error
}

type State struct {
	Manifest Manifest `json:"manifest"`
	Enabled  bool     `json:"enabled"`
}

type StateEvent struct {
	ID      ID   `json:"id"`
	Enabled bool `json:"enabled"`
}

// Registry is immutable after construction. Returned manifests are copies so
// callers cannot mutate Provider ownership through shared slices.
type Registry struct {
	manifests  []Manifest
	byID       map[ID]Manifest
	byProvider map[string]ID
}

func NewRegistry(manifests ...Manifest) (Registry, error) {
	registry := Registry{
		manifests:  make([]Manifest, 0, len(manifests)),
		byID:       make(map[ID]Manifest, len(manifests)),
		byProvider: make(map[string]ID),
	}
	for _, input := range manifests {
		manifest := cloneManifest(input)
		manifest.ID = ID(strings.TrimSpace(string(manifest.ID)))
		manifest.DisplayName = strings.TrimSpace(manifest.DisplayName)
		if !validID(string(manifest.ID)) {
			return Registry{}, fmt.Errorf("agent id %q is invalid", manifest.ID)
		}
		if manifest.DisplayName == "" {
			return Registry{}, fmt.Errorf("agent %q display name is required", manifest.ID)
		}
		if _, exists := registry.byID[manifest.ID]; exists {
			return Registry{}, fmt.Errorf("agent id %q is duplicated", manifest.ID)
		}
		seenProviders := make(map[string]struct{}, len(manifest.ProviderIDs))
		for index, rawProviderID := range manifest.ProviderIDs {
			providerID := strings.TrimSpace(rawProviderID)
			if !validID(providerID) {
				return Registry{}, fmt.Errorf("agent %q provider id %q is invalid", manifest.ID, rawProviderID)
			}
			if _, exists := seenProviders[providerID]; exists {
				return Registry{}, fmt.Errorf("agent %q provider id %q is duplicated", manifest.ID, providerID)
			}
			if owner, exists := registry.byProvider[providerID]; exists {
				return Registry{}, fmt.Errorf("provider %q is owned by both %q and %q", providerID, owner, manifest.ID)
			}
			seenProviders[providerID] = struct{}{}
			registry.byProvider[providerID] = manifest.ID
			manifest.ProviderIDs[index] = providerID
		}
		registry.manifests = append(registry.manifests, manifest)
		registry.byID[manifest.ID] = manifest
	}
	return registry, nil
}

func MustRegistry(manifests ...Manifest) Registry {
	registry, err := NewRegistry(manifests...)
	if err != nil {
		panic(err)
	}
	return registry
}

func BuiltinRegistry() Registry {
	return MustRegistry(
		Manifest{ID: Codex, DisplayName: "Codex", ProviderIDs: []string{"codex"}, DefaultDesktopEnabled: true},
		Manifest{ID: Antigravity, DisplayName: "Antigravity", ProviderIDs: []string{"antigravity"}, DefaultDesktopEnabled: true},
		Manifest{ID: ClaudeCode, DisplayName: "Claude Code", ProviderIDs: []string{"claude-code"}, DefaultDesktopEnabled: true},
	)
}

func (registry Registry) Manifests() []Manifest {
	result := make([]Manifest, len(registry.manifests))
	for index, manifest := range registry.manifests {
		result[index] = cloneManifest(manifest)
	}
	return result
}

func (registry Registry) Manifest(id ID) (Manifest, bool) {
	manifest, ok := registry.byID[id]
	return cloneManifest(manifest), ok
}

func (registry Registry) AgentForProvider(providerID string) (ID, bool) {
	id, ok := registry.byProvider[strings.TrimSpace(providerID)]
	return id, ok
}

type Service struct {
	registry Registry
	stores   store.Factory
	mode     AccessMode

	listenersMu  sync.RWMutex
	listeners    map[uint64]func(StateEvent)
	nextListener uint64
}

func NewService(registry Registry, stores store.Factory, mode AccessMode) *Service {
	return &Service{registry: registry, stores: stores, mode: mode, listeners: make(map[uint64]func(StateEvent))}
}

func (service *Service) Registry() Registry {
	return service.registry
}

func (service *Service) List(ctx context.Context) ([]State, error) {
	states := service.defaultStates()
	if service.mode == AccessUnrestricted {
		return states, nil
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	settings, err := db.ListSettingsByPrefix(ctx, desktopSettingPrefix)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to load Desktop Agent preferences", err)
	}
	byID := make(map[ID]int, len(states))
	for index, state := range states {
		byID[state.Manifest.ID] = index
	}
	for _, setting := range settings {
		id, ok := agentIDFromSettingKey(setting.Key)
		if !ok {
			continue
		}
		index, known := byID[id]
		if !known {
			continue
		}
		var enabled bool
		if err := json.Unmarshal([]byte(setting.ValueJSON), &enabled); err != nil {
			return nil, apperror.Wrap(apperror.SettingInvalid, "Desktop Agent preference is invalid", err).WithDetail("agent_id", id)
		}
		states[index].Enabled = enabled
	}
	return states, nil
}

func (service *Service) SetEnabled(ctx context.Context, id ID, enabled bool) (State, error) {
	manifest, ok := service.registry.Manifest(id)
	if !ok {
		return State{}, apperror.New(apperror.SettingInvalid, "Agent is not registered").WithDetail("agent_id", id)
	}
	if service.mode != AccessDesktopPreferences {
		return State{}, apperror.New(apperror.SettingInvalid, "Agent preferences are available only to Desktop")
	}
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return State{}, err
	}
	defer db.Close()
	raw, _ := json.Marshal(enabled)
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: settingKey(id), ValueJSON: string(raw)}); err != nil {
		return State{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to save Desktop Agent preference", err).WithDetail("agent_id", id)
	}
	state := State{Manifest: manifest, Enabled: enabled}
	service.notify(StateEvent{ID: id, Enabled: enabled})
	return state, nil
}

func (service *Service) RequireAgent(ctx context.Context, id ID) error {
	if _, ok := service.registry.Manifest(id); !ok {
		return apperror.New(apperror.SettingInvalid, "Agent is not registered").WithDetail("agent_id", id)
	}
	if service.mode == AccessUnrestricted {
		return nil
	}
	states, err := service.List(ctx)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.Manifest.ID == id {
			if state.Enabled {
				return nil
			}
			return apperror.New(apperror.AgentDisabled, "Agent is disabled").WithDetail("agent_id", id)
		}
	}
	return apperror.New(apperror.SettingInvalid, "Agent is not registered").WithDetail("agent_id", id)
}

func (service *Service) RequireProvider(ctx context.Context, providerID string) error {
	id, managed := service.registry.AgentForProvider(providerID)
	if !managed {
		return nil
	}
	return service.RequireAgent(ctx, id)
}

func (service *Service) Subscribe(listener func(StateEvent)) func() {
	if listener == nil {
		return func() {}
	}
	service.listenersMu.Lock()
	service.nextListener++
	id := service.nextListener
	service.listeners[id] = listener
	service.listenersMu.Unlock()
	return func() {
		service.listenersMu.Lock()
		delete(service.listeners, id)
		service.listenersMu.Unlock()
	}
}

func (service *Service) defaultStates() []State {
	manifests := service.registry.Manifests()
	states := make([]State, 0, len(manifests))
	for _, manifest := range manifests {
		states = append(states, State{Manifest: manifest, Enabled: service.mode == AccessUnrestricted || manifest.DefaultDesktopEnabled})
	}
	return states
}

func (service *Service) notify(event StateEvent) {
	service.listenersMu.RLock()
	listeners := make([]func(StateEvent), 0, len(service.listeners))
	for _, listener := range service.listeners {
		listeners = append(listeners, listener)
	}
	service.listenersMu.RUnlock()
	for _, listener := range listeners {
		listener(event)
	}
}

func settingKey(id ID) string {
	return desktopSettingPrefix + string(id) + ".enabled"
}

func agentIDFromSettingKey(key string) (ID, bool) {
	if !strings.HasPrefix(key, desktopSettingPrefix) || !strings.HasSuffix(key, ".enabled") {
		return "", false
	}
	id := ID(strings.TrimSuffix(strings.TrimPrefix(key, desktopSettingPrefix), ".enabled"))
	return id, id != ""
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.ProviderIDs = append([]string(nil), manifest.ProviderIDs...)
	return manifest
}

func validID(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if index > 0 {
			valid = valid || char == '.' || char == '_' || char == '-'
		}
		if !valid {
			return false
		}
	}
	return true
}
