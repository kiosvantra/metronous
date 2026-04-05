//go:build !linux && !windows

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metronous "github.com/kiosvantra/metronous"
)

// TestGenerateLaunchdPlist verifies the generated plist contains the required
// fields and that paths are embedded verbatim (no shell quoting for launchd).
func TestGenerateLaunchdPlist(t *testing.T) {
	binaryPath := "/usr/local/bin/metronous"
	dataDir := "/Users/user/.metronous/data"

	content := generateLaunchdPlist(binaryPath, dataDir)

	checks := []struct {
		label string
		want  string
	}{
		{"Label", "com.metronous.daemon"},
		{"binary", binaryPath},
		{"data-dir arg", dataDir},
		{"daemon-mode arg", "--daemon-mode"},
		{"KeepAlive true", "<key>KeepAlive</key>"},
		{"KeepAlive value", "<true/>"},
		{"RunAtLoad true", "<key>RunAtLoad</key>"},
		{"log path", "/Users/user/.metronous/metronous.log"},
	}
	for _, tc := range checks {
		if !strings.Contains(content, tc.want) {
			t.Errorf("plist missing %s (%q)\ngot:\n%s", tc.label, tc.want, content)
		}
	}
}

// TestGenerateLaunchdPlistLogPath verifies that the log file is placed one
// directory above the data dir (i.e., ~/.metronous/metronous.log).
func TestGenerateLaunchdPlistLogPath(t *testing.T) {
	dataDir := "/tmp/testuser/.metronous/data"
	content := generateLaunchdPlist("/bin/metronous", dataDir)

	wantLog := "/tmp/testuser/.metronous/metronous.log"
	if !strings.Contains(content, wantLog) {
		t.Errorf("expected log path %q in plist\ngot:\n%s", wantLog, content)
	}
}

// TestPatchOpencodeJSONMacOS verifies patchOpencodeJSON on non-Linux/Windows.
func TestPatchOpencodeJSONMacOS(t *testing.T) {
	tmpHome := t.TempDir()
	cfgDir := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := map[string]interface{}{
		"theme": "dark",
	}
	raw, _ := json.MarshalIndent(initial, "", "  ")
	cfgPath := filepath.Join(cfgDir, "opencode.json")
	if err := os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}

	binaryPath := "/usr/local/bin/metronous"
	if err := patchOpencodeJSON(tmpHome, binaryPath); err != nil {
		t.Fatalf("patchOpencodeJSON: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	mcp, ok := result["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp not a map")
	}
	metronousEntry, ok := mcp["metronous"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp.metronous not found")
	}
	command, ok := metronousEntry["command"].([]interface{})
	if !ok || len(command) != 2 {
		t.Fatalf("expected command=[binary mcp], got %v", metronousEntry["command"])
	}
	if command[0] != binaryPath || command[1] != "mcp" {
		t.Errorf("expected [%s mcp], got %v", binaryPath, command)
	}

	// Existing keys must be preserved.
	if result["theme"] != "dark" {
		t.Errorf("theme key was lost")
	}
}

// TestPatchOpencodeJSONMissingMacOS verifies that opencode.json is created if
// it does not exist on macOS / other platforms.
func TestPatchOpencodeJSONMissingMacOS(t *testing.T) {
	tmpHome := t.TempDir()
	binaryPath := "/usr/local/bin/metronous"

	if err := patchOpencodeJSON(tmpHome, binaryPath); err != nil {
		t.Fatalf("patchOpencodeJSON should create opencode.json if missing, got: %v", err)
	}

	configPath := filepath.Join(tmpHome, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("opencode.json not created: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("created opencode.json is invalid JSON: %v", err)
	}
	mcp, ok := cfg["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp key missing from created opencode.json")
	}
	entry, ok := mcp["metronous"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp.metronous missing")
	}
	cmd, _ := entry["command"].([]interface{})
	if len(cmd) != 2 || cmd[0] != binaryPath || cmd[1] != "mcp" {
		t.Errorf("unexpected command: %v", cmd)
	}
}

// TestInstallOpenCodePluginMacOS verifies the plugin is copied to the correct
// path on macOS / other platforms.
func TestInstallOpenCodePluginMacOS(t *testing.T) {
	tmpHome := t.TempDir()

	if err := installOpenCodePlugin(tmpHome); err != nil {
		t.Fatalf("installOpenCodePlugin: %v", err)
	}

	pluginPath := filepath.Join(tmpHome, ".config", "opencode", "plugins", "metronous.ts")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read installed plugin: %v", err)
	}

	if string(data) != string(metronous.EmbeddedPlugin()) {
		t.Fatal("installed plugin does not match bundled plugin")
	}
}

// TestLaunchAgentLabel verifies the launchd label constant has the expected
// value used in plist files and launchctl commands.
func TestLaunchAgentLabel(t *testing.T) {
	if launchAgentLabel != "com.metronous.daemon" {
		t.Errorf("unexpected launchAgentLabel: %q", launchAgentLabel)
	}
	if launchAgentPlistName != "com.metronous.daemon.plist" {
		t.Errorf("unexpected launchAgentPlistName: %q", launchAgentPlistName)
	}
}
