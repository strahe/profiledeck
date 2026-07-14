//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package doctor

func inspectProcess(pid int) string {
	if pid <= 0 {
		return doctorPIDStateUnknown
	}
	return doctorPIDStateUnavailable
}
