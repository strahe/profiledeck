//go:build !darwin || !cgo

package keychain

type unavailableDriver struct{}

func newDriver() Driver { return unavailableDriver{} }

func (unavailableDriver) Find(string, string, bool) ([]Reference, error) { return nil, ErrUnavailable }
func (unavailableDriver) Read([]byte, bool) (Item, error)                { return Item{}, ErrUnavailable }
func (unavailableDriver) Update([]byte, []byte) error                    { return ErrUnavailable }
