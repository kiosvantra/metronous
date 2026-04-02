// Package tui provides the Bubble Tea terminal user interface for Metronous.
// It exposes a four-tab dashboard: Tracking (real-time events), Benchmark Summary
// (aggregated per-agent/model), Benchmark Detailed (historical runs), and Config
// (threshold editor).
package tui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/version"
)

// UpdateCheckMsg is sent when the background update check completes.
type UpdateCheckMsg struct {
	Available     bool
	LatestVersion string
}

// Tab identifies one of the four dashboard panels.
type Tab int

const (
	TabTracking          Tab = iota // 0 — real-time event stream
	TabBenchmarkSummary             // 1 — aggregated benchmark summary
	TabBenchmarkDetailed            // 2 — per-run benchmark history
	TabCharts                       // 3 — cost charts
	TabConfig                       // 4 — threshold editor
)

// TabBenchmark is an alias for TabBenchmarkDetailed kept for backwards compat
// in any code that still references the old name.
const TabBenchmark = TabBenchmarkDetailed

const numTabs = 5

// tabNames are the display labels for each tab (1-indexed for humans).
var tabNames = [numTabs]string{
	"[1] Tracking",
	"[2] Benchmark Summary",
	"[3] Benchmark Detailed",
	"[4] Charts",
	"[5] Config",
}

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
	tracking         TrackingModel
	benchmarkSummary BenchmarkSummaryModel
	benchmark        BenchmarkModel
	config           ConfigModel
	charts           ChartsModel

	// StatusMsg is a transient message shown at the bottom of the screen.
	StatusMsg string

	// needsClear triggers an ANSI clear only when switching between tabs.
	// Doing it on every render can cause terminal redraw artifacts.
	needsClear bool

	// UpdateAvailable indicates a new version is available.
	UpdateAvailable bool
	// LatestVersion is the version string of the latest release.
	LatestVersion string
	// CurrentVersion is the currently running version.
	CurrentVersion string
}

// NewAppModel creates an AppModel wired to the given stores/config path.
// dataDir is the Metronous data directory (e.g. ~/.metronous/data); it is used
// by the benchmark view to load model pricing from dataDir/../thresholds.json.
// workDir is the current working directory used for project-level agent discovery.
// version is the current application version for update checking.
func NewAppModel(es store.EventStore, bs store.BenchmarkStore, configPath string, dataDir string, workDir string, version string) AppModel {
	// Build an intraweek runner when all dependencies are available.
	// If any dependency is nil, the runner is omitted and F5 is a no-op.
	var iwr IntraweekRunner
	if es != nil && bs != nil {
		thresholdsPath := dataDir + "/../thresholds.json"
		thresholds, err := decision.LoadThresholds(thresholdsPath)
		if err != nil {
			// Fall back to defaults when the file cannot be read.
			defaults := config.DefaultThresholdValues()
			thresholds = &defaults
		}
		engine := decision.NewDecisionEngine(thresholds)
		iwr = runner.NewRunner(es, bs, engine, dataDir, nil)
	}

	return AppModel{
		CurrentTab:       TabTracking,
		tracking:         NewTrackingModel(es),
		benchmarkSummary: NewBenchmarkSummaryModel(bs),
		benchmark:        NewBenchmarkModel(bs, dataDir, workDir, iwr),
		config:           NewConfigModel(configPath),
		charts:           NewChartsModel(es),
		CurrentVersion:   version,
		needsClear:       true,
		UpdateAvailable:  false,
		LatestVersion:    "",
	}
}

// Init returns the initial Bubble Tea command (starts polling for tracking data).
func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.tracking.Init(),
		m.benchmarkSummary.Init(),
		m.benchmark.Init(),
		m.config.Init(),
		m.charts.Init(),
		checkForUpdate,
	)
}

// checkForUpdate fetches the latest release from GitHub API and returns an
// UpdateCheckMsg. It compares semantically against the running binary version.
func checkForUpdate() tea.Msg {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/kiosvantra/metronous/releases/latest")
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return UpdateCheckMsg{Available: false}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UpdateCheckMsg{Available: false}
	}

	s := string(body)
	idx := strings.Index(s, `"tag_name"`)
	if idx == -1 {
		return UpdateCheckMsg{Available: false}
	}
	rest := s[idx+len(`"tag_name"`):]
	colon := strings.Index(rest, `"`)
	if colon == -1 {
		return UpdateCheckMsg{Available: false}
	}
	rest = rest[colon+1:]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return UpdateCheckMsg{Available: false}
	}
	latestTag := rest[:end]
	if latestTag == "" {
		return UpdateCheckMsg{Available: false}
	}

	current := version.Version
	latestClean := strings.TrimPrefix(latestTag, "v")

	if !semverGreater(latestClean, current) {
		return UpdateCheckMsg{Available: false, LatestVersion: latestTag}
	}
	return UpdateCheckMsg{Available: true, LatestVersion: latestTag}
}

// semverGreater returns true if a > b using simple semantic version comparison.
func semverGreater(a, b string) bool {
	ap := semverParts(a)
	bp := semverParts(b)
	for i := 0; i < 3; i++ {
		if ap[i] > bp[i] {
			return true
		}
		if ap[i] < bp[i] {
			return false
		}
	}
	return false
}

func semverParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	// Strip any pre-release suffix (e.g. "0.9.14-dev" → "0.9.14")
	if dash := strings.Index(v, "-"); dash != -1 {
		v = v[:dash]
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		fmt.Sscanf(parts[i], "%d", &out[i])
	}
	return out
}

