//go:build windows

package mcp

import (
	"golang.org/x/sys/windows"
)

const windowsStillActive = 259 // STILL_ACTIVE / STATUS_PENDING

func isProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED means the process exists but we cannot query it.
		if err == windows.ERROR_ACCESS_DENIED {
			return true
		}
		// Any other error (e.g. ERROR_INVALID_PARAMETER) means no such process.
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// Cannot read exit code but handle was opened — treat as alive.
		return true
	}
	return code == windowsStillActive
}
