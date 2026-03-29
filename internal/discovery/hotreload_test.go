package discovery_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/discovery"
)

func TestHotReloadUpdatesRegistry(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent-hot")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Seed the registry with an initial config.
	writeJSON(t, filepath.Join(agentDir, "opencode.json"), map[string]interface{}{
		"id":    "agent-hot",
		"model": "gpt-4",
	})

	reg := discovery.NewRegistry()
	_ = reg.LoadFromDisk(dir)

	if _, ok := reg.Get("agent-hot"); !ok {
		t.Fatal("agent-hot should be registered initially")
	}

	// Set up watcher + hot-reloader.
	w, err := discovery.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Watch(agentDir); err != nil {
		t.Fatal(err)
	}

	logger := zap.NewNop()
	hr := discovery.NewHotReloader(w, reg, logger)
	hr.Start()

	// Overwrite the config with a new model.
	writeJSON(t, filepath.Join(agentDir, "opencode.json"), map[string]interface{}{
		"id":    "agent-hot",
		"model": "claude-3-haiku",
	})

	// Wait for hot-reload to kick in (debounce + processing).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, ok := reg.Get("agent-hot")
		if ok && a.Model == "claude-3-haiku" {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	a, _ := reg.Get("agent-hot")
	t.Errorf("expected model to be updated to claude-3-haiku, got %q", a.Model)
}

func TestApplyModelChangeRewritesAgentConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "opencode.json")
	writeJSON(t, cfgPath, map[string]interface{}{
		"id":    "my-agent",
		"model": "gpt-4",
	})

	reg := discovery.NewRegistry()
	_ = reg.Register(&discovery.AgentConfig{
		ID:         "my-agent",
		Model:      "gpt-4",
		SourcePath: cfgPath,
	})

	logger := zap.NewNop()
	if err := discovery.ApplyModelChange(reg, "my-agent", "claude-sonnet-4-5", logger); err != nil {
		t.Fatalf("ApplyModelChange: %v", err)
	}

	// Verify registry was updated.
	got, ok := reg.Get("my-agent")
	if !ok {
		t.Fatal("agent not found after apply")
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("expected model claude-sonnet-4-5, got %q", got.Model)
	}

	// Verify the file was rewritten.
	parsed, err := discovery.ParseAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("re-parse after apply: %v", err)
	}
	if parsed.Model != "claude-sonnet-4-5" {
		t.Errorf("file model not updated: %q", parsed.Model)
	}
}

func TestApplyModelChangeUnknownAgent(t *testing.T) {
	reg := discovery.NewRegistry()
	err := discovery.ApplyModelChange(reg, "ghost-agent", "gpt-4", nil)
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}
