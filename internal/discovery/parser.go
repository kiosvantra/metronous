package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rewriteModel reads the agent config at path, replaces the model field with
// newModel, writes the file back atomically (temp → fsync → rename), and
// returns the updated AgentConfig. Atomic write prevents config corruption on crash.
func rewriteModel(path, newModel string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Unmarshal into a generic map so we can update a single field without
	// losing unknown fields.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	raw["model"] = newModel
	// Clear the alternate field if present.
	delete(raw, "model_id")

	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}
	updated = append(updated, '\n')

	// Atomic write: write to a temp file in the same directory, fsync, then rename.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".rewrite-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on failure.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write temp file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("fsync temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temp file for %s: %w", path, err)
	}

	// Set target file permissions before rename.
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return nil, fmt.Errorf("chmod temp file for %s: %w", path, err)
	}

	// Atomic rename: on POSIX systems this is guaranteed atomic.
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	success = true

	return ParseAgentConfig(path)
}

// AgentConfig holds the parsed configuration for a discovered agent.
type AgentConfig struct {
	// ID is a unique identifier for this agent (derived from directory name if empty).
	ID string `json:"id"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// Model is the current LLM model identifier (e.g. "claude-sonnet-4-5").
	Model string `json:"model"`
	// Description is an optional summary of what the agent does.
	Description string `json:"description"`
	// SourcePath is the path from which this config was loaded.
	SourcePath string `json:"-"`
}

// openCodeConfig is the shape of ~/.opencode/agents/<name>/opencode.json.
type openCodeConfig struct {
	// Top-level fields (opencode.json direct format).
	ID          string `json:"id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Description string `json:"description"`

	// Nested model field (alternate format used by some opencode versions).
	ModelID string `json:"model_id"`
}

// ParseAgentConfig parses an agent configuration file.
// It supports:
//   - opencode.json (JSON with id/name/model/description)
//   - agent.yaml / agent.json variants (same JSON structure)
//
// The id is derived from the parent directory name when the file does not
// provide one.
func ParseAgentConfig(path string) (*AgentConfig, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return parseJSONConfig(path)
	default:
		return nil, fmt.Errorf("unsupported agent config format: %s", ext)
	}
}

// ParseAgentDirectory scans a directory for a recognised agent config file
// and parses it. It looks for opencode.json first, then agent.json.
func ParseAgentDirectory(dir string) (*AgentConfig, error) {
	candidates := []string{
		filepath.Join(dir, "opencode.json"),
		filepath.Join(dir, "agent.json"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return ParseAgentConfig(c)
		}
	}
	return nil, fmt.Errorf("no agent config file found in %s", dir)
}

// parseJSONConfig parses a JSON agent config file.
func parseJSONConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw openCodeConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg := &AgentConfig{
		ID:          raw.ID,
		Name:        raw.Name,
		Model:       raw.Model,
		Description: raw.Description,
		SourcePath:  path,
	}

	// Fall back to model_id if model is empty.
	if cfg.Model == "" && raw.ModelID != "" {
		cfg.Model = raw.ModelID
	}

	// Derive ID from parent directory name if not specified.
	if cfg.ID == "" {
		cfg.ID = filepath.Base(filepath.Dir(path))
		if cfg.ID == "." {
			cfg.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}

	// Derive name from ID if not specified.
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}

	return cfg, nil
}
