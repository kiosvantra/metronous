//go:build !windows

package mcp

import (
	"errors"
	"os"
	"syscall"
)

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	sigErr := proc.Signal(syscall.Signal(0))
	if sigErr == nil {
		return true
	}
	// EPERM means the process exists but we lack permission to signal it.
	if errors.Is(sigErr, syscall.EPERM) {
		return true
	}
	return false
}
