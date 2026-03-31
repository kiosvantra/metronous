//go:build linux

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

// shellQuote wraps a path in single quotes and escapes any embedded single
// quotes so that the result is safe to embed literally in a systemd unit file
// value (systemd uses shell-like quoting for ExecStart and similar fields).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// generateUnitFile returns the rendered systemd unit file content.
// BinaryPath and DataDir are shell-quoted for ExecStart (systemd uses shell-style
// quoting for ExecStart tokens) but used raw for StandardOutput=append: which
// does not support shell quoting and expects a literal filesystem path.
func generateUnitFile(binaryPath, dataDir string) (string, error) {
	// Validate dataDir doesn't contain spaces which would break StandardOutput=append:
	if strings.Contains(dataDir, " ") {
		return "", fmt.Errorf("data directory %q contains spaces which is not supported", dataDir)
	}
	qBinary := shellQuote(binaryPath)
	qData := shellQuote(dataDir)
	content := fmt.Sprintf(`[Unit]
Description=Metronous Agent Intelligence Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s server --data-dir %s --daemon-mode
Restart=on-failure
RestartSec=5s
StandardOutput=append:%s/metronous.log
StandardError=inherit

[Install]
WantedBy=default.target
`, qBinary, qData, dataDir)
	return content, nil
}

// NewInstallCommand creates the `metronous install` cobra command.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as a systemd user service",
		Long: `Install Metronous as a systemd user service (Linux only).

This command:
  1. Initializes ~/.metronous (idempotent)
  2. Writes ~/.config/systemd/user/metronous.service
  3. Enables and starts the service via systemctl --user
  4. Patches ~/.config/opencode/opencode.json to use this executable for MCP
  5. Installs the OpenCode plugin (metronous.ts)

After running this command, every OpenCode instance will automatically
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall()
		},
	}
}

// warnOpencodeConfig prints an informational message if OpenCode does not appear
// to be installed. It never blocks installation — Metronous works with OpenCode
// built-in agents even without an opencode.json.
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

// runInstall performs all installation steps.
func runInstall() error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("run 'metronous install' as your normal user, not with sudo or root")
	}

	// Pre-flight: verify OpenCode is configured before touching anything.
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
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found: Linux install requires systemd user services")
	}

	// Step 3: Generate unit file.
	unitContent, err := generateUnitFile(binaryPath, dataDir)
	if err != nil {
		return err
	}

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

	// Step 5: Write unit file.
	systemdDir := filepath.Join(userHome, ".config", "systemd", "user")
	if err := os.MkdirAll(systemdDir, 0700); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	unitPath := filepath.Join(systemdDir, "metronous.service")
	if err := os.WriteFile(unitPath, []byte(unitContent), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	fmt.Printf("written: %s\n", unitPath)

	// All state-mutating steps from here use rollback on any failure.
	rollback := func(cause error) error {
		stopErr := runSystemctl("stop", "metronous")
		disableErr := runSystemctl("disable", "metronous")
		removeErr := os.Remove(unitPath)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		reloadErr := runSystemctl("daemon-reload")
		configErr := configBackup.restore(0600)
		pluginErr := pluginBackup.restore(0600)
		return combineRollback(cause, stopErr, disableErr, removeErr, reloadErr, configErr, pluginErr)
	}

	// Step 6: systemctl --user daemon-reload.
	if err := runSystemctl("daemon-reload"); err != nil {
		return rollback(err)
	}

	// Step 7: systemctl --user enable metronous.
	if err := runSystemctl("enable", "metronous"); err != nil {
		return rollback(err)
	}

	// Step 8: systemctl --user start metronous.
	if err := runSystemctl("start", "metronous"); err != nil {
		return rollback(err)
	}

	// Step 9: Patch opencode.json.
	if err := patchOpencodeJSON(userHome, binaryPath); err != nil {
		return rollback(fmt.Errorf("configure opencode mcp: %w", err))
	}

	// Step 10: Install OpenCode plugin.
	if err := installOpenCodePlugin(userHome); err != nil {
		return rollback(fmt.Errorf("install opencode plugin: %w", err))
	}
	fmt.Println("installed: OpenCode plugin")

	fmt.Println("\nMetronous service installed and started.")
	fmt.Println("Use 'systemctl --user status metronous' to check service health.")
	fmt.Println("All OpenCode instances will now use the shared daemon via 'metronous mcp'.")
	return nil
}

// runSystemctl runs a systemctl --user command.
func runSystemctl(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	out, err := cmd.CombinedOutput()
	label := "systemctl --user " + strings.Join(args, " ")
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", label, err, string(out))
	}
	fmt.Printf("ok: %s\n", label)
	return nil
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

// installOpenCodePlugin copies the embedded metronous-plugin.ts to ~/.config/opencode/plugins/
func installOpenCodePlugin(userHome string) error {
	pluginData := metronous.EmbeddedPlugin()
	if len(pluginData) == 0 {
		return fmt.Errorf("embedded plugin is empty")
	}

	// Create plugins directory
	pluginsDir := filepath.Join(userHome, ".config", "opencode", "plugins")
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
