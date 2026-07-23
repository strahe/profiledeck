package plan

import (
	"context"
	"errors"
)

// ErrStateNotFound is returned by StateReader when optional application state
// does not exist. Adapters must not depend on persistence-specific sentinels.
var ErrStateNotFound = errors.New("plan state not found")

type Provider struct {
	ID           string
	Name         string
	AdapterID    string
	MetadataJSON string
}

type Profile struct {
	ID           string
	Name         string
	Description  string
	MetadataJSON string
}

type Target struct {
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         string
	Format       string
	Strategy     string
	ValueJSON    string
	Enabled      bool
	MetadataJSON string
}

type ActiveState struct {
	ProfileID string
	Revision  int64
}

type Credential struct {
	ID             string
	ProviderID     string
	CredentialKind string
	PayloadJSON    string
	PayloadSHA256  string
	MetadataJSON   string
}

type ConfigSet struct {
	ID            string
	ProviderID    string
	ConfigKind    string
	Name          string
	Description   string
	PayloadText   string
	PayloadSHA256 string
	MetadataJSON  string
}

type CredentialBinding struct {
	ProfileID    string
	ProviderID   string
	SlotID       string
	CredentialID string
}

type ConfigSetBinding struct {
	ProfileID   string
	ProviderID  string
	SlotID      string
	ConfigSetID string
}

type CredentialUpdate struct {
	ID             string
	ProviderID     string
	CredentialKind string
	PayloadJSON    string
	PayloadSHA256  string
	MetadataJSON   string
}

type ConfigSetUpdate struct {
	ID            string
	ProviderID    string
	ConfigKind    string
	Name          string
	Description   string
	PayloadText   string
	PayloadSHA256 string
	MetadataJSON  string
}

// StateReader is the read-only application state contract available while an
// adapter prepares a plan. It exposes no mutation or transaction APIs.
type StateReader interface {
	GetActiveState(context.Context, string) (ActiveState, error)
	ListTargets(context.Context, string, string, bool) ([]Target, error)
	GetCredential(context.Context, string) (Credential, error)
	ListCredentials(context.Context, string) ([]Credential, error)
	GetConfigSet(context.Context, string, string) (ConfigSet, error)
	ListConfigSets(context.Context, string, string) ([]ConfigSet, error)
	GetCredentialBinding(context.Context, string, string, string) (CredentialBinding, error)
	ListCredentialBindings(context.Context, string, string) ([]CredentialBinding, error)
	ListCredentialBindingsByProvider(context.Context, string) ([]CredentialBinding, error)
	ListConfigSetBindings(context.Context, string, string) ([]ConfigSetBinding, error)
	CountCredentialReferences(context.Context, string) (int, error)
}
