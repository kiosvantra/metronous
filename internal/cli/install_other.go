//go:build !linux && !windows

// Package cli provides the Cobra subcommand implementations for Metronous.
//
// # macOS Installation (launchd)
//
// On macOS, `metronous install` registers a launchd user agent by writing a
// plist to ~/Library/LaunchAgents/com.metronous.daemon.plist and loading it
// with `launchctl bootstrap`.  The daemon is kept alive by launchd
// (KeepAlive=true) so the embedded weekly benchmark fires even when no
// OpenCode client is running.
//
// The install is idempotent: if the plist already exists and is loaded,
// re-running `metronous install` unloads the old job before rewriting and
// reloading it.  This covers self-update scenarios transparently.
//
// Scheduling: the weekly cron ("0 0 2 * * 0", Sunday 02:00 local time) is
// embedded in the daemon binary itself via [scheduler.NewSchedulerWithContext].
// No separate launchd StartCalendarInterval is needed — launchd only needs
// to keep the process alive.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	metronous "github.com/kiosvantra/metronous"
	"github.com/spf13/cobra"
)

// launchAgentLabel is the launchd job label for the Metronous daemon.
const launchAgentLabel = "com.metronous.daemon"

// launchAgentPlistName is the filename of the launchd plist.
const launchAgentPlistName = launchAgentLabel + ".plist"

// generateLaunchdPlist returns the launchd plist XML for the Metronous daemon.
// The plist keeps the daemon alive (KeepAlive=true) so the embedded weekly
// benchmark cron fires without any additional launchd calendar interval.
func generateLaunchdPlist(binaryPath, dataDir string) string {
	logPath := filepath.Join(filepath.Dir(dataDir), "metronous.log")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
    "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>

    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>server</string>
        <string>--data-dir</string>
        <string>%s</string>
        <string>--daemon-mode</string>
    </array>

    <!-- Keep the daemon alive so the embedded weekly benchmark scheduler
         fires at the scheduled time (Sunday 02:00 local) even when no
         OpenCode client is open.  launchd will restart the process if it
         exits unexpectedly. -->
    <key>KeepAlive</key>
    <true/>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, launchAgentLabel, binaryPath, dataDir, logPath, logPath)
}

