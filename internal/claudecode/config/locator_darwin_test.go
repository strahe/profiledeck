//go:build darwin

package config

import "testing"

func TestResolveLocatorUsesOfficialKeychainSelector(t *testing.T) {
	locator, err := ResolveLocator()
	if err != nil {
		t.Fatal(err)
	}
	if locator.Storage != StorageKeychain || locator.Service != KeychainService || locator.Account == "" || locator.Path != "" {
		t.Fatalf("ResolveLocator() = %#v", locator)
	}
}
