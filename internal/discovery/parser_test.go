package discovery_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kiosvantra/metronous/internal/discovery"
)

func TestParseAgentConfigFromDirectory(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "my-agent")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]interface{}{
		"id":          "my-agent",
		"name":        "My Agent",
		"model":       "claude-sonnet-4-5",
		"description": "A test agent",
	}
	writeJSON(t, filepath.Join(agentDir, "opencode.json"), cfg)

	got, err := discovery.ParseAgentDirectory(agentDir)
	if err != nil {
		t.Fatalf("ParseAgentDirectory: %v", err)
	}
	if got.ID != "my-agent" {
		t.Errorf("ID: want %q, got %q", "my-agent", got.ID)
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: want %q, got %q", "claude-sonnet-4-5", got.Model)
	}
}

func TestParseAgentConfigDerivesIDFromDir(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "fancy-agent")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Config file without an explicit id field.
	writeJSON(t, filepath.Join(agentDir, "opencode.json"), map[string]interface{}{
		"name":  "Fancy Agent",
		"model": "gpt-4o",
	})

	got, err := discovery.ParseAgentDirectory(agentDir)
	if err != nil {
		t.Fatalf("ParseAgentDirectory: %v", err)
	}
	if got.ID != "fancy-agent" {
		t.Errorf("expected ID to be derived from dir name, got %q", got.ID)
	}
}

func TestParseAgentConfigFallsBackToAgentJSON(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "agent.json"), map[string]interface{}{
		"id":    "agent-b",
		"model": "claude-3-haiku",
	})

	got, err := discovery.ParseAgentDirectory(dir)
	if err != nil {
		t.Fatalf("ParseAgentDirectory: %v", err)
	}
	if got.ID != "agent-b" {
		t.Errorf("ID: want %q, got %q", "agent-b", got.ID)
	}
}

func TestParseAgentConfigNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	_, err := discovery.ParseAgentDirectory(dir)
	if err == nil {
		t.Error("expected error when no config file present")
	}
}

func TestParseAgentConfigUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.yaml")
	if err := os.WriteFile(path, []byte("id: foo"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := discovery.ParseAgentConfig(path)
	if err == nil {
		t.Error("expected error for .yaml format")
	}
}

func TestDiscoverExistingAgentsOnStartup(t *testing.T) {
	agentsDir := t.TempDir()

	// Create two agent directories.
	for _, name := range []string{"agent-alpha", "agent-beta"} {
		d := filepath.Join(agentsDir, name)
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, filepath.Join(d, "opencode.json"), map[string]interface{}{
			"id":    name,
			"model": "claude-sonnet-4-5",
		})
	}

	reg := discovery.NewRegistry()
	if err := reg.LoadFromDisk(agentsDir); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	agents := reg.List()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	for _, name := range []string{"agent-alpha", "agent-beta"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("agent %q not found in registry", name)
		}
	}
}

func TestRegistryRegisterGetList(t *testing.T) {
	reg := discovery.NewRegistry()

	a := &discovery.AgentConfig{ID: "x", Name: "X", Model: "gpt-4"}
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg.Get("x")
	if !ok {
		t.Fatal("expected to find agent x")
	}
	if got.Model != "gpt-4" {
		t.Errorf("Model: want gpt-4, got %s", got.Model)
	}

	list := reg.List()
	if len(list) != 1 {
		t.Errorf("expected 1 agent, got %d", len(list))
	}
}

func TestRegistryUnregister(t *testing.T) {
	reg := discovery.NewRegistry()
	_ = reg.Register(&discovery.AgentConfig{ID: "del-me", Model: "x"})
	reg.Unregister("del-me")
	if _, ok := reg.Get("del-me"); ok {
		t.Error("expected agent to be removed")
	}
}

func TestRegistryLoadFromDiskNonExistentDir(t *testing.T) {
	reg := discovery.NewRegistry()
	err := reg.LoadFromDisk("/tmp/nonexistent-metronous-agents-xyz")
	if err != nil {
		t.Errorf("LoadFromDisk should not error on missing dir: %v", err)
	}
}

// writeJSON writes v as JSON to path.
func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
