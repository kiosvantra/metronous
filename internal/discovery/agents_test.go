package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeOpenCodeJSON writes a minimal opencode.json with the given agent configs
// into dir and returns the path.
func writeOpenCodeJSON(t *testing.T, dir string, agents map[string]opencodeAgentConfig) string {
	t.Helper()
	cfg := opencodeRootConfig{Agent: agents}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal opencode.json: %v", err)
	}
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}
	return path
}

// containsAgent returns true if the result slice contains an agent with the given ID.
func containsAgent(results []AgentInfo, id string) bool {
	for _, a := range results {
		if a.ID == id {
			return true
		}
	}
	return false
}

// TestDiscoverAgents_BuiltInsAlwaysPresent verifies that the four built-in agents
// are always present even when no config files exist.
func TestDiscoverAgents_BuiltInsAlwaysPresent(t *testing.T) {
	dir := t.TempDir()
	// No opencode.json written — built-ins must still appear.
	results := DiscoverAgents(dir)

	for _, id := range builtinAgents {
		if !containsAgent(results, id) {
			t.Errorf("expected built-in agent %q in results, not found", id)
		}
	}
}

// TestDiscoverAgents_DisabledExcluded verifies that an agent with disable:true
// does not appear in the results.
func TestDiscoverAgents_DisabledExcluded(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodeJSON(t, dir, map[string]opencodeAgentConfig{
		"my-agent": {Disable: true},
	})

	results := DiscoverAgents(dir)
	if containsAgent(results, "my-agent") {
		t.Error("expected disabled agent 'my-agent' to be excluded, but it was present")
	}
}

// TestDiscoverAgents_HiddenIncluded verifies that an agent with hidden:true IS
// included — hidden only affects @ autocomplete visibility, not benchmarking.
func TestDiscoverAgents_HiddenIncluded(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodeJSON(t, dir, map[string]opencodeAgentConfig{
		"hidden-agent": {Hidden: true},
	})

	results := DiscoverAgents(dir)
	if !containsAgent(results, "hidden-agent") {
		t.Error("expected hidden agent 'hidden-agent' to be included, but it was absent")
	}
}

// TestDiscoverAgents_SystemAgentsExcluded verifies that compaction, title, and
// summary agents are never surfaced, even if they appear in a config file.
func TestDiscoverAgents_SystemAgentsExcluded(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodeJSON(t, dir, map[string]opencodeAgentConfig{
		"compaction": {},
		"title":      {},
		"summary":    {},
	})

	results := DiscoverAgents(dir)
	for id := range systemAgents {
		if containsAgent(results, id) {
			t.Errorf("expected system agent %q to be excluded, but it was present", id)
		}
	}
}

// TestDiscoverAgents_Deduplication verifies that an agent appearing in multiple
// sources is deduplicated to a single entry in the result.
func TestDiscoverAgents_Deduplication(t *testing.T) {
	// Create a two-level directory tree so collectProjectDirs returns both dirs.
	parentDir := t.TempDir()
	childDir := filepath.Join(parentDir, "project")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	// Write the same agent in both levels.
	writeOpenCodeJSON(t, parentDir, map[string]opencodeAgentConfig{
		"shared-agent": {Mode: "primary"},
	})
	writeOpenCodeJSON(t, childDir, map[string]opencodeAgentConfig{
		"shared-agent": {Mode: "subagent"},
	})

	results := DiscoverAgents(childDir)

	count := 0
	for _, a := range results {
		if a.ID == "shared-agent" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'shared-agent' to appear exactly once, got %d", count)
	}
}

// TestDiscoverAgents_MissingConfigDir verifies that DiscoverAgents does not
// panic when the workDir has no opencode.json and returns at least the built-ins.
func TestDiscoverAgents_MissingConfigDir(t *testing.T) {
	dir := t.TempDir()
	// No opencode.json, no .opencode/agents — should not panic.

	var results []AgentInfo
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DiscoverAgents panicked: %v", r)
		}
	}()
	results = DiscoverAgents(dir)

	// Must return at least the four built-ins.
	if len(results) < len(builtinAgents) {
		t.Errorf("expected at least %d built-in agents, got %d", len(builtinAgents), len(results))
	}
	for _, id := range builtinAgents {
		if !containsAgent(results, id) {
			t.Errorf("expected built-in agent %q in results, not found", id)
		}
	}
}

// TestDiscoverAgents_WorkdirPriority verifies that a workDir config overrides an
// ancestor-level config (workDir wins; priority inversion fix).
func TestDiscoverAgents_WorkdirPriority(t *testing.T) {
	// Build: parentDir (git root) → childDir (workDir)
	parentDir := t.TempDir()
	// Mark parentDir as a fake git root.
	if err := os.MkdirAll(filepath.Join(parentDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	childDir := filepath.Join(parentDir, "project")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	// Parent (git root) disables the agent; child (workDir) re-enables it.
	writeOpenCodeJSON(t, parentDir, map[string]opencodeAgentConfig{
		"priority-agent": {Disable: true},
	})
	writeOpenCodeJSON(t, childDir, map[string]opencodeAgentConfig{
		"priority-agent": {Disable: false},
	})

	results := DiscoverAgents(childDir)
	if !containsAgent(results, "priority-agent") {
		t.Error("expected workDir config to re-enable 'priority-agent', but it was absent")
	}
}
