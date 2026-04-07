package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/kiosvantra/metronous/internal/config"
)

// ConfigSavedMsg is sent after a successful save.
// Exported so tests can inject it.
type ConfigSavedMsg struct{}

// configSavedMsg is an internal alias.
type configSavedMsg = ConfigSavedMsg

// ConfigReloadedMsg is sent after a successful reload.
// Exported so tests can inject it.
type ConfigReloadedMsg struct {
	Thresholds config.Thresholds
	Schedule   string
	WindowDays int
}

// configReloadedMsg is an internal alias.
type configReloadedMsg = ConfigReloadedMsg

// ConfigErrMsg is sent when a save or reload fails.
// Exported so tests can inject it.
type ConfigErrMsg struct{ Err error }

// configErrMsg is an internal alias.
type configErrMsg = ConfigErrMsg

// fieldType defines the type of a configuration field.
type fieldType int

const (
	fieldTypeFloat fieldType = iota
	fieldTypeInt
	fieldTypeString
)

// configField describes an editable threshold field.
type configField struct {
	label       string
	key         string // matches the JSON key in DefaultThresholds or nested paths
	fieldType   fieldType
	step        float64
	min         float64
	max         float64
	description string // English description shown when cursor is on this field
}

var configFields = []configField{
	// Decision thresholds - defaults
	{
		label:       "Min Accuracy",
		key:         "min_accuracy",
		fieldType:   fieldTypeFloat,
		step:        0.01,
		min:         0,
		max:         1,
		description: "If an agent's accuracy drops below this value, Metronous recommends switching to a better model.",
	},
	{
		label:       "Min ROI Score",
		key:         "min_roi_score",
		fieldType:   fieldTypeFloat,
		step:        0.01,
		min:         0,
		max:         10.0,
		description: "For paid models, this is the minimum value-for-money score. If results fall below it, Metronous recommends a cheaper option.",
	},
	{
		label:       "Max Cost/Session (USD)",
		key:         "max_cost_usd_per_session",
		fieldType:   fieldTypeFloat,
		step:        0.50,
		min:         0,
		max:         100,
		description: "If one session costs more than this, Metronous flags it as expensive in the tracking and benchmark views.",
	},
	// Urgent triggers
	{
		label:       "Urgent Min Accuracy",
		key:         "urgent_min_accuracy",
		fieldType:   fieldTypeFloat,
		step:        0.01,
		min:         0,
		max:         1,
		description: "Emergency accuracy floor. If an agent falls below this, Metronous jumps straight to an urgent switch recommendation.",
	},
	{
		label:       "Urgent Max Error Rate",
		key:         "urgent_max_error_rate",
		fieldType:   fieldTypeFloat,
		step:        0.01,
		min:         0,
		max:         1,
		description: "Emergency error ceiling. If errors go above this, Metronous treats the model as unsafe and recommends an urgent switch.",
	},
	{
		label:       "Urgent Cost Spike Multiplier",
		key:         "urgent_max_cost_spike_multiplier",
		fieldType:   fieldTypeFloat,
		step:        0.1,
		min:         1,
		max:         10,
		description: "If a run costs this many times more than its usual baseline, Metronous raises an urgent cost warning.",
	},
	// Scheduler settings
	{
		label:       "Benchmark Schedule",
		key:         "scheduler_benchmark_schedule",
		fieldType:   fieldTypeString,
		description: "The cron schedule that tells the daemon when to run the weekly benchmark automatically.",
	},
	{
		label:       "Window Days",
		key:         "scheduler_window_days",
		fieldType:   fieldTypeInt,
		step:        1,
		min:         1,
		max:         30,
		description: "How many recent days of activity each benchmark run should look at.",
	},
}

const (
	defaultBenchmarkSchedule = "0 0 2 * * 0"
	defaultWindowDays        = 7
)

var (
	fieldActiveStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	fieldInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	validStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	invalidStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	saveStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	legendStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
)

// ConfigModel is the Bubble Tea sub-model for the threshold config editor.
type ConfigModel struct {
	configPath string
	thresholds config.Thresholds
	schedule   string
	windowDays int
	cursor     int
	statusMsg  string
	statusOK   bool
	loaded     bool
}

