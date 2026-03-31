package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kiosvantra/metronous/internal/cli"
)

// TestInitCommandCreatesHomeLayout verifies the full directory structure is created.
func TestInitCommandCreatesHomeLayout(t *testing.T) {
	tempHome := t.TempDir()

	cmd := cli.NewInitCommand()
	cmd.SetArgs([]string{"--home", tempHome})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	// Verify directories exist with correct permissions.
	dirs := []struct {
		path string
		perm os.FileMode
	}{
		{tempHome, 0700},
		{filepath.Join(tempHome, "data"), 0700},
		{filepath.Join(tempHome, "agents"), 0700},
		{filepath.Join(tempHome, "artifacts"), 0700},
	}

	for _, d := range dirs {
		info, err := os.Stat(d.path)
		if err != nil {
			t.Errorf("directory %q not created: %v", d.path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d.path)
		}
		// Windows does not honour Unix permission bits — skip perm check.
		if runtime.GOOS != "windows" {
			gotPerm := info.Mode().Perm()
			if gotPerm != d.perm {
				t.Errorf("directory %q: permissions got %o, want %o", d.path, gotPerm, d.perm)
			}
		}
	}

	// Verify files exist with correct permissions.
	files := []struct {
		path string
		perm os.FileMode
	}{
		{filepath.Join(tempHome, "thresholds.json"), 0600},
		{filepath.Join(tempHome, "config.yaml"), 0600},
	}

	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			t.Errorf("file %q not created: %v", f.path, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("%q is a directory, expected file", f.path)
		}
		// Windows does not honour Unix permission bits — skip perm check.
		if runtime.GOOS != "windows" {
			gotPerm := info.Mode().Perm()
			if gotPerm != f.perm {
				t.Errorf("file %q: permissions got %o, want %o", f.path, gotPerm, f.perm)
			}
		}
	}

	// Verify tracking.db was created.
	trackingDB := filepath.Join(tempHome, "data", "tracking.db")
	if _, err := os.Stat(trackingDB); os.IsNotExist(err) {
		t.Errorf("tracking.db not created at %s", trackingDB)
	}

	// Verify thresholds.json is valid JSON.
	thresholdsPath := filepath.Join(tempHome, "thresholds.json")
	data, err := os.ReadFile(thresholdsPath)
	if err != nil {
		t.Fatalf("read thresholds.json: %v", err)
	}
	var thresholds map[string]interface{}
	if err := json.Unmarshal(data, &thresholds); err != nil {
		t.Errorf("thresholds.json is not valid JSON: %v", err)
	}
}

// TestInitCommandIsIdempotent verifies that running init multiple times is safe.
func TestInitCommandIsIdempotent(t *testing.T) {
	tempHome := t.TempDir()

	// Run init twice.
	for i := 0; i < 2; i++ {
		cmd := cli.NewInitCommand()
		cmd.SetArgs([]string{"--home", tempHome})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("init command run %d failed: %v", i+1, err)
		}
	}

	// After two runs, verify structure is still correct.
	dirs := []string{
		tempHome,
		filepath.Join(tempHome, "data"),
		filepath.Join(tempHome, "agents"),
		filepath.Join(tempHome, "artifacts"),
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("directory %q missing after second init: %v", d, err)
		}
	}

	// Files should not be overwritten — thresholds.json should still be valid.
	thresholdsPath := filepath.Join(tempHome, "thresholds.json")
	data, err := os.ReadFile(thresholdsPath)
	if err != nil {
		t.Fatalf("read thresholds.json after second run: %v", err)
	}
	var thresholds map[string]interface{}
	if err := json.Unmarshal(data, &thresholds); err != nil {
		t.Errorf("thresholds.json corrupted after second init: %v", err)
	}

	// config.yaml should also be valid YAML (we check non-empty).
	configPath := filepath.Join(tempHome, "config.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	if len(configData) == 0 {
		t.Error("config.yaml is empty after second init")
	}

	// tracking.db should still exist.
	trackingDB := filepath.Join(tempHome, "data", "tracking.db")
	if _, err := os.Stat(trackingDB); os.IsNotExist(err) {
		t.Errorf("tracking.db missing after second init")
	}
}

// TestInitCommandUsesCustomHome verifies --home flag works.
func TestInitCommandUsesCustomHome(t *testing.T) {
	parent := t.TempDir()
	customHome := filepath.Join(parent, "custom-metronous")

	cmd := cli.NewInitCommand()
	cmd.SetArgs([]string{"--home", customHome})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with custom home failed: %v", err)
	}

	if _, err := os.Stat(customHome); err != nil {
		t.Errorf("custom home directory not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(customHome, "data", "tracking.db")); err != nil {
		t.Errorf("tracking.db not created in custom home: %v", err)
	}
}

// TestInitCommandThresholdsNotOverwritten verifies that existing thresholds.json is preserved.
func TestInitCommandThresholdsNotOverwritten(t *testing.T) {
	tempHome := t.TempDir()

	// First init.
	cmd := cli.NewInitCommand()
	cmd.SetArgs([]string{"--home", tempHome})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Manually modify thresholds.json.
	thresholdsPath := filepath.Join(tempHome, "thresholds.json")
	customContent := `{"custom": "value"}`
	if err := os.WriteFile(thresholdsPath, []byte(customContent), 0600); err != nil {
		t.Fatalf("write custom thresholds: %v", err)
	}

	// Second init should NOT overwrite.
	cmd2 := cli.NewInitCommand()
	cmd2.SetArgs([]string{"--home", tempHome})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("second init: %v", err)
	}

	data, err := os.ReadFile(thresholdsPath)
	if err != nil {
		t.Fatalf("read thresholds after second init: %v", err)
	}
	if string(data) != customContent {
		t.Errorf("thresholds.json was overwritten; got %q, want %q", string(data), customContent)
	}
}
