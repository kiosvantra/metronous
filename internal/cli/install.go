//go:build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
  4. Patches ~/.config/opencode/opencode.json to use ["metronous", "mcp"]

After running this command, every OpenCode instance will automatically
connect to the shared long-lived Metronous daemon via the 'metronous mcp' shim.`,
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

	// Step 2: Determine paths.
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	dataDir := defaultDataDir()

	// Step 3: Generate unit file.
	unitContent, err := generateUnitFile(binaryPath, dataDir)
	if err != nil {
		return err
	}

	// Step 4: Write unit file.
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	systemdDir := filepath.Join(userHome, ".config", "systemd", "user")
	if err := os.MkdirAll(systemdDir, 0700); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	unitPath := filepath.Join(systemdDir, "metronous.service")
	if err := os.WriteFile(unitPath, []byte(unitContent), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	fmt.Printf("written: %s\n", unitPath)

	// Step 5: systemctl --user daemon-reload.
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}

	// Step 6: systemctl --user enable metronous.
	if err := runSystemctl("enable", "metronous"); err != nil {
		return err
	}

	// Step 7: systemctl --user start metronous.
	if err := runSystemctl("start", "metronous"); err != nil {
		return err
	}

	// Step 8: Patch opencode.json.
	if err := patchOpencodeJSON(userHome); err != nil {
		// Non-fatal: print warning.
		fmt.Printf("\nWarning: could not patch opencode.json: %v\n", err)
		fmt.Println("Manually add to ~/.config/opencode/opencode.json:")
		fmt.Println(`  "mcpServers": {"metronous": {"command": ["metronous", "mcp"]}}`)
	}

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
func patchOpencodeJSON(userHome string) error {
	configPath := filepath.Join(userHome, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("opencode.json not found at %s", configPath)
		}
		return fmt.Errorf("read opencode.json: %w", err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse opencode.json: %w", err)
	}

	// Ensure mcpServers map exists.
	mcpServers, _ := cfg["mcpServers"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}

	// Set or overwrite the metronous entry.
	mcpServers["metronous"] = map[string]interface{}{
		"command": []interface{}{"metronous", "mcp"},
	}
	cfg["mcpServers"] = mcpServers

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
