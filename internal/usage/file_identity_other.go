//go:build !darwin && !linux && !windows

package usage

import (
	"os"

	"github.com/strahe/profiledeck/internal/store"
)

func sourceFileIdentityDigest(string, os.FileInfo) store.UsageKey {
	return store.UsageKey{}
}
