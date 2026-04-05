package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/config"
)

// ConfigSavedMsg is sent after a successful save.
// Exported so tests can inject it.
type ConfigSavedMsg struct{}

// configSavedMsg is an internal alias.
type configSavedMsg = ConfigSavedMsg

// ConfigReloadedMsg is sent after a successful reload.
// Exported so tests can inject it.
type ConfigReloadedMsg struct{ Thresholds config.Thresholds }

// configReloadedMsg is an internal alias.
type configReloadedMsg = ConfigReloadedMsg

// ConfigErrMsg is sent when a save or reload fails.
// Exported so tests can inject it.
type ConfigErrMsg struct{ Err error }

// configErrMsg is an internal alias.
type configErrMsg = ConfigErrMsg

// configField describes an editable threshold field.
type configField struct {
	label string
	key   string // matches the JSON key in DefaultThresholds
	step  float64
	min   float64
	max   float64
}

var configFields = []configField{
	{label: "Min Accuracy", key: "min_accuracy", step: 0.01, min: 0, max: 1},
	{label: "Min ROI Score", key: "min_roi_score", step: 0.01, min: 0, max: 10.0},
	{label: "Max Cost/Session (USD)", key: "max_cost_usd_per_session", step: 0.50, min: 0, max: 100},
}

var (
	fieldActiveStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	fieldInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	validStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	invalidStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	saveStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
)

// ConfigModel is the Bubble Tea sub-model for the threshold config editor.
type ConfigModel struct {
	configPath string
	thresholds config.Thresholds
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
	}
}

// Init loads thresholds from disk.
func (m ConfigModel) Init() tea.Cmd {
	return m.reloadCmd()
}

// Update handles key presses for navigation and value adjustment.
func (m ConfigModel) Update(msg tea.Msg) (ConfigModel, tea.Cmd) {
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

	case configReloadedMsg:
		m.thresholds = msg.Thresholds
		m.loaded = true
		m.statusMsg = "✓ Reloaded"
		m.statusOK = true

	case configErrMsg:
		m.statusMsg = fmt.Sprintf("✗ Error: %v", msg.Err)
		m.statusOK = false
	}
	return m, nil
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
	v := m.getFieldValue(f.key)
	v += dir * f.step
	if v < f.min {
		v = f.min
	}
	if v > f.max {
		v = f.max
	}
	m.setFieldValue(f.key, v)
}

// getFieldValue returns the current float64 value for a field key.
func (m *ConfigModel) getFieldValue(key string) float64 {
	switch key {
	case "min_accuracy":
		return m.thresholds.Defaults.MinAccuracy
	case "min_roi_score":
		return m.thresholds.Defaults.MinROIScore
	case "max_cost_usd_per_session":
		return m.thresholds.Defaults.MaxCostUSDPerSession
	}
	return 0
}

// setFieldValue updates a field value given a float64.
func (m *ConfigModel) setFieldValue(key string, v float64) {
	switch key {
	case "min_accuracy":
		m.thresholds.Defaults.MinAccuracy = v
	case "min_roi_score":
		m.thresholds.Defaults.MinROIScore = v
	case "max_cost_usd_per_session":
		m.thresholds.Defaults.MaxCostUSDPerSession = v
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
		path := m.configPath
		if path == "" {
			return ConfigErrMsg{Err: fmt.Errorf("no config path set")}
		}

		// Validate thresholds before writing (Issue 8).
		if err := validateThresholds(m.thresholds); err != nil {
			return ConfigErrMsg{Err: fmt.Errorf("validation: %w", err)}
		}

		data, err := json.MarshalIndent(m.thresholds, "", "  ")
		if err != nil {
			return ConfigErrMsg{Err: err}
		}
		data = append(data, '\n')

		// Atomic write: temp → fsync → rename.
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return ConfigErrMsg{Err: fmt.Errorf("create config dir: %w", err)}
		}
		tmp, err := os.CreateTemp(dir, ".thresholds-*.tmp")
		if err != nil {
			return ConfigErrMsg{Err: fmt.Errorf("create temp file: %w", err)}
		}
		tmpPath := tmp.Name()

		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return ConfigErrMsg{Err: fmt.Errorf("write temp file: %w", err)}
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return ConfigErrMsg{Err: fmt.Errorf("fsync temp file: %w", err)}
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return ConfigErrMsg{Err: fmt.Errorf("close temp file: %w", err)}
		}
		if err := os.Chmod(tmpPath, 0600); err != nil {
			_ = os.Remove(tmpPath)
			return ConfigErrMsg{Err: fmt.Errorf("chmod temp file: %w", err)}
		}
		if err := os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			return ConfigErrMsg{Err: fmt.Errorf("rename to target: %w", err)}
		}

		return ConfigSavedMsg{}
	}
}

// reloadCmd returns a tea.Cmd that reads thresholds from disk.
func (m ConfigModel) reloadCmd() tea.Cmd {
	return func() tea.Msg {
		path := m.configPath
		if path == "" {
			// Return defaults when no path is configured.
			return ConfigReloadedMsg{Thresholds: config.DefaultThresholdValues()}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return ConfigReloadedMsg{Thresholds: config.DefaultThresholdValues()}
			}
			return ConfigErrMsg{Err: err}
		}
		var t config.Thresholds
		if err := json.Unmarshal(data, &t); err != nil {
			return ConfigErrMsg{Err: err}
		}
		return ConfigReloadedMsg{Thresholds: t}
	}
}

// View renders the config editor tab.
func (m ConfigModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Threshold Configuration") + "\n\n")
	sb.WriteString(dimStyle.Render("  ↑/↓: select field  ←/→ or +/-: adjust value  s: save  r: reload") + "\n")
	sb.WriteString(dimStyle.Render("  Keymap preset: Default (numbers/arrows) or Nvim (hjkl) via the Config tab") + "\n\n")

	for i, f := range configFields {
		v := m.getFieldValue(f.key)

		// Validation.
		valid := v >= f.min && v <= f.max
		valStr := formatConfigValue(f.key, v)
		var valRendered string
		if valid {
			valRendered = validStyle.Render(valStr)
		} else {
			valRendered = invalidStyle.Render(valStr + " (invalid)")
		}

		label := fmt.Sprintf("  %-28s  %s", f.label, valRendered)
		if i == m.cursor {
			sb.WriteString(fieldActiveStyle.Render("▶ " + strings.TrimLeft(label, " ")))
		} else {
			sb.WriteString(fieldInactiveStyle.Render(label))
		}
		sb.WriteString("\n")
	}

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
	return nil
}

// GetCurrentFieldValue returns the value of the currently selected field.
// Exposed for testing.
func (m *ConfigModel) GetCurrentFieldValue() float64 {
	if m.cursor >= len(configFields) {
		return 0
	}
	return m.getFieldValue(configFields[m.cursor].key)
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
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
