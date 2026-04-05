package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// openCodeAgentConfig holds the per-agent fields we care about in opencode.json.
type openCodeAgentConfig struct {
	Model string `json:"model"`
}

// openCodeConfig is a minimal representation of ~/.config/opencode/opencode.json.
// Only the fields required for agent model lookup are decoded; all other fields
// are ignored.
type openCodeConfig struct {
	Agent map[string]openCodeAgentConfig `json:"agent"`
}

// OpenCodeConfig provides model lookups from an opencode.json config file.
type OpenCodeConfig struct {
	agents map[string]openCodeAgentConfig
}

// LoadOpenCodeConfig reads the opencode.json file at path and returns an
// OpenCodeConfig that can be queried for per-agent model names.
// Returns an error if the file cannot be read or parsed.
func LoadOpenCodeConfig(path string) (*OpenCodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read opencode config %q: %w", path, err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse opencode config %q: %w", path, err)
	}

	if cfg.Agent == nil {
		cfg.Agent = make(map[string]openCodeAgentConfig)
	}

	return &OpenCodeConfig{agents: cfg.Agent}, nil
}

// DefaultOpenCodeConfigPath returns the canonical path to the opencode.json
// configuration file: ~/.config/opencode/opencode.json.
func DefaultOpenCodeConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback — should never happen in practice.
		return filepath.Join(".config", "opencode", "opencode.json")
	}
	return filepath.Join(homeDir, ".config", "opencode", "opencode.json")
}

// AgentModel returns the configured model for the given agentID.
// The model string is returned as-is from the config file (may include provider
// prefixes such as "opencode/claude-sonnet-4-6").
// Returns ("", false) if the agent is not found in the config or has no model set.
func (c *OpenCodeConfig) AgentModel(agentID string) (string, bool) {
	if c == nil {
		return "", false
	}
	agent, ok := c.agents[agentID]
	if !ok || agent.Model == "" {
		return "", false
	}
	return agent.Model, true
}

// AgentModelLookup is a function type that returns the configured model for an
// agent. Used by the benchmark runner to determine the currently active model
// without coupling the runner directly to opencode.json.
// Returns ("", false) if the agent is not configured or the model is unknown.
type AgentModelLookup func(agentID string) (model string, found bool)

// NullAgentModelLookup is an AgentModelLookup that always returns not-found.
// Use this in tests or contexts where opencode.json is unavailable.
func NullAgentModelLookup(_ string) (string, bool) {
	return "", false
}

// LoadDefaultAgentModelLookup loads the opencode.json config from the default
// path (~/.config/opencode/opencode.json) and returns an AgentModelLookup.
// If the file cannot be read or parsed, a NullAgentModelLookup is returned so
// the runner falls back to the window-based heuristic without failing.
// The errFn callback (if non-nil) receives any load error for logging purposes.
func LoadDefaultAgentModelLookup(errFn func(err error)) AgentModelLookup {
	cfg, err := LoadOpenCodeConfig(DefaultOpenCodeConfigPath())
	if err != nil {
		if errFn != nil {
			errFn(err)
		}
		return NullAgentModelLookup
	}
	return cfg.AgentModel
}