// NewConfigModel creates a ConfigModel that reads/writes configPath.
func NewConfigModel(configPath string) ConfigModel {
	return ConfigModel{
		configPath: configPath,
		thresholds: config.DefaultThresholdValues(),
		schedule:   defaultBenchmarkSchedule,
		windowDays: defaultWindowDays,
	}
}

// Init loads thresholds from disk.
func (m ConfigModel) Init() tea.Cmd {
	return m.reloadCmd()
}

// Update handles key presses for navigation and value adjustment.
func (m ConfigModel) Update(msg tea.Msg) (ConfigModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		if m.thresholds.EffectiveKeymapPreset() == config.KeymapPresetNvim {
			// In nvim preset, map h/l to left/right for config adjustments.
			switch key {
			case "h":
				key = "left"
			case "l":
				key = "right"
			}
		}

		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			// The last cursor position is reserved for the keymap preset row.
			maxCursor := len(configFields)
			if m.cursor < maxCursor {
				m.cursor++
			}
		case "right", "+", "=":
			if m.cursor == len(configFields) {
				m.toggleKeymapPreset(+1)
			} else {
				m.adjustCurrent(+1)
			}
		case "left", "-":
			if m.cursor == len(configFields) {
				m.toggleKeymapPreset(-1)
			} else {
				m.adjustCurrent(-1)
			}
		}

	case configSavedMsg:
		m.statusMsg = "✓ Saved"
		m.statusOK = true
		// After a successful save, automatically reload thresholds so that all
		// tabs see the updated configuration without requiring a manual reload.
		cmd = m.reloadCmd()

	case configReloadedMsg:
		m.thresholds = msg.Thresholds
		if msg.Schedule != "" {
			m.schedule = msg.Schedule
		}
		if msg.WindowDays > 0 {
			m.windowDays = msg.WindowDays
		}
		m.loaded = true
		m.statusMsg = "✓ Reloaded"
		m.statusOK = true

	case configErrMsg:
		m.statusMsg = fmt.Sprintf("✗ Error: %v", msg.Err)
		m.statusOK = false
	}
	return m, cmd
}

// UpdateSave handles ctrl+s saves.
func (m ConfigModel) UpdateSave(msg tea.KeyMsg) (ConfigModel, tea.Cmd) {
	return m, m.saveCmd()
}

// UpdateReload handles ctrl+r reloads.
func (m ConfigModel) UpdateReload(msg tea.KeyMsg) (ConfigModel, tea.Cmd) {
	return m, m.reloadCmd()
}

// adjustCurrent modifies the currently selected field value by ±step.
func (m *ConfigModel) adjustCurrent(dir float64) {
	if m.cursor >= len(configFields) {
		return
	}
	f := configFields[m.cursor]

	switch f.fieldType {
	case fieldTypeFloat:
		v := m.getFloatValue(f.key)
		v += dir * f.step
		if v < f.min {
			v = f.min
		}
		if v > f.max {
			v = f.max
		}
		m.setFloatValue(f.key, v)
	case fieldTypeInt:
		v := m.getIntValue(f.key)
		v += int(dir * f.step)
		if v < int(f.min) {
			v = int(f.min)
		}
		if v > int(f.max) {
			v = int(f.max)
		}
		m.setIntValue(f.key, v)
	case fieldTypeString:
		// String fields are not adjustable with arrow keys
		// They would need a separate edit mode
	}
}

// getFloatValue returns the current float64 value for a field key.
func (m *ConfigModel) getFloatValue(key string) float64 {
	switch key {
	case "min_accuracy":
		return m.thresholds.Defaults.MinAccuracy
	case "min_roi_score":
		return m.thresholds.Defaults.MinROIScore
	case "max_cost_usd_per_session":
		return m.thresholds.Defaults.MaxCostUSDPerSession
	case "urgent_min_accuracy":
		return m.thresholds.UrgentTriggers.MinAccuracy
	case "urgent_max_error_rate":
		return m.thresholds.UrgentTriggers.MaxErrorRate
	case "urgent_max_cost_spike_multiplier":
		return m.thresholds.UrgentTriggers.MaxCostSpikeMultiplier
	}
	return 0
}

