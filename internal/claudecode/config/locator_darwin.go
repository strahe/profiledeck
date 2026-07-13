//go:build darwin

package config

import (
	"errors"
	"os/user"
	"strings"
)

func ResolveLocator() (Locator, error) {
	current, err := user.Current()
	if err != nil {
		return Locator{}, errors.New("failed to resolve the macOS short username")
	}
	account := strings.TrimSpace(current.Username)
	if account == "" {
		return Locator{}, errors.New("macOS short username is empty")
	}
	return Locator{Storage: StorageKeychain, Service: KeychainService, Account: account}, nil
}
