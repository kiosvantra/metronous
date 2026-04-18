package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kiosvantra/metronous/internal/config"
)

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestLoadOpenCodeConfig_AgentModel(t *testing.T) {
	path := writeConfig(t, `{
		"agent": {
			"igris": { "model": "opencode/claude-sonnet-4-6" },
			"sdd-apply": { "model": "opencode/claude-haiku-3-5" },
			"no-model-agent": {}
		}
	}`)

	cfg, err := config.LoadOpenCodeConfig(path)
	if err != nil {
		t.Fatalf("LoadOpenCodeConfig: %v", err)
	}

	tests := []struct {
		agentID   string
		wantModel string
		wantFound bool
	}{
		{"igris", "opencode/claude-sonnet-4-6", true},
		{"sdd-apply", "opencode/claude-haiku-3-5", true},
		{"no-model-agent", "", false},
		{"nonexistent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.agentID, func(t *testing.T) {
			gotModel, gotFound := cfg.AgentModel(tt.agentID)
			if gotFound != tt.wantFound {
				t.Errorf("AgentModel(%q) found=%v, want %v", tt.agentID, gotFound, tt.wantFound)
			}
			if gotModel != tt.wantModel {
				t.Errorf("AgentModel(%q) model=%q, want %q", tt.agentID, gotModel, tt.wantModel)
			}
		})
	}
}

func TestLoadOpenCodeConfig_EmptyAgentSection(t *testing.T) {
	path := writeConfig(t, `{}`)

	cfg, err := config.LoadOpenCodeConfig(path)
	if err != nil {
		t.Fatalf("LoadOpenCodeConfig: %v", err)
	}

	model, found := cfg.AgentModel("any-agent")
	if found {
		t.Errorf("expected not found, got model=%q", model)
	}
}

func TestLoadOpenCodeConfig_FileNotFound(t *testing.T) {
	_, err := config.LoadOpenCodeConfig("/nonexistent/path/opencode.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadOpenCodeConfig_InvalidJSON(t *testing.T) {
	path := writeConfig(t, `{invalid json`)

	_, err := config.LoadOpenCodeConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestNullAgentModelLookup(t *testing.T) {
	model, found := config.NullAgentModelLookup("any-agent")
	if found {
		t.Errorf("NullAgentModelLookup: expected found=false, got true (model=%q)", model)
	}
}

func TestOpenCodeConfigNilSafety(t *testing.T) {
	var cfg *config.OpenCodeConfig
	model, found := cfg.AgentModel("igris")
	if found {
		t.Errorf("nil cfg AgentModel: expected found=false, got true (model=%q)", model)
	}
}
