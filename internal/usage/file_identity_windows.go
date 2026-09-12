//go:build windows

package usage

import (
	"crypto/sha256"
	"encoding/binary"
	"os"

	"golang.org/x/sys/windows"

	"github.com/strahe/profiledeck/internal/store"
)

func sourceFileIdentityDigest(path string, _ os.FileInfo) store.UsageKey {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return store.UsageKey{}
	}
	handle, err := windows.CreateFile(
		name,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return store.UsageKey{}
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return store.UsageKey{}
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-usage-file-identity-v1\x00"))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(info.VolumeSerialNumber))
	_, _ = hash.Write(encoded[:])
	fileIndex := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	binary.BigEndian.PutUint64(encoded[:], fileIndex)
	_, _ = hash.Write(encoded[:])
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}
