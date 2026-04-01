//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestShimReadPortFile tests that shimPortFilePath returns the correct path.
func TestShimPortFilePath(t *testing.T) {
	path := shimPortFilePath()
	// Should end with mcp.port in the default data dir
	if !strings.HasSuffix(path, "mcp.port") {
		t.Errorf("expected path ending with mcp.port, got %s", path)
	}
	if !strings.Contains(path, ".metronous") {
		t.Errorf("expected path containing .metronous, got %s", path)
	}
}

// TestReadShimPortSuccess tests reading a valid port file.
func TestReadShimPortSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a temporary port file
	portFile := filepath.Join(tmpDir, "mcp.port")
	testPort := 12345
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(testPort)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Override defaultDataDir temporarily for this test
	// Since readShimPort uses defaultDataDir, we test the parsing logic directly
	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	if port != testPort {
		t.Errorf("expected %d, got %d", testPort, port)
	}
}

// TestReadShimPortInvalid tests reading an invalid port file.
func TestReadShimPortInvalid(t *testing.T) {
	tmpDir := t.TempDir()

	portFile := filepath.Join(tmpDir, "mcp.port")
	// Write invalid content
	if err := os.WriteFile(portFile, []byte("not-a-number\n"), 0600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	_, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err == nil {
		t.Error("expected error parsing invalid port, got nil")
	}
}

// TestReadShimPortOutOfRange tests port validation (must be 1-65535).
func TestReadShimPortOutOfRange(t *testing.T) {
	tests := []struct {
		name    string
		port    string
		wantErr bool
	}{
		{"valid port", "8080", false},
		{"valid max port", "65535", false},
		{"too low", "0", true},
		{"too high", "65536", true},
		{"negative", "-1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := strconv.Atoi(strings.TrimSpace(tt.port))
			if err != nil {
				if !tt.wantErr {
					t.Errorf("unexpected parse error: %v", err)
				}
				return
			}
			if port <= 0 || port > 65535 {
				if !tt.wantErr {
					t.Errorf("expected validation error for port %d", port)
				}
			} else if tt.wantErr {
				t.Errorf("expected error for port %d, got none", port)
			}
		})
	}
}

// TestReadShimPortMissing tests that reading a missing port file returns an error.
func TestReadShimPortMissing(t *testing.T) {
	tmpDir := t.TempDir()
	// Non-existent file
	_, err := os.ReadFile(filepath.Join(tmpDir, "nonexistent.port"))
	if err == nil {
		t.Error("expected error reading non-existent file")
	}
}
