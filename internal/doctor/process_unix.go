//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package doctor

import (
	"errors"
	"syscall"
)

func inspectProcess(pid int) string {
	if pid <= 0 {
		return doctorPIDStateUnknown
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return doctorPIDStateAlive
	}
	if errors.Is(err, syscall.ESRCH) {
		return doctorPIDStateDead
	}
	return doctorPIDStateUnknown
}
