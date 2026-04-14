package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func managedBinaryPath(currentPath string) string {
	if runtime.GOOS == "windows" {
		return currentPath
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return currentPath
	}
	name := filepath.Base(currentPath)
	if name == "" {
		name = "metronous"
		if runtime.GOOS == "windows" {
			name = "metronous.exe"
		}
	}
	return filepath.Join(home, ".local", "bin", name)
}

func installBinaryToManagedPath(srcPath string) (string, bool, error) {
	targetPath := managedBinaryPath(srcPath)
	if targetPath == srcPath {
		return targetPath, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", false, fmt.Errorf("create managed binary dir: %w", err)
	}
	if err := copyFile(srcPath, targetPath); err != nil {
		return "", false, fmt.Errorf("install managed binary: %w", err)
	}
	return targetPath, true, nil
}
