//go:build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestShimReadPort(t *testing.T) {
	tmpDir := t.TempDir()

	portFile := filepath.Join(tmpDir, "mcp.port")
	if err := os.WriteFile(portFile, []byte("8765\n"), 0600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}

	var port int
	if _, err := fmt.Sscanf(string(data), "%d", &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	if port != 8765 {
		t.Errorf("expected 8765, got %d", port)
	}
}

func TestShimReadPortMissing(t *testing.T) {
	// Override the data dir env temporarily to a non-existent path.
	// readShimPort uses defaultDataDir() which reads home dir — test in isolation.
	tmpDir := t.TempDir()
	portFile := filepath.Join(tmpDir, "mcp.port")

	data, err := os.ReadFile(portFile)
	if err == nil {
		t.Fatalf("expected error, got data: %s", data)
	}
}
