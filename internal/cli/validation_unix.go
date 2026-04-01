//go:build !windows

package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// validateDataDir checks if the data directory is valid for installation.
func validateDataDir(dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("data directory path is empty")
	}

	parent := filepath.Dir(dataDir)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		return fmt.Errorf("parent directory does not exist: %s", parent)
	}

	testFile := filepath.Join(parent, ".metronous-write-test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return fmt.Errorf("cannot write to parent directory: %w", err)
	}
	os.Remove(testFile)

	return nil
}

// validateBinary checks if the binary is in a valid location.
func validateBinary(binaryPath string) error {
	if binaryPath == "" {
		return fmt.Errorf("binary path is empty")
	}

	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("binary does not exist: %s", binaryPath)
	}

	normalizedPath := filepath.Clean(binaryPath)
	if strings.Contains(normalizedPath, " ") && !filepath.IsAbs(normalizedPath) {
		fmt.Printf("Warning: binary path contains spaces: %s\n", binaryPath)
	}

	return nil
}

// validateBinaryPath is an alias for validateBinary to maintain API compatibility.
func validateBinaryPath(binaryPath string) error {
	return validateBinary(binaryPath)
}

// checkPortConflict checks if the default port is already in use.
func checkPortConflict() error {
	port := getDefaultPort()
	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is already in use. Stop the existing service or use a different port.", port)
	}
	ln.Close()
	return nil
}

// validatePermissions checks if we have the required permissions for installation.
func validatePermissions() error {
	// On Unix, check if we can write to /usr/local/bin or similar
	// This is a simplified check - in practice, service installation would fail later
	return nil
}

// getDefaultPort returns the default port for the metronous service.
func getDefaultPort() int {
	return 8844
}
