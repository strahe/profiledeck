//go:build darwin || windows

package targetfs

import "strings"

func normalizeLocalLockKey(path string) string {
	return strings.ToLower(path)
}
