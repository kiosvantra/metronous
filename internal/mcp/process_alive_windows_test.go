//go:build windows

package mcp

import (
	"os"
	"testing"
)

func TestIsProcessAliveCurrentProcess(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("expected current process to be alive")
	}
}
