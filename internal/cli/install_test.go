//go:build linux

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateUnitFile(t *testing.T) {
	binaryPath := "/usr/local/bin/metronous"
	dataDir := "/home/user/.metronous/data"

	content, err := generateUnitFile(binaryPath, dataDir)
	if err != nil {
		t.Fatalf("generateUnitFile: %v", err)
	}

	checks := []string{
		// ExecStart uses shell-quoting: binary and --data-dir value are single-quoted.
		"ExecStart='/usr/local/bin/metronous' server --data-dir '/home/user/.metronous/data'",
		"WantedBy=default.target",
		"Restart=on-failure",
		// StandardOutput=append: uses a raw (unquoted) filesystem path.
		"StandardOutput=append:/home/user/.metronous/data/metronous.log",
		"Description=Metronous Agent Intelligence Daemon",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
			t.Errorf("unit file missing %q\ngot:\n%s", want, content)
		}
	}
}

func TestPatchOpencodeJSON(t *testing.T) {
	// Set up a temp home with an existing opencode.json.
	tmpHome := t.TempDir()
	cfgDir := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := map[string]interface{}{
		"theme": "dark",
		"mcpServers": map[string]interface{}{
			"other": map[string]interface{}{"command": []string{"other-tool"}},
		},
	}
	raw, _ := json.MarshalIndent(initial, "", "  ")
	cfgPath := filepath.Join(cfgDir, "opencode.json")
	if err := os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}

	if err := patchOpencodeJSON(tmpHome); err != nil {
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

	mcpServers, ok := result["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers not a map")
	}
	metronousEntry, ok := mcpServers["metronous"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers.metronous not found")
	}
	command, ok := metronousEntry["command"].([]interface{})
	if !ok || len(command) != 2 {
		t.Fatalf("expected command=[metronous mcp], got %v", metronousEntry["command"])
	}
	if command[0] != "metronous" || command[1] != "mcp" {
		t.Errorf("expected [metronous mcp], got %v", command)
	}

	// Existing keys must be preserved.
	if result["theme"] != "dark" {
		t.Errorf("theme key was lost")
	}
	if _, exists := mcpServers["other"]; !exists {
		t.Errorf("pre-existing mcpServers.other was removed")
	}
}

func TestPatchOpencodeJSONMissing(t *testing.T) {
	tmpHome := t.TempDir()
	// opencode.json does not exist.
	err := patchOpencodeJSON(tmpHome)
	if err == nil {
		t.Fatal("expected error when opencode.json is missing")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