// setFloatValue updates a field value given a float64.
func (m *ConfigModel) setFloatValue(key string, v float64) {
	switch key {
	case "min_accuracy":
		m.thresholds.Defaults.MinAccuracy = v
	case "min_roi_score":
		m.thresholds.Defaults.MinROIScore = v
	case "max_cost_usd_per_session":
		m.thresholds.Defaults.MaxCostUSDPerSession = v
	case "urgent_min_accuracy":
		m.thresholds.UrgentTriggers.MinAccuracy = v
	case "urgent_max_error_rate":
		m.thresholds.UrgentTriggers.MaxErrorRate = v
	case "urgent_max_cost_spike_multiplier":
		m.thresholds.UrgentTriggers.MaxCostSpikeMultiplier = v
	}
}

// getIntValue returns the current int value for a field key.
func (m *ConfigModel) getIntValue(key string) int {
	switch key {
	case "scheduler_window_days":
		return m.windowDays
	}
	return 0
}

// setIntValue updates a field value given an int.
func (m *ConfigModel) setIntValue(key string, v int) {
	switch key {
	case "scheduler_window_days":
		m.windowDays = v
	}
}

// getStringValue returns the current string value for a field key.
func (m *ConfigModel) getStringValue(key string) string {
	switch key {
	case "model_accuracy":
		return m.thresholds.ModelRecommendations.AccuracyModel
	case "model_performance":
		return m.thresholds.ModelRecommendations.PerformanceModel
	case "model_default":
		return m.thresholds.ModelRecommendations.DefaultModel
	case "scheduler_benchmark_schedule":
		return m.schedule
	}
	return ""
}

// setStringValue updates a field value given a string.
func (m *ConfigModel) setStringValue(key string, v string) {
	switch key {
	case "model_accuracy":
		m.thresholds.ModelRecommendations.AccuracyModel = v
	case "model_performance":
		m.thresholds.ModelRecommendations.PerformanceModel = v
	case "model_default":
		m.thresholds.ModelRecommendations.DefaultModel = v
	case "scheduler_benchmark_schedule":
		m.schedule = v
	}
}

// toggleKeymapPreset cycles the keymap preset between the supported values.
// dir should be +1 or -1.
func (m *ConfigModel) toggleKeymapPreset(dir int) {
	presets := []config.KeymapPreset{config.KeymapPresetDefault, config.KeymapPresetNvim}
	current := m.thresholds.EffectiveKeymapPreset()
	idx := 0
	for i, p := range presets {
		if p == current {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(presets)) % len(presets)
	m.thresholds.KeymapPreset = presets[idx]
}

// saveCmd returns a tea.Cmd that writes the current thresholds to disk atomically.
// It uses a temp-file → fsync → rename pattern to prevent config corruption on crash.
func (m ConfigModel) saveCmd() tea.Cmd {
	return func() tea.Msg {
		// Save thresholds.json
		if err := m.saveThresholds(); err != nil {
			return ConfigErrMsg{Err: err}
		}

		// Save config.yaml
		if err := m.saveConfigYAML(); err != nil {
			return ConfigErrMsg{Err: err}
		}

		return ConfigSavedMsg{}
	}
}

// saveThresholds writes thresholds.json atomically.
func (m ConfigModel) saveThresholds() error {
	path := m.configPath
	if path == "" {
		return fmt.Errorf("no config path set")
	}

	// Validate thresholds before writing (Issue 8).
	if err := validateThresholds(m.thresholds); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	data, err := json.MarshalIndent(m.thresholds, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Atomic write: temp → fsync → rename.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".thresholds-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename to target: %w", err)
	}

	return nil
}

// saveConfigYAML writes config.yaml atomically.
func (m ConfigModel) saveConfigYAML() error {
	// Derive config.yaml path from thresholds.json path
	configDir := filepath.Dir(m.configPath)
	configPath := filepath.Join(configDir, "config.yaml")

	type schedulerConfig struct {
		BenchmarkSchedule string `yaml:"benchmark_schedule"`
		WindowDays        int    `yaml:"window_days"`
	}

	type appConfig struct {
		Scheduler schedulerConfig `yaml:"scheduler"`
	}

	cfg := appConfig{
		Scheduler: schedulerConfig{
			BenchmarkSchedule: m.schedule,
			WindowDays:        m.windowDays,
		},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Atomic write: temp → fsync → rename.
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename to target: %w", err)
	}

	return nil
}

