//go:build windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/daemon"
)

// NewInstallCommand creates the `metronous install` cobra command.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as a Windows service",
		Long: `Install Metronous as a Windows service (requires elevated terminal).

This command:
  1. Initializes ~/.metronous (idempotent)
  2. Registers the Metronous service via Windows Service Control Manager
  3. Starts the service
  4. Patches opencode.json to use ["metronous", "mcp"]
  5. Installs the OpenCode plugin (metronous.ts)

After running this command, every OpenCode instance will automatically
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.

Note: Run this from an elevated terminal (Run as Administrator).`,
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

	// Step 3: Install the Windows service via kardianos/service.
	fmt.Println("Installing Windows service...")
	svc, err := buildService(dataDir)
	if err != nil {
		return fmt.Errorf("build service: %w", err)
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("install service: %w (try running as Administrator)", err)
	}
	fmt.Printf("ok: service installed (platform: %s)\n", daemon.Platform())

	// Step 4: Start the service.
	fmt.Println("Starting service...")
	if err := svc.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Println("ok: service started")

	// Step 5: Patch opencode.json.
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	if err := patchOpencodeJSON(userHome); err != nil {
		// Non-fatal: print warning.
		fmt.Printf("\nWarning: could not patch opencode.json: %v\n", err)
		fmt.Println("Manually add to your opencode.json:")
		fmt.Println(`  "mcp": {"metronous": {"command": ["metronous", "mcp"], "type": "local"}}`)
	}

	// Step 6: Install OpenCode plugin.
	if err := installOpenCodePlugin(userHome); err != nil {
		fmt.Printf("\nWarning: could not install plugin: %v\n", err)
	} else {
		fmt.Println("installed: OpenCode plugin")
	}

	fmt.Println("\nMetronous service installed and started.")
	fmt.Println("Use 'sc query metronous' or 'metronous service status' to check service health.")
	fmt.Println("All OpenCode instances will now use the shared daemon via 'metronous mcp'.")
	return nil
}

// patchOpencodeJSON patches opencode.json to use the MCP shim.
// On Windows it checks %APPDATA%\opencode\opencode.json first, then falls
// back to userHome\.config\opencode\opencode.json.
func patchOpencodeJSON(userHome string) error {
	appData := os.Getenv("APPDATA")

	candidates := []string{}
	if appData != "" {
		candidates = append(candidates, filepath.Join(appData, "opencode", "opencode.json"))
	}
	candidates = append(candidates, filepath.Join(userHome, ".config", "opencode", "opencode.json"))

	var configPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			configPath = c
			break
		}
	}
	if configPath == "" {
		return fmt.Errorf("opencode.json not found (checked: %v)", candidates)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read opencode.json: %w", err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse opencode.json: %w", err)
	}

	// Ensure mcp map exists (OpenCode uses "mcp", not "mcpServers").
	mcpServers, _ := cfg["mcp"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}

	// Set or overwrite the metronous entry.
	mcpServers["metronous"] = map[string]interface{}{
		"command": []interface{}{"metronous", "mcp"},
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

// installOpenCodePlugin copies the metronous-plugin.ts to the plugins directory.
// On Windows it checks %APPDATA%\opencode\plugins first, then falls back to
// userHome\.config\opencode\plugins.
func installOpenCodePlugin(userHome string) error {
	// Find the plugin file - it's in the same directory as the executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	execDir := filepath.Dir(execPath)
	pluginSrc := filepath.Join(execDir, "metronous-plugin.ts")

	// Check if plugin exists in exec directory
	if _, err := os.Stat(pluginSrc); os.IsNotExist(err) {
		// Try current working directory as fallback
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("plugin not found in exec dir or CWD")
		}
		pluginSrc = filepath.Join(cwd, "metronous-plugin.ts")
		if _, err := os.Stat(pluginSrc); os.IsNotExist(err) {
			return fmt.Errorf("metronous-plugin.ts not found")
		}
	}

	// Determine plugins directory - check APPDATA first on Windows
	appData := os.Getenv("APPDATA")
	var pluginsDir string
	if appData != "" {
		pluginsDir = filepath.Join(appData, "opencode", "plugins")
	} else {
		pluginsDir = filepath.Join(userHome, ".config", "opencode", "plugins")
	}

	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}

	// Copy plugin file
	pluginDst := filepath.Join(pluginsDir, "metronous.ts")
	data, err := os.ReadFile(pluginSrc)
	if err != nil {
		return fmt.Errorf("read plugin: %w", err)
	}
	if err := os.WriteFile(pluginDst, data, 0644); err != nil {
		return fmt.Errorf("write plugin: %w", err)
	}
	return nil
}
