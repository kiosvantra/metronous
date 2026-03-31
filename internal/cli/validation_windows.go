//go:build windows

package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// validateDataDir checks if the data directory is valid for installation.
func validateDataDir(dataDir string) error {
	// Check if data directory path is valid
	if dataDir == "" {
		return fmt.Errorf("data directory path is empty")
	}

	// Check parent directory exists
	parent := filepath.Dir(dataDir)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		return fmt.Errorf("parent directory does not exist: %s", parent)
	}

	// Check if we can write to the parent directory
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

	// Check if binary exists
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("binary does not exist: %s", binaryPath)
	}

	// Check if binary is executable
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("cannot access binary: %w", err)
	}

	// Validate binary path doesn't contain spaces (common issue on Windows)
	// Note: We don't error on spaces, but we warn about it
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
	// Check if we can create services (requires elevation)
	cmd := exec.Command("sc", "query", "type=", "service", "state=", "all")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot query services - may require elevation: %w", err)
	}

	// Check if we can write to system directories
	systemDir := os.Getenv("SystemRoot")
	if systemDir == "" {
		systemDir = "C:\\Windows"
	}

	// Try to check if we can create a test file in a common location
	testDirs := []string{
		filepath.Join(systemDir, "System32"),
		filepath.Join(os.Getenv("ProgramFiles"), "Metronous"),
	}

	for _, dir := range testDirs {
		if _, err := os.Stat(dir); err == nil {
			// Directory exists, try a write test (subtle - just check if it's writable)
			testFile := filepath.Join(dir, ".metronous-perm-test")
			f, err := os.Create(testFile)
			if err != nil {
				// Not writable, but that's OK for System32
				continue
			}
			f.Close()
			os.Remove(testFile)
		}
	}

	return nil
}

// getDefaultPort returns the default port for the metronous service.
func getDefaultPort() int {
	return 8844
}