// reloadCmd returns a tea.Cmd that reads thresholds from disk.
func (m ConfigModel) reloadCmd() tea.Cmd {
	return func() tea.Msg {
		// Reload thresholds.json
		path := m.configPath
		if path == "" {
			// Return defaults when no path is configured.
			return ConfigReloadedMsg{Thresholds: config.DefaultThresholdValues(), Schedule: defaultBenchmarkSchedule, WindowDays: defaultWindowDays}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return ConfigReloadedMsg{Thresholds: config.DefaultThresholdValues(), Schedule: defaultBenchmarkSchedule, WindowDays: defaultWindowDays}
			}
			return ConfigErrMsg{Err: err}
		}
		var t config.Thresholds
		if err := json.Unmarshal(data, &t); err != nil {
			return ConfigErrMsg{Err: err}
		}

		// Reload config.yaml
		configDir := filepath.Dir(path)
		configPath := filepath.Join(configDir, "config.yaml")
		schedule := defaultBenchmarkSchedule
		windowDays := defaultWindowDays

		if yamlData, err := os.ReadFile(configPath); err == nil {
			type schedulerConfig struct {
				BenchmarkSchedule string `yaml:"benchmark_schedule"`
				WindowDays        int    `yaml:"window_days"`
			}
			type appConfig struct {
				Scheduler schedulerConfig `yaml:"scheduler"`
			}
			var cfg appConfig
			if err := yaml.Unmarshal(yamlData, &cfg); err == nil {
				if cfg.Scheduler.BenchmarkSchedule != "" {
					schedule = cfg.Scheduler.BenchmarkSchedule
				}
				if cfg.Scheduler.WindowDays > 0 {
					windowDays = cfg.Scheduler.WindowDays
				}
			}
		}

		// Update the model with loaded values
		m.thresholds = t
		m.schedule = schedule
		m.windowDays = windowDays

		return ConfigReloadedMsg{Thresholds: t, Schedule: schedule, WindowDays: windowDays}
	}
}

// View renders the config editor tab.
func (m ConfigModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Configuration") + "\n\n")
	sb.WriteString(dimStyle.Render("  ↑/↓ or j/k: move between fields") + "\n")
	sb.WriteString(dimStyle.Render("  ←/→ or +/-: adjust value  s / ctrl+s: save  r / ctrl+r: reload") + "\n")
	sb.WriteString(dimStyle.Render("  Thresholds affect the daemon and benchmark pipeline; the keymap only changes how this screen behaves. Model recommendation policy is system-managed and not editable here.") + "\n\n")

	// Decision thresholds section.
	sb.WriteString(dimStyle.Render("Decision thresholds:") + "\n")

	for i, f := range configFields {
		var valRendered string
		var valid bool

		switch f.fieldType {
		case fieldTypeFloat:
			v := m.getFloatValue(f.key)
			valid = v >= f.min && v <= f.max
			valStr := formatConfigValue(f.key, v)
			if valid {
				valRendered = validStyle.Render(valStr)
			} else {
				valRendered = invalidStyle.Render(valStr + " (invalid)")
			}
		case fieldTypeInt:
			v := m.getIntValue(f.key)
			valid = float64(v) >= f.min && float64(v) <= f.max
			valStr := formatConfigValue(f.key, float64(v))
			if valid {
				valRendered = validStyle.Render(valStr)
			} else {
				valRendered = invalidStyle.Render(valStr + " (invalid)")
			}
		case fieldTypeString:
			v := m.getStringValue(f.key)
			if v == "" {
				valRendered = dimStyle.Render("(not set)")
			} else {
				valRendered = validStyle.Render(v)
			}
			valid = true
		}

		label := fmt.Sprintf("  %-28s  %s", f.label, valRendered)
		if i == m.cursor {
			sb.WriteString(fieldActiveStyle.Render("▶ " + strings.TrimLeft(label, " ")))
		} else {
			sb.WriteString(fieldInactiveStyle.Render(label))
		}
		sb.WriteString("\n")
	}

	// UI preferences section.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("UI preferences:") + "\n")

	// Keymap preset row.
	keymapPreset := m.thresholds.EffectiveKeymapPreset()
	var keymapLabel string
	switch keymapPreset {
	case config.KeymapPresetNvim:
		keymapLabel = "Nvim (hjkl navigation)"
	default:
		keymapLabel = "Default (numbers/arrows)"
	}
	row := fmt.Sprintf("  %-28s  %s", "Keymap preset", keymapLabel)
	if m.cursor == len(configFields) {
		sb.WriteString(fieldActiveStyle.Render("▶ " + strings.TrimLeft(row, " ")))
	} else {
		sb.WriteString(fieldInactiveStyle.Render(row))
	}
	sb.WriteString("\n")

	// Dynamic legend for the currently selected field.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("What this setting means:") + "\n")
	if m.cursor < len(configFields) {
		f := configFields[m.cursor]
		// Word wrap the description at 80 characters
		wrapped := wordWrap(f.description, 76)
		for _, line := range wrapped {
			sb.WriteString(legendStyle.Render("  "+line) + "\n")
		}
	} else {
		sb.WriteString(legendStyle.Render("  Keymap preset: 'Default' keeps original keybindings; 'Nvim' enables hjkl navigation.") + "\n")
	}

	// Per-agent overrides count.
	if len(m.thresholds.PerAgent) > 0 {
		sb.WriteString("\n")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  Per-agent overrides: %d agent(s)", len(m.thresholds.PerAgent))))
		sb.WriteString("\n")
	}

	// Status message.
	if m.statusMsg != "" {
		sb.WriteString("\n")
		if m.statusOK {
			sb.WriteString(saveStyle.Render("  " + m.statusMsg))
		} else {
			sb.WriteString(errStyle.Render("  " + m.statusMsg))
		}
		sb.WriteString("\n")
	}

	if m.configPath != "" {
		sb.WriteString("\n")
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  Config: %s", m.configPath)))
		sb.WriteString("\n")
	}

	return sb.String()
}

