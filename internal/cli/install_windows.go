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
	var dryRun bool
	var assumeYes bool
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
			return runInstall(cmd, forceInstall, installOptions{dryRun: dryRun, yes: assumeYes})
		},
	}
	cmd.Flags().BoolVar(&forceInstall, "force", false, "Force reinstall if service already exists")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the OpenCode and service changes without writing files")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the confirmation prompt and apply the changes")
	return cmd
}

// runInstall performs all installation steps.
func runInstall(cmd *cobra.Command, force bool, opts installOptions) error {
	// Step 0: Validate prerequisites before any changes.
	dataDir := defaultDataDir()
	if err := validateDataDir(dataDir); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	if err := validatePermissions(); err != nil {
		return fmt.Errorf("permission validation failed: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Step 1: Validate binary location before any changes.
	if err := validateBinary(execPath); err != nil {
		return fmt.Errorf("binary validation failed: %w", err)
	}

	// Step 2: Check if daemon is already running (daemon uses dynamic ports, not fixed 8844).
	if err := checkDaemonRunning(); err != nil {
		return fmt.Errorf("daemon conflict: %w (use 'metronous uninstall' first or --force to reinstall)", err)
	}

	configRoot := resolveOpenCodeRoot(userHome)
	configPath := filepath.Join(configRoot, "opencode.json")
	pluginPath := filepath.Join(configRoot, "plugins", "metronous.ts")
	plan := []string{
		fmt.Sprintf("- initialize %s", defaultMetronousHome()),
		"- install or replace the Windows service metronous",
		fmt.Sprintf("- update %s", configPath),
		fmt.Sprintf("- install %s", pluginPath),
	}
	skip, err := reviewInstallPlan(cmd, plan, opts.dryRun, opts.yes)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	// Step 1: Check if service already exists and auto-cleanup if not using --force
	// This provides idempotency even without the --force flag
	if !force {
		if err := handleServiceExists(dataDir); err != nil {
			return fmt.Errorf("service cleanup failed: %w", err)
		}
	}

	// Step 2: Handle --force reinstall if service already exists.
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

	configBackup, err := backupFile(configPath)
	if err != nil {
		return fmt.Errorf("backup opencode.json: %w", err)
	}
	pluginBackup, err := backupFile(pluginPath)
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

// handleServiceExists checks if service already exists and removes it for idempotency.
// This enables `metronous install` to be run multiple times without --force.
func handleServiceExists(dataDir string) error {
	svc, err := buildService(dataDir)
	if err != nil {
		// Service might not exist yet - that's OK
		return nil
	}

	status, err := svc.Status()
	if err != nil || status == service.StatusUnknown {
		// Service doesn't exist - nothing to do
		return nil
	}

	// Service exists - stop, uninstall, and clean up
	fmt.Println("Idempotent install: found existing service, cleaning up...")
	if err := svc.Stop(); err != nil {
		fmt.Printf("Warning: could not stop service: %v\n", err)
	}

	if !waitForServiceStop(svc, 10*time.Second) {
		fmt.Printf("Warning: service did not stop within timeout\n")
	}

	if err := svc.Uninstall(); err != nil {
		return fmt.Errorf("uninstall existing service: %w", err)
	}

	if !waitForServiceUninstalled(dataDir, 10*time.Second) {
		fmt.Printf("Warning: service uninstall may not have completed\n")
	}

	cleanupServiceFiles()
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

	// Poll for service to fully stop before uninstalling
	if !waitForServiceStop(svc, 10*time.Second) {
		fmt.Printf("Warning: service did not stop within timeout, proceeding with uninstall anyway\n")
	}

	fmt.Println("Force reinstall: uninstalling existing service...")
	if err := svc.Uninstall(); err != nil {
		return fmt.Errorf("uninstall existing service: %w", err)
	}

	// Poll for uninstall to complete before proceeding
	if !waitForServiceUninstalled(dataDir, 10*time.Second) {
		fmt.Printf("Warning: service uninstall may not have completed within timeout\n")
	}

	// Clean up leftover files from previous installation
	cleanupServiceFiles()

	return nil
}

// waitForServiceStop polls service status until it stops or timeout.
func waitForServiceStop(svc service.Service, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := svc.Status()
		if err != nil || status == service.StatusUnknown || status == service.StatusStopped {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// waitForServiceUninstalled polls until service is completely removed.
func waitForServiceUninstalled(dataDir string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		svc, err := buildService(dataDir)
		if err != nil {
			// Service doesn't exist - uninstall complete
			return true
		}
		status, err := svc.Status()
		if err != nil || status == service.StatusUnknown {
			// Service doesn't exist - uninstall complete
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// cleanupServiceFiles removes leftover files from previous installation.
func cleanupServiceFiles() error {
	home := defaultMetronousHome()

	filesToClean := []string{
		"mcp.port",
		"daemon.lock",
		"daemon.pid",
	}

	var cleaned []string
	for _, f := range filesToClean {
		path := filepath.Join(home, f)
		if err := os.Remove(path); err == nil {
			cleaned = append(cleaned, f)
		}
	}

	if len(cleaned) > 0 {
		fmt.Printf("Cleaned up: %v\n", cleaned)
	}

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
