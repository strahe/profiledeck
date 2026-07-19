//go:build windows

package targetfs

// SyncDirectory is a no-op because Windows does not expose the directory fsync
// primitive used by the POSIX target replacement contract.
func SyncDirectory(string) error {
	return nil
}
