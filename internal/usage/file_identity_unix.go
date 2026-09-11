//go:build darwin || linux

package usage

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"syscall"

	"github.com/strahe/profiledeck/internal/store"
)

func sourceFileIdentityDigest(_ string, info os.FileInfo) store.UsageKey {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return store.UsageKey{}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-usage-file-identity-v1\x00"))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(stat.Dev))
	_, _ = hash.Write(encoded[:])
	binary.BigEndian.PutUint64(encoded[:], uint64(stat.Ino))
	_, _ = hash.Write(encoded[:])
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}
