//go:build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

func postUpdateInstallMigration(binaryPath string) error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	if err := patchOpencodeJSON(userHome, binaryPath); err != nil {
		return fmt.Errorf("update opencode config: %w", err)
	}

	systemdDir := filepath.Join(userHome, ".config", "systemd", "user")
	unitPath := filepath.Join(systemdDir, "metronous.service")
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return nil
	}
	dataDir := defaultDataDir()
	if parsed, err := extractUnitDataDir(unitPath); err == nil && parsed != "" {
		dataDir = parsed
	}
	unitContent, err := generateUnitFile(binaryPath, dataDir)
	if err != nil {
		return fmt.Errorf("generate migrated unit: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
		return fmt.Errorf("write migrated unit: %w", err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("restart", "metronous"); err != nil {
		return err
	}
	return nil
}

func extractUnitDataDir(unitPath string) (string, error) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`--data-dir ('([^']+)'|"([^"]+)"|([^\s]+))`)
	m := pattern.FindStringSubmatch(string(data))
	if len(m) == 0 {
		return "", nil
	}
	for _, idx := range []int{2, 3, 4} {
		if idx < len(m) && m[idx] != "" {
			return m[idx], nil
		}
	}
	return "", nil
}
