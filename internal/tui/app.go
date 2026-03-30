// Package tui provides the Bubble Tea terminal user interface for Metronous.
// It exposes a three-tab dashboard: Tracking (real-time events), Benchmark
// (historical runs), and Config (threshold editor).
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/store"
)

// Tab identifies one of the three dashboard panels.
type Tab int

const (
	TabTracking  Tab = iota // 0 — real-time event stream
	TabBenchmark            // 1 — benchmark history
	TabConfig               // 2 — threshold editor
)

const numTabs = 3

// tabNames are the display labels for each tab (1-indexed for humans).
var tabNames = [numTabs]string{"[1] Tracking", "[2] Benchmark", "[3] Config"}

// Styles are shared across all views.
var (
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Padding(0, 1)

	tabBarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("240"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))
)

// AppModel is the root Bubble Tea model for the Metronous dashboard.
type AppModel struct {
	// CurrentTab is the index of the active tab.
	CurrentTab Tab

	// Width and Height are updated on every tea.WindowSizeMsg.
	Width  int
	Height int

	// Sub-models for each tab.
	tracking  TrackingModel
	benchmark BenchmarkModel
	config    ConfigModel

	// StatusMsg is a transient message shown at the bottom of the screen.
	StatusMsg string
}

// NewAppModel creates an AppModel wired to the given stores/config path.
// dataDir is the Metronous data directory (e.g. ~/.metronous/data); it is used
// by the benchmark view to load model pricing from dataDir/../thresholds.json.
// workDir is the current working directory used for project-level agent discovery.
func NewAppModel(es store.EventStore, bs store.BenchmarkStore, configPath string, dataDir string, workDir string) AppModel {
	return AppModel{
		CurrentTab: TabTracking,
		tracking:   NewTrackingModel(es),
		benchmark:  NewBenchmarkModel(bs, dataDir, workDir),
		config:     NewConfigModel(configPath),
	}
}

// Init returns the initial Bubble Tea command (starts polling for tracking data).
func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.tracking.Init(),
		m.benchmark.Init(),
		m.config.Init(),
	)
}

// Update handles all incoming messages and routes them to sub-models.
//
// Key events are handled by the app first (tab switching, quit) and then
// forwarded only to the active sub-model so that keyboard shortcuts stay
// scoped to the visible tab.
//
// All other messages (async data, ticks, window resize) are fanned out to
// ALL sub-models so that background tabs continue receiving data even when
// they are not active. Each sub-model already ignores messages it does not
// understand via the default case in its own Update switch.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle app-level key events first (tab switching, quit, and
	// tab-specific shortcuts like ctrl+s / ctrl+r).
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "1":
			m.CurrentTab = TabTracking
			return m, nil

		case "2":
			m.CurrentTab = TabBenchmark
			return m, nil

		case "3":
			m.CurrentTab = TabConfig
			return m, nil

		case "left":
			if m.CurrentTab > 0 {
				m.CurrentTab--
			}
			return m, nil

		case "right":
			if int(m.CurrentTab) < numTabs-1 {
				m.CurrentTab++
			}
			return m, nil

		case "ctrl+s":
			if m.CurrentTab == TabConfig {
				var cmd tea.Cmd
				m.config, cmd = m.config.UpdateSave(key)
				return m, cmd
			}
			return m, nil

		case "ctrl+r":
			if m.CurrentTab == TabConfig {
				var cmd tea.Cmd
				m.config, cmd = m.config.UpdateReload(key)
				return m, cmd
			}
			return m, nil
		}

		// Unknown key — forward only to the active sub-model.
		var cmd tea.Cmd
		switch m.CurrentTab {
		case TabTracking:
			m.tracking, cmd = m.tracking.Update(msg)
		case TabBenchmark:
			m.benchmark, cmd = m.benchmark.Update(msg)
		case TabConfig:
			m.config, cmd = m.config.Update(msg)
		}
		return m, cmd
	}

	// Non-key messages (async data, ticks, window resize, etc.) are fanned
	// out to ALL sub-models so background tabs never miss their data.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = ws.Width
		m.Height = ws.Height
	}

	var tCmd, bCmd, cCmd tea.Cmd
	m.tracking, tCmd = m.tracking.Update(msg)
	m.benchmark, bCmd = m.benchmark.Update(msg)
	m.config, cCmd = m.config.Update(msg)
	return m, tea.Batch(tCmd, bCmd, cCmd)
}

// View renders the full dashboard.
func (m AppModel) View() string {
	if m.Width == 0 {
		return "loading…"
	}

	// Tab bar.
	tabBar := m.renderTabBar()

	// Content area.
	var content string
	switch m.CurrentTab {
	case TabTracking:
		content = m.tracking.View()
	case TabBenchmark:
		content = m.benchmark.View()
	case TabConfig:
		content = m.config.View()
	}

	// Status bar.
	hint := statusBarStyle.Render("↑/↓: navigate  q: quit  1/2/3 or ←/→: switch tabs  ctrl+s: save  ctrl+r: reload")

	return fmt.Sprintf("%s\n%s\n%s", tabBar, content, hint)
}

// renderTabBar returns the rendered tab bar string.
func (m AppModel) renderTabBar() string {
	var tabs [numTabs]string
	for i, name := range tabNames {
		if Tab(i) == m.CurrentTab {
			tabs[i] = activeTabStyle.Render(name)
		} else {
			tabs[i] = inactiveTabStyle.Render(name)
		}
	}
	bar := tabs[0]
	for _, t := range tabs[1:] {
		bar += "  " + t
	}
	return tabBarStyle.Render(bar)
}
