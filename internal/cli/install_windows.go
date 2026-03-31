//go:build windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	metronous "github.com/kiosvantra/metronous"
	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/daemon"
)

// NewInstallCommand creates the `metronous install` cobra command.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as an experimental Windows service",
		Long: `Install Metronous as an experimental Windows service (requires elevated terminal).

This command:
  1. Initializes ~/.metronous (idempotent)
  2. Registers the Metronous service via Windows Service Control Manager
  3. Starts the service
  4. Patches opencode.json to use this executable for MCP
  5. Installs the OpenCode plugin (metronous.ts)

After running this experimental command, OpenCode on this machine will
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.

Linux remains the only officially supported installer path.

Note: Run this from an elevated terminal (Run as Administrator) using the same Windows user account that runs OpenCode.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall()
		},
	}
}

// runInstall performs all installation steps.
func runInstall() error {
	// Step 1: Initialize ~/.metronous (idempotent).
	home := defaultMetronousHome()
	fmt.Println("Initializing Metronous home directory...")
	if err := runInit(home); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	// Step 2: Determine data directory.
	dataDir := defaultDataDir()

	// Step 3: Determine install paths.
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Step 4: Install the Windows service via kardianos/service.
	fmt.Println("Installing Windows service...")
	svc, err := buildService(dataDir)
	if err != nil {
		return fmt.Errorf("build service: %w", err)
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("install service: %w (try running as Administrator)", err)
	}
	fmt.Printf("ok: service installed (platform: %s)\n", daemon.Platform())

	// Step 5: Start the service.
	fmt.Println("Starting service...")
	if err := svc.Start(); err != nil {
		return combineRollback(fmt.Errorf("start service: %w", err), svc.Uninstall())
	}
	fmt.Println("ok: service started")

	configRoot := resolveOpenCodeRoot(userHome)
	configBackup, err := backupFile(filepath.Join(configRoot, "opencode.json"))
	if err != nil {
		return fmt.Errorf("backup opencode.json: %w", err)
	}
	pluginBackup, err := backupFile(filepath.Join(configRoot, "plugins", "metronous.ts"))
	if err != nil {
		return fmt.Errorf("backup plugin: %w", err)
	}

	rollback := func(cause error) error {
		stopErr := svc.Stop()
		uninstallErr := svc.Uninstall()
		configErr := configBackup.restore(0600)
		pluginErr := pluginBackup.restore(0600)
		return combineRollback(cause, stopErr, uninstallErr, configErr, pluginErr)
	}

	// Step 6: Patch opencode.json.
	if err := patchOpencodeJSON(userHome, execPath); err != nil {
		return rollback(fmt.Errorf("configure opencode mcp: %w", err))
	}

	// Step 7: Install OpenCode plugin.
	if err := installOpenCodePlugin(userHome); err != nil {
		return rollback(fmt.Errorf("install opencode plugin: %w", err))
	}
	fmt.Println("installed: OpenCode plugin")

	fmt.Println("\nExperimental Windows service installed and started.")
	fmt.Printf("Use 'sc query metronous' or '%s service status' to check service health.\n", execPath)
	fmt.Println("OpenCode on this machine is now configured to use the shared daemon via 'metronous mcp'.")
	return nil
}

// patchOpencodeJSON patches opencode.json to use the MCP shim.
// On Windows it checks %APPDATA%\opencode\opencode.json first, then falls
// back to userHome\.config\opencode\opencode.json.
func resolveOpenCodeRoot(userHome string) string {
	appData := os.Getenv("APPDATA")
	if appData != "" {
		appDataRoot := filepath.Join(appData, "opencode")
		if _, err := os.Stat(filepath.Join(appDataRoot, "opencode.json")); err == nil {
			return appDataRoot
		}
	}
	return filepath.Join(userHome, ".config", "opencode")
}

func patchOpencodeJSON(userHome, binaryPath string) error {
	rootDir := resolveOpenCodeRoot(userHome)
	if err := os.MkdirAll(rootDir, 0700); err != nil {
		return fmt.Errorf("create opencode config dir: %w", err)
	}

	configPath := filepath.Join(rootDir, "opencode.json")
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read opencode.json: %w", err)
	}

	var cfg map[string]interface{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse opencode.json: %w", err)
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// Ensure mcp map exists (OpenCode uses "mcp", not "mcpServers").
	mcpServers, _ := cfg["mcp"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}

	// Set or overwrite the metronous entry.
	mcpServers["metronous"] = map[string]interface{}{
		"command": []interface{}{binaryPath, "mcp"},
		"type":    "local",
	}
	cfg["mcp"] = mcpServers

	patched, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode.json: %w", err)
	}
	if err := os.WriteFile(configPath, patched, 0600); err != nil {
		return fmt.Errorf("write opencode.json: %w", err)
	}
	fmt.Printf("patched: %s\n", configPath)
	return nil
}

// installOpenCodePlugin copies the embedded metronous-plugin.ts to the plugins directory.
// On Windows it checks %APPDATA%\opencode\plugins first, then falls back to
// userHome\.config\opencode\plugins.
func installOpenCodePlugin(userHome string) error {
	pluginData := metronous.EmbeddedPlugin()
	if len(pluginData) == 0 {
		return fmt.Errorf("embedded plugin is empty")
	}

	pluginsDir := filepath.Join(resolveOpenCodeRoot(userHome), "plugins")

	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}

	// Copy plugin file
	pluginDst := filepath.Join(pluginsDir, "metronous.ts")
	if err := os.WriteFile(pluginDst, pluginData, 0600); err != nil {
		return fmt.Errorf("write plugin: %w", err)
	}
	return nil
}