// NewInstallCommand creates the `metronous install` cobra command for macOS.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as a launchd user agent (macOS)",
		Long: `Install Metronous as a launchd user agent (macOS only).

This command:
  1. Initializes ~/.metronous (idempotent)
  2. Writes ~/Library/LaunchAgents/com.metronous.daemon.plist
  3. Loads the agent via launchctl bootstrap
  4. Patches ~/.config/opencode/opencode.json to use this executable for MCP
  5. Installs the OpenCode plugin (metronous.ts)

The daemon is kept alive by launchd (KeepAlive=true), so the embedded weekly
benchmark scheduler fires every Sunday at 02:00 local time even when no
OpenCode client is open.

Re-running install is idempotent: if a previous version is loaded it will be
unloaded first so the new binary and data-dir take effect immediately.

After running this command, every OpenCode instance will automatically
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall()
		},
	}
}

// runInstall performs all macOS installation steps.
func runInstall() error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}

	warnOpencodeConfig(userHome)

	// Step 1: Initialize ~/.metronous (idempotent).
	home := defaultMetronousHome()
	fmt.Println("Initializing Metronous home directory...")
	if err := runInit(home); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	// Step 2: Determine paths.
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	dataDir := defaultDataDir()

	// Verify launchctl is available (sanity check on macOS).
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("launchctl not found: macOS install requires launchd")
	}

	// Step 3: Prepare the LaunchAgents directory.
	agentsDir := filepath.Join(userHome, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	plistPath := filepath.Join(agentsDir, launchAgentPlistName)

	// Step 4: Pre-flight backup of OpenCode files.
	configPath := filepath.Join(userHome, ".config", "opencode", "opencode.json")
	pluginPath := filepath.Join(userHome, ".config", "opencode", "plugins", "metronous.ts")
	configBackup, err := backupFile(configPath)
	if err != nil {
		return fmt.Errorf("backup opencode.json: %w", err)
	}
	pluginBackup, err := backupFile(pluginPath)
	if err != nil {
		return fmt.Errorf("backup plugin: %w", err)
	}

	// rollback restores OpenCode config on failure (plist is cheap to remove).
	rollback := func(cause error) error {
		configErr := configBackup.restore(0600)
		pluginErr := pluginBackup.restore(0600)
		return combineRollback(cause, configErr, pluginErr)
	}

	// Step 5: Idempotency — unload existing job if already loaded.
	//
	// We attempt bootout regardless of whether the plist already exists;
	// launchctl will return an error if the job is not loaded, which we
	// safely ignore.
	_ = runLaunchctl("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel))

	// Step 6: Write the plist file.
	plistContent := generateLaunchdPlist(binaryPath, dataDir)
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return rollback(fmt.Errorf("write plist: %w", err))
	}
	fmt.Printf("written: %s\n", plistPath)

	// Step 7: Load the launchd agent.
	// "bootstrap gui/<uid> <plist>" loads the job for the current GUI session.
	if err := runLaunchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath); err != nil {
		_ = os.Remove(plistPath)
		return rollback(fmt.Errorf("load launchd agent: %w", err))
	}
	fmt.Printf("ok: launchctl bootstrap gui/%d %s\n", os.Getuid(), plistPath)

	// Step 8: Patch opencode.json.
	if err := patchOpencodeJSON(userHome, binaryPath); err != nil {
		return rollback(fmt.Errorf("configure opencode mcp: %w", err))
	}

	// Step 9: Install OpenCode plugin.
	if err := installOpenCodePlugin(userHome); err != nil {
		return rollback(fmt.Errorf("install opencode plugin: %w", err))
	}
	fmt.Println("installed: OpenCode plugin")

	fmt.Println("\nMetronous launchd agent installed and started.")
	fmt.Printf("Use 'launchctl list %s' to check agent status.\n", launchAgentLabel)
	fmt.Println("The weekly benchmark runs every Sunday at 02:00 local time via the embedded scheduler.")
	fmt.Println("All OpenCode instances will now use the shared daemon via 'metronous mcp'.")
	return nil
}

// runLaunchctl runs a launchctl sub-command and its arguments.
func runLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	label := "launchctl " + strings.Join(args, " ")
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", label, err, string(out))
	}
	return nil
}

// warnOpencodeConfig prints an informational message if OpenCode does not
// appear to be installed. It never blocks installation.
func warnOpencodeConfig(userHome string) {
	configPath := filepath.Join(userHome, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Println("")
		fmt.Println("Note: OpenCode config not found at ~/.config/opencode/opencode.json")
		fmt.Println("Metronous will work with OpenCode built-in agents automatically.")
		fmt.Println("To add custom agents or providers, configure OpenCode first:")
		fmt.Println("  curl -fsSL https://opencode.ai/install | bash && opencode")
		fmt.Println("")
	}
}

// patchOpencodeJSON patches ~/.config/opencode/opencode.json to use the MCP shim.
// If the file does not exist it is created with a minimal valid configuration.
func patchOpencodeJSON(userHome, binaryPath string) error {
	configDir := filepath.Join(userHome, ".config", "opencode")
	configPath := filepath.Join(configDir, "opencode.json")

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("create opencode config dir: %w", err)
	}

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

// installOpenCodePlugin copies the embedded metronous-plugin.ts to
// ~/.config/opencode/plugins/.
func installOpenCodePlugin(userHome string) error {
	pluginData := metronous.EmbeddedPlugin()
	if len(pluginData) == 0 {
		return fmt.Errorf("embedded plugin is empty")
	}

	pluginsDir := filepath.Join(userHome, ".config", "opencode", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}

	pluginDst := filepath.Join(pluginsDir, "metronous.ts")
	if err := os.WriteFile(pluginDst, pluginData, 0600); err != nil {
		return fmt.Errorf("write plugin: %w", err)
	}
	return nil
}
