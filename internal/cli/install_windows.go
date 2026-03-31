//go:build windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"
	metronous "github.com/kiosvantra/metronous"
	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/daemon"
)

var forceInstall bool

// NewInstallCommand creates the `metronous install` cobra command.
func NewInstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as an experimental Windows service",
		Long: `Install Metronous as an experimental Windows service (requires elevated terminal).

This command:
  1. Initializes ~/.metronous (idempotent)
  2. Validates installation prerequisites
  3. Registers the Metronous service via Windows Service Control Manager
  4. Starts the service
  5. Patches opencode.json to use this executable for MCP
  6. Installs the OpenCode plugin (metronous.ts)

Use --force to reinstall if the service already exists (uninstalls first).

After running this experimental command, OpenCode on this machine will
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.

Linux remains the only officially supported installer path.

Note: Run this from an elevated terminal (Run as Administrator) using the same Windows user account that runs OpenCode.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(forceInstall)
		},
	}
	cmd.Flags().BoolVar(&forceInstall, "force", false, "Force reinstall if service already exists")
	return cmd
}

// runInstall performs all installation steps.
func runInstall(force bool) error {
	// Step 0: Validate prerequisites before any changes.
	dataDir := defaultDataDir()
	if err := validateDataDir(dataDir); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	if err := validatePermissions(); err != nil {
		return fmt.Errorf("permission validation failed: %w", err)
	}

	// Step 1: Handle --force reinstall if service already exists.
	if force {
		if err := handleForceReinstall(dataDir); err != nil {
			return fmt.Errorf("force reinstall failed: %w", err)
		}
	}

	// Step 2: Initialize ~/.metronous (idempotent).
	home := defaultMetronousHome()
	fmt.Println("Initializing Metronous home directory...")
	if err := runInit(home); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	// Step 3: Determine data directory.
	dataDir = defaultDataDir()

	// Step 4: Determine install paths.
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Step 5: Validate binary location.
	if err := validateBinary(execPath); err != nil {
		return fmt.Errorf("binary validation failed: %w", err)
	}

	// Step 6: Check for port conflicts.
	if err := checkPortConflict(); err != nil {
		return fmt.Errorf("port conflict detected: %w", err)
	}

	// Step 7: Install the Windows service via kardianos/service.
	fmt.Println("Installing Windows service...")
	svc, err := buildService(dataDir)
	if err != nil {
		return fmt.Errorf("build service: %w", err)
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("install service: %w (try running as Administrator)", err)
	}
	fmt.Printf("ok: service installed (platform: %s)\n", daemon.Platform())

	// Step 8: Start the service.
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

	// Step 9: Patch opencode.json.
	if err := patchOpencodeJSON(userHome, execPath); err != nil {
		return rollback(fmt.Errorf("configure opencode mcp: %w", err))
	}

	// Step 10: Install OpenCode plugin.
	if err := installOpenCodePlugin(userHome); err != nil {
		return rollback(fmt.Errorf("install opencode plugin: %w", err))
	}
	fmt.Println("installed: OpenCode plugin")

	fmt.Println("\nExperimental Windows service installed and started.")
	fmt.Printf("Use 'sc query metronous' or '%s service status' to check service health.\n", execPath)
	fmt.Println("OpenCode on this machine is now configured to use the shared daemon via 'metronous mcp'.")
	return nil
}

// handleForceReinstall stops and uninstalls the existing service before reinstalling.
func handleForceReinstall(dataDir string) error {
	svc, err := buildService(dataDir)
	if err != nil {
		// Service might not exist yet - that's OK for --force
		return nil
	}

	status, err := svc.Status()
	if err != nil || status == service.StatusUnknown {
		// Service doesn't exist - nothing to uninstall
		return nil
	}

	fmt.Println("Force reinstall: stopping existing service...")
	if err := svc.Stop(); err != nil {
		fmt.Printf("Warning: could not stop service: %v\n", err)
	}

	// Wait for service to fully stop before uninstalling
	time.Sleep(2 * time.Second)

	fmt.Println("Force reinstall: uninstalling existing service...")
	if err := svc.Uninstall(); err != nil {
		return fmt.Errorf("uninstall existing service: %w", err)
	}

	// Wait for uninstall to complete before proceeding
	time.Sleep(2 * time.Second)

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
