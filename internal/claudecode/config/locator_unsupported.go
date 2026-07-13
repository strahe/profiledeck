//go:build !darwin && !linux && !windows

package config

import "errors"

func ResolveLocator() (Locator, error) {
	return Locator{}, errors.New("Claude Code credential switching is unavailable on this platform")
}
