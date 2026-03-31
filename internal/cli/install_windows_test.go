//go:build windows

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metronous "github.com/kiosvantra/metronous"
)

func TestPatchOpencodeJSON(t *testing.T) {
	// Override APPDATA so we don't pick up the real opencode.json.
	t.Setenv("APPDATA", t.TempDir())

	// Set up a temp home with an existing opencode.json under .config/opencode.
	tmpHome := t.TempDir()
	cfgDir := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := map[string]interface{}{
		"theme": "dark",
		"mcp": map[string]interface{}{
			"other": map[string]interface{}{"command": []string{"other-tool"}},
		},
	}
	raw, _ := json.MarshalIndent(initial, "", "  ")
	cfgPath := filepath.Join(cfgDir, "opencode.json")
	if err := os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}

	binaryPath := `C:\\Tools\\metronous.exe`
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
	if _, exists := mcp["other"]; !exists {
		t.Errorf("pre-existing mcp.other was removed")
	}
}

func TestPatchOpencodeJSONAppDataFirst(t *testing.T) {
	// Verify that %APPDATA% location is preferred when it exists.
	tmpHome := t.TempDir()
	tmpAppData := t.TempDir()

	// Create opencode.json under both locations.
	appDataDir := filepath.Join(tmpAppData, "opencode")
	if err := os.MkdirAll(appDataDir, 0700); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := map[string]interface{}{"location": "appdata"}
	raw, _ := json.MarshalIndent(initial, "", "  ")
	appDataPath := filepath.Join(appDataDir, "opencode.json")
	if err := os.WriteFile(appDataPath, raw, 0600); err != nil {
		t.Fatal(err)
	}

	initial2 := map[string]interface{}{"location": "dotconfig"}
	raw2, _ := json.MarshalIndent(initial2, "", "  ")
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.WriteFile(configPath, raw2, 0600); err != nil {
		t.Fatal(err)
	}

	// Set APPDATA to our temp directory.
	t.Setenv("APPDATA", tmpAppData)

	if err := patchOpencodeJSON(tmpHome, `C:\\Tools\\metronous.exe`); err != nil {
		t.Fatalf("patchOpencodeJSON: %v", err)
	}

	// The APPDATA file should have been patched (it's checked first).
	data, err := os.ReadFile(appDataPath)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["location"] != "appdata" {
		t.Errorf("expected appdata location to be patched, got %v", result["location"])
	}
	mcp, ok := result["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp not a map in appdata file")
	}
	if _, exists := mcp["metronous"]; !exists {
		t.Error("metronous entry not found in appdata file")
	}

	// The .config file should NOT have been modified.
	data2, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var result2 map[string]interface{}
	if err := json.Unmarshal(data2, &result2); err != nil {
		t.Fatal(err)
	}
	if _, exists := result2["mcp"]; exists {
		t.Error(".config/opencode file was modified when APPDATA file existed")
	}
}

func TestPatchOpencodeJSONMissing(t *testing.T) {
	tmpHome := t.TempDir()
	// Ensure APPDATA also points to a temp dir without opencode.json.
	t.Setenv("APPDATA", t.TempDir())

	err := patchOpencodeJSON(tmpHome, `C:\\Tools\\metronous.exe`)
	if err == nil {
		t.Fatal("expected error when opencode.json is missing")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInstallOpenCodePluginUsesBundledPlugin(t *testing.T) {
	tmpHome := t.TempDir()
	tmpAppData := t.TempDir()
	t.Setenv("APPDATA", tmpAppData)

	// Create opencode.json in %APPDATA%\opencode so resolveOpenCodeRoot picks it up.
	appDataDir := filepath.Join(tmpAppData, "opencode")
	if err := os.MkdirAll(appDataDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDataDir, "opencode.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := installOpenCodePlugin(tmpHome); err != nil {
		t.Fatalf("installOpenCodePlugin: %v", err)
	}

	pluginPath := filepath.Join(tmpAppData, "opencode", "plugins", "metronous.ts")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read installed plugin: %v", err)
	}

	if string(data) != string(metronous.EmbeddedPlugin()) {
		t.Fatal("installed plugin does not match bundled plugin")
	}
}

func TestResolveOpenCodeRootFallsBackWhenAppDataConfigMissing(t *testing.T) {
	tmpHome := t.TempDir()
	tmpAppData := t.TempDir()
	t.Setenv("APPDATA", tmpAppData)

	got := resolveOpenCodeRoot(tmpHome)
	want := filepath.Join(tmpHome, ".config", "opencode")
	if got != want {
		t.Fatalf("resolveOpenCodeRoot() = %q, want %q", got, want)
	}
}
