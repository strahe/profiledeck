//go:build windows

package doctor

import (
	"errors"

	"golang.org/x/sys/windows"
)

func inspectProcess(pid int) string {
	if pid <= 0 {
		return doctorPIDStateUnknown
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return doctorPIDStateDead
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return doctorPIDStateAlive
		}
		return doctorPIDStateUnknown
	}
	defer windows.CloseHandle(handle)

	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return doctorPIDStateUnknown
	}
	switch event {
	case uint32(windows.WAIT_TIMEOUT):
		return doctorPIDStateAlive
	case uint32(windows.WAIT_OBJECT_0):
		return doctorPIDStateDead
	default:
		return doctorPIDStateUnknown
	}
}
