package discovery

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// AgentInfo describes a discovered, active agent.
type AgentInfo struct {
	// ID is the agent identifier (e.g. "sdd-apply", "build").
	ID string
	// Type is one of "built-in", "primary", "subagent", "all".
	Type string
	// Source describes where the agent was found (for debugging).
	Source string
}

// systemAgents are internal OpenCode agents that must never be surfaced.
var systemAgents = map[string]bool{
	"compaction": true,
	"title":      true,
	"summary":    true,
}

// builtinAgents are the four hardcoded agents, always present unless
// explicitly disabled by a project/global config.
var builtinAgents = []string{"build", "plan", "general", "explore"}

// opencodeAgentConfig mirrors the per-agent block inside opencode.json.
type opencodeAgentConfig struct {
	Disable bool   `json:"disable"`
	Hidden  bool   `json:"hidden"`
	Mode    string `json:"mode"`
}

// opencodeRootConfig is the shape of the top-level opencode.json file.
type opencodeRootConfig struct {
	Agent map[string]opencodeAgentConfig `json:"agent"`
}

// agentState tracks the merged state of an agent across all config sources.
type agentState struct {
	disabled bool
	hidden   bool
	mode     string // "" = not yet seen in JSON
	source   string
}

// DiscoverAgents returns all active (non-disabled, non-system — hidden agents are
// included since hidden only affects @ autocomplete visibility) agents from the
// following sources, in priority order (project overrides global):
//
//  1. Built-in agents: build, plan, general, explore
//  2. Global JSON: ~/.config/opencode/opencode.json → agent section
//  3. Global Markdown: ~/.config/opencode/agents/*.md
//  4. Project JSON: opencode.json in workDir and parents up to git root
//  5. Project Markdown: .opencode/agents/*.md in workDir
//
// If workDir is empty, sources 4 and 5 are skipped.
func DiscoverAgents(workDir string) []AgentInfo {
	// state map: agentID → merged state
	states := make(map[string]*agentState)

	// --- 1. Seed built-in agents ---
	for _, id := range builtinAgents {
		states[id] = &agentState{mode: "built-in", source: "built-in"}
	}

	// --- 2. Global JSON ---
	if home, err := os.UserHomeDir(); err == nil {
		globalJSON := filepath.Join(home, ".config", "opencode", "opencode.json")
		mergeFromJSONConfig(globalJSON, "global-json", states)
	}

	// --- 3. Global Markdown ---
	if home, err := os.UserHomeDir(); err == nil {
		globalMD := filepath.Join(home, ".config", "opencode", "agents")
		mergeFromMarkdownDir(globalMD, "global-md", states)
	}

	// --- 4. Project JSON (walk up to git root) ---
	if workDir != "" {
		projectJSONDirs := collectProjectDirs(workDir)
		// Reverse so git root is processed first, workDir last (workDir takes priority).
		for i, j := 0, len(projectJSONDirs)-1; i < j; i, j = i+1, j-1 {
			projectJSONDirs[i], projectJSONDirs[j] = projectJSONDirs[j], projectJSONDirs[i]
		}
		for _, dir := range projectJSONDirs {
			p := filepath.Join(dir, "opencode.json")
			mergeFromJSONConfig(p, "project-json", states)
		}
	}

	// --- 5. Project Markdown ---
	if workDir != "" {
		projectMD := filepath.Join(workDir, ".opencode", "agents")
		mergeFromMarkdownDir(projectMD, "project-md", states)
	}

	// --- Build result list ---
	var result []AgentInfo
	for id, st := range states {
		// Always exclude internal system agents
		if systemAgents[id] {
			continue
		}
		// Exclude explicitly disabled agents
		if st.disabled {
			continue
		}
		// hidden: true only means "don't show in @ autocomplete" per OpenCode docs
		// We still benchmark hidden subagents — they are active, just not user-visible
		// Only exclude hidden built-ins (compaction/title/summary already covered above)
		agentType := resolveType(id, st.mode)
		result = append(result, AgentInfo{
			ID:     id,
			Type:   agentType,
			Source: st.source,
		})
	}
	return result
}

// resolveType converts an agent's raw mode string into the canonical Type value.
func resolveType(id, mode string) string {
	switch mode {
	case "built-in":
		return "built-in"
	case "primary":
		return "primary"
	case "subagent":
		return "subagent"
	case "all":
		return "all"
	default:
		// Built-in sentinel overrides
		for _, b := range builtinAgents {
			if b == id {
				return "built-in"
			}
		}
		// Unknown / not specified — treat as primary
		return "primary"
	}
}

// mergeFromJSONConfig reads an opencode.json file and updates the agent state map.
// Last source wins — later calls overwrite earlier ones. Call order determines priority.
func mergeFromJSONConfig(path, source string, states map[string]*agentState) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg opencodeRootConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}
	for name, ac := range cfg.Agent {
		if systemAgents[name] {
			continue
		}
		if existing, ok := states[name]; ok {
			// Project entries override global (project sources come after global).
			// We overwrite regardless because later sources win.
			existing.disabled = ac.Disable
			existing.hidden = ac.Hidden
			if ac.Mode != "" {
				existing.mode = ac.Mode
			}
			existing.source = source
		} else {
			states[name] = &agentState{
				disabled: ac.Disable,
				hidden:   ac.Hidden,
				mode:     ac.Mode,
				source:   source,
			}
		}
	}
}

// mergeFromMarkdownDir scans a directory for *.md files and parses YAML
// frontmatter in each to discover agent configurations.
func mergeFromMarkdownDir(dir, source string, states map[string]*agentState) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		agentID := strings.TrimSuffix(name, filepath.Ext(name))
		if systemAgents[agentID] {
			continue
		}
		path := filepath.Join(dir, name)
		disabled, hidden, mode := parseAgentMarkdown(path)

		if existing, ok := states[agentID]; ok {
			existing.disabled = disabled
			existing.hidden = hidden
			if mode != "" {
				existing.mode = mode
			}
			existing.source = source
		} else {
			states[agentID] = &agentState{
				disabled: disabled,
				hidden:   hidden,
				mode:     mode,
				source:   source,
			}
		}
	}
}

// parseAgentMarkdown reads a markdown file and extracts disable, hidden, and
// mode from its YAML frontmatter (the --- delimited block at the top of the file).
// Only the three fields we care about are parsed; no external YAML library is needed.
func parseAgentMarkdown(path string) (disabled bool, hidden bool, mode string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Look for opening ---
	if !scanner.Scan() {
		return
	}
	firstLine := strings.TrimSpace(scanner.Text())
	if firstLine != "---" {
		return
	}

	// Read until closing ---
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, val, ok := parseFrontmatterLine(line)
		if !ok {
			continue
		}
		switch key {
		case "disable", "disabled":
			disabled = isYAMLTrue(val)
		case "hidden":
			hidden = isYAMLTrue(val)
		case "mode":
			mode = strings.ToLower(strings.TrimSpace(val))
		}
	}
	return
}

// parseFrontmatterLine parses a single "key: value" YAML line.
// Returns (key, value, true) or ("", "", false) if the line is not a simple key-value.
func parseFrontmatterLine(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// isYAMLTrue returns true for the YAML boolean true values.
func isYAMLTrue(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// collectProjectDirs returns the directories from workDir up to (and including)
// the git root directory. If no git root is found, only workDir is returned.
func collectProjectDirs(workDir string) []string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return []string{workDir}
	}

	var dirs []string
	current := abs
	for {
		dirs = append(dirs, current)
		// Check if this directory is the git root.
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root without finding .git
			break
		}
		current = parent
	}
	return dirs
}