// httpGet is a simple HTTP GET wrapper for update checking.
func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	return buf[:n], nil
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
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle update check result
	if um, ok := msg.(UpdateCheckMsg); ok {
		m.UpdateAvailable = um.Available
		m.LatestVersion = um.LatestVersion
		return m, nil
	}

	// Handle app-level key events first (tab switching, quit, and
	// tab-specific shortcuts like ctrl+s / ctrl+r).
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "u":
			// Only allow update if update is available
			if !m.UpdateAvailable {
				return m, nil
			}
			// Use absolute path to avoid PATH issues
			exePath, err := os.Executable()
			if err != nil {
				m.StatusMsg = "Error: could not find executable"
				return m, nil
			}
			return m, func() tea.Msg {
				updateCmd := exec.Command(exePath, "self-update")
				updateCmd.Stdout = os.Stdout
				updateCmd.Stderr = os.Stderr
				err := updateCmd.Run()
				if err != nil {
					m.StatusMsg = "Update failed: " + err.Error()
				} else {
					m.StatusMsg = "Update complete! Close and reopen the dashboard."
				}
				return nil
			}

		case "1":
			m.CurrentTab = TabTracking
			m.needsClear = true
			return m, nil

		case "2":
			m.CurrentTab = TabBenchmarkSummary
			m.needsClear = true
			return m, nil

		case "3":
			m.CurrentTab = TabBenchmarkDetailed
			m.needsClear = true
			return m, nil

		case "4":
			m.CurrentTab = TabCharts
			m.needsClear = true
			return m, nil

		case "5":
			m.CurrentTab = TabConfig
			m.needsClear = true
			return m, nil

		case "left":
			if m.CurrentTab > 0 {
				m.CurrentTab--
			}
			m.needsClear = true
			return m, nil

		case "right":
			if int(m.CurrentTab) < numTabs-1 {
				m.CurrentTab++
			}
			m.needsClear = true
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
		case TabBenchmarkSummary:
			m.benchmarkSummary, cmd = m.benchmarkSummary.Update(msg)
		case TabBenchmarkDetailed:
			m.benchmark, cmd = m.benchmark.Update(msg)
		case TabConfig:
			m.config, cmd = m.config.Update(msg)
		case TabCharts:
			m.charts, cmd = m.charts.Update(msg)
		}
		return m, cmd
	}

	// Non-key messages (async data, ticks, window resize, etc.) are fanned
	// out to ALL sub-models so background tabs never miss their data.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = ws.Width
		m.Height = ws.Height
	}

	var tCmd, sCmd, bCmd, cCmd, chCmd tea.Cmd
	m.tracking, tCmd = m.tracking.Update(msg)
	m.benchmarkSummary, sCmd = m.benchmarkSummary.Update(msg)
	m.benchmark, bCmd = m.benchmark.Update(msg)
	m.config, cCmd = m.config.Update(msg)
	m.charts, chCmd = m.charts.Update(msg)
	return m, tea.Batch(tCmd, sCmd, bCmd, cCmd, chCmd)
}

// View renders the full dashboard.
func (m *AppModel) View() string {
	if m.Width == 0 {
		return "loading…"
	}

	clearSeq := "\x1b[2J\x1b[H"
	prefix := ""
	// Clear only when switching tabs to avoid terminal redraw artifacts while
	// moving cursors inside the same tab.
	if m.needsClear {
		prefix = clearSeq
		m.needsClear = false
	}

	// Tab bar.
	tabBar := m.renderTabBar()

	// Content area.
	var content string
	switch m.CurrentTab {
	case TabTracking:
		content = m.tracking.View()
	case TabBenchmarkSummary:
		content = m.benchmarkSummary.View()
	case TabBenchmarkDetailed:
		content = m.benchmark.View()
	case TabConfig:
		content = m.config.View()
	case TabCharts:
		content = m.charts.View()
	}

	// Update banner
	var banner string
	if m.UpdateAvailable {
		bannerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("yellow")).
			Bold(true)
		banner = bannerStyle.Render(fmt.Sprintf("Update available: %s (current: %s). Press 'u' to update.",
			m.LatestVersion, m.CurrentVersion))
		banner += "\n"
	}

	// Status bar - show "u: update" only if update is available
	hint := statusBarStyle.Render("↑/↓: navigate  q: quit  1/2/3/4/5 or ←/→: switch tabs  ctrl+s: save  ctrl+r: reload")
	if m.UpdateAvailable {
		hint = statusBarStyle.Render("↑/↓: navigate  q: quit  1/2/3/4/5 or ←/→: switch tabs  ctrl+s: save  ctrl+r: reload  u: update")
	}

	return prefix + fmt.Sprintf("%s\n%s\n%s\n%s", tabBar, banner, content, hint)
}

// renderTabBar returns the rendered tab bar string with the current version on the right.
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

	// Append version right-aligned inside the tab bar.
	if m.CurrentVersion != "" && m.Width > 0 {
		versionLabel := "v" + m.CurrentVersion
		// lipgloss.Width strips ANSI codes — use it for accurate text widths.
		barTextWidth := lipgloss.Width(bar)
		versionTextWidth := lipgloss.Width(versionLabel)
		// tabBarStyle renders inside m.Width (terminal columns).
		// Subtract 2 to account for the border frame lipgloss adds.
		innerWidth := m.Width - 2
		padding := innerWidth - barTextWidth - versionTextWidth
		if padding > 0 {
			bar += strings.Repeat(" ", padding) + inactiveTabStyle.Render(versionLabel)
		} else {
			bar += "  " + inactiveTabStyle.Render(versionLabel)
		}
	}

	return tabBarStyle.Render(bar)
}
