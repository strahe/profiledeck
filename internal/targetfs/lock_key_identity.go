//go:build !darwin && !windows

package targetfs

func normalizeLocalLockKey(path string) string {
	return path
}