// wordWrap wraps text to a maximum width.
func wordWrap(text string, maxWidth int) []string {
	var lines []string
	words := strings.Fields(text)
	currentLine := ""
	currentLength := 0

	for _, word := range words {
		if currentLength == 0 {
			currentLine = word
			currentLength = len(word)
		} else if currentLength+1+len(word) <= maxWidth {
			currentLine += " " + word
			currentLength += 1 + len(word)
		} else {
			lines = append(lines, currentLine)
			currentLine = word
			currentLength = len(word)
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// validateThresholds checks that all threshold values are within their valid ranges.
// Returns a non-nil error describing the first invalid field.
func validateThresholds(t config.Thresholds) error {
	d := t.Defaults
	if d.MinAccuracy < 0 || d.MinAccuracy > 1.0 {
		return fmt.Errorf("min_accuracy %.4f is outside valid range [0, 1]", d.MinAccuracy)
	}
	if d.MinROIScore < 0 {
		return fmt.Errorf("min_roi_score %.4f must be >= 0", d.MinROIScore)
	}
	if d.MaxCostUSDPerSession < 0 {
		return fmt.Errorf("max_cost_usd_per_session %.4f must be >= 0", d.MaxCostUSDPerSession)
	}

	u := t.UrgentTriggers
	if u.MinAccuracy < 0 || u.MinAccuracy > 1.0 {
		return fmt.Errorf("urgent_min_accuracy %.4f is outside valid range [0, 1]", u.MinAccuracy)
	}
	if u.MaxErrorRate < 0 || u.MaxErrorRate > 1.0 {
		return fmt.Errorf("urgent_max_error_rate %.4f is outside valid range [0, 1]", u.MaxErrorRate)
	}
	if u.MaxCostSpikeMultiplier < 1 {
		return fmt.Errorf("urgent_max_cost_spike_multiplier %.4f must be >= 1", u.MaxCostSpikeMultiplier)
	}

	return nil
}

// GetCurrentFieldValue returns the value of the currently selected field.
// Exposed for testing.
func (m *ConfigModel) GetCurrentFieldValue() float64 {
	if m.cursor >= len(configFields) {
		return 0
	}
	f := configFields[m.cursor]
	if f.fieldType == fieldTypeFloat {
		return m.getFloatValue(f.key)
	}
	return 0
}

// formatConfigValue formats a field value for display.
func formatConfigValue(key string, v float64) string {
	switch key {
	case "max_latency_p95_ms":
		return fmt.Sprintf("%dms", int(v))
	case "max_cost_usd_per_session":
		return fmt.Sprintf("$%.2f", v)
	case "min_roi_score":
		return fmt.Sprintf("%.3f", v)
	case "urgent_max_cost_spike_multiplier":
		return fmt.Sprintf("%.1fx", v)
	case "scheduler_window_days":
		return fmt.Sprintf("%d days", int(v))
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
