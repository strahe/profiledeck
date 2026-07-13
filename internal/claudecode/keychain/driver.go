package keychain

import "errors"

var (
	ErrNotFound            = errors.New("keychain item not found")
	ErrInteractionRequired = errors.New("keychain user interaction required")
	ErrUnavailable         = errors.New("keychain driver unavailable")
)

type Reference struct {
	Persistent []byte
	Service    string
	Account    string
}

type Item struct {
	Service string
	Account string
	Data    []byte
}

// Driver intentionally excludes add and delete operations. Claude Code owns
// the lifecycle of its Keychain item; ProfileDeck only updates an exact item.
type Driver interface {
	Find(service, account string, allowInteraction bool) ([]Reference, error)
	Read(persistent []byte, allowInteraction bool) (Item, error)
	Update(persistent, data []byte) error
}

func New() Driver {
	return newDriver()
}
