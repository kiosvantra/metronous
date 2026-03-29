package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultAgentsDir returns the default agents directory: ~/.opencode/agents/
func DefaultAgentsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".opencode/agents"
	}
	return filepath.Join(home, ".opencode", "agents")
}

// Registry maintains an in-memory map of discovered agents indexed by ID.
// It is safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*AgentConfig
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]*AgentConfig),
	}
}

// Register adds or replaces an agent in the registry.
func (r *Registry) Register(agent *AgentConfig) error {
	if agent == nil {
		return fmt.Errorf("agent must not be nil")
	}
	if agent.ID == "" {
		return fmt.Errorf("agent ID must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agent.ID] = agent
	return nil
}

// Unregister removes an agent by ID. It is a no-op if the agent is unknown.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, id)
}

// Get returns the AgentConfig for the given ID, if present.
func (r *Registry) Get(id string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	return a, ok
}

// List returns a snapshot of all registered agents (order is not guaranteed).
func (r *Registry) List() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AgentConfig, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}

// LoadFromDisk scans the given directory for agent subdirectories, parses each
// one, and registers the resulting AgentConfig. Directories that do not contain
// a recognised config file are silently skipped.
func (r *Registry) LoadFromDisk(agentsDir string) error {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory does not exist yet — treat as empty.
			return nil
		}
		return fmt.Errorf("read agents dir %s: %w", agentsDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(agentsDir, e.Name())
		cfg, err := ParseAgentDirectory(dir)
		if err != nil {
			// Unrecognised or malformed — skip.
			continue
		}
		_ = r.Register(cfg)
	}
	return nil
}

// UnregisterByPath removes any agent whose SourcePath starts with the given
// directory prefix. This is used when a directory is deleted.
func (r *Registry) UnregisterByPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, a := range r.agents {
		if a.SourcePath == path || isUnderDir(a.SourcePath, path) {
			delete(r.agents, id)
		}
	}
}

// isUnderDir returns true if file is inside dir.
func isUnderDir(file, dir string) bool {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return false
	}
	return len(rel) > 0 && rel[0] != '.'
}
