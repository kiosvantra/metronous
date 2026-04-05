package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/discovery"
	"github.com/kiosvantra/metronous/internal/store"
)

// IntraweekRunner is the interface the benchmark view uses to trigger a manual
// intraweek benchmark run. It is satisfied by *runner.Runner and can be mocked
// in tests.
type IntraweekRunner interface {
	RunIntraweek(ctx context.Context, windowDays int) error
}

// intraweekRunDoneMsg is sent by the async intraweek benchmark command when the
// run completes (successfully or with an error).
// Err is exported so tests can construct the message via the IntraweekRunDoneMsg alias.
type intraweekRunDoneMsg struct{ Err error }

// benchmarkRefreshInterval is the auto-refresh period for the benchmark tab,
// matching the tracking tab's cadence.
const benchmarkRefreshInterval = 2 * time.Second

// benchmarkTickMsg is sent by the auto-refresh ticker.
type benchmarkTickMsg struct{ t time.Time }

// BenchmarkDataMsg carries fetched benchmark runs.
type BenchmarkDataMsg struct {
	Runs      []store.BenchmarkRun
	Cycles    []time.Time         // week-start timestamps, newest first (nil = no change)
	TypeByID  map[string]string   // agentID → type label (primary/subagent/built-in/all)
	TrendByID map[string][]string // agentID → verdict trend (oldest first)
	Err       error
}

// Verdict colour styles.
var (
	verdictKeep   = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))  // green
	verdictSwitch = lipgloss.NewStyle().Foreground(lipgloss.Color("226")) // yellow
	verdictUrgent = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	verdictOther  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // grey
)

// detailPanelStyle styles the decision rationale detail panel.
var detailPanelStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("252"))

// detailLabelStyle styles the label keys in the detail panel.
var detailLabelStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("33"))

// Keybind highlight styles.
var f5KeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("19")).Bold(true) // dark blue

// benchColWidths / benchColNames describe the benchmark history table.
// Columns: Time | Agent | Model | Samples | Accuracy | Avg Response | Verdict | → Switch To | Savings
// "Time" shows full date+time (YYYY-MM-DD HH:MM) so width is 17 to avoid truncation.
var (
	benchColWidths = []int{17, 14, 22, 8, 10, 13, 18, 16, 8}
	benchColNames  = []string{"Time", "Agent", "Model", "Samples", "Accuracy", "Avg Response", "Verdict", "→ Switch To", "Savings"}
)

// verdictColIdx is the index of the Verdict column in benchColNames/benchColWidths.
// Defined as a constant so the rendering code stays in sync with the column layout.
const verdictColIdx = 6

// maxBenchmarkRows is the maximum number of rows visible at once (scroll window).
const maxBenchmarkRows = 20

// modelPricingSection mirrors the JSON structure of the "model_pricing" key in thresholds.json.
type modelPricingSection struct {
	Models map[string]float64 `json:"models"`
}

// loadModelPricing reads the "model_pricing.models" section from thresholds.json located
// in the parent directory of dataDir (i.e. dataDir/../thresholds.json).
// Returns an empty map if the file cannot be read or the section is absent — callers
// treat an empty map as "pricing unknown" and display "-" for savings.
func loadModelPricing(dataDir string) map[string]float64 {
	if dataDir == "" {
		return map[string]float64{}
	}
	thresholdsPath := filepath.Join(dataDir, "..", "thresholds.json")
	data, err := os.ReadFile(thresholdsPath)
	if err != nil {
		return map[string]float64{}
	}
	var raw struct {
		ModelPricing *modelPricingSection `json:"model_pricing"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.ModelPricing == nil {
		return map[string]float64{}
	}
	return raw.ModelPricing.Models
}

// BenchmarkModel is the Bubble Tea sub-model for the benchmark history tab.
type BenchmarkModel struct {
	bs        store.BenchmarkStore
	runs      []store.BenchmarkRun // rows for the current cycle (one per agent+model combo, placeholder if no run)
	agents    []discovery.AgentInfo
	typeByID  map[string]string   // agentID → type label (primary/subagent/built-in/all)
	trendByID map[string][]string // agentID → verdict trend (oldest first)
	err       error
	// cursor is the absolute row index within m.runs.
	// offset is the first visible row index (scroll window).
	cursor  int
	offset  int
	loading bool
	// cycles is the ordered list of week-start times (newest first) discovered in the DB.
	cycles []time.Time
	// cycleIndex is the index into cycles currently displayed (0 = newest cycle).
	// PgDn increases cycleIndex (moves toward older cycles).
	// PgUp decreases cycleIndex (moves toward newer cycles).
	cycleIndex int
	// detailFrozen indicates whether the detail panel is locked to frozenRun/frozenTrend.
	// When true, the detail does not update even if the background refresh changes m.runs.
	detailFrozen bool
	// frozenRun is the run whose detail panel is displayed when detailFrozen == true.
	frozenRun store.BenchmarkRun
	// frozenTrend is the verdict trend for frozenRun, captured at freeze time.
	frozenTrend []string
	pricing     map[string]float64
	workDir     string
	// runner is an optional IntraweekRunner used to trigger manual F5 runs.
	// When nil, F5 is a no-op.
	runner IntraweekRunner
	// running is true while an F5-triggered intraweek run is in progress.
	// It prevents concurrent runs and shows a status indicator in the footer.
	running bool
	// runErr holds the error (if any) from the most recent F5 run.
	// Cleared when the next F5 run starts.
	runErr error
	// Threshold defaults used for explainability classification/rendering.
	minAccuracy float64
	minROI      float64
}

// NewBenchmarkModel creates a BenchmarkModel wired to the given BenchmarkStore.
// dataDir is the Metronous data directory (e.g. ~/.metronous/data); pricing is
// loaded from dataDir/../thresholds.json. Pass an empty string to disable pricing.
// workDir is used for project-level agent discovery; pass os.Getwd() from the caller.
// r is an optional IntraweekRunner; pass nil to disable F5 manual runs.
func NewBenchmarkModel(bs store.BenchmarkStore, dataDir string, workDir string, r IntraweekRunner) BenchmarkModel {
	defaults := config.DefaultThresholdValues().Defaults
	return BenchmarkModel{
		bs:          bs,
		loading:     true,
		pricing:     loadModelPricing(dataDir),
		agents:      discovery.DiscoverAgents(workDir),
		workDir:     workDir,
		runner:      r,
		minAccuracy: defaults.MinAccuracy,
		minROI:      defaults.MinROIScore,
	}
}

// Init returns the initial fetch command and starts the auto-refresh ticker.
func (m BenchmarkModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchRuns(),
		tea.Tick(benchmarkRefreshInterval, func(t time.Time) tea.Msg {
			return benchmarkTickMsg{t: t}
		}),
	)
}

// Update handles data, tick, and key messages.
func (m BenchmarkModel) Update(msg tea.Msg) (BenchmarkModel, tea.Cmd) {
	switch msg := msg.(type) {
	case benchmarkTickMsg:
		// Schedule next tick and refresh data.
		// While a manual run is in progress we still schedule the next tick so the
		// auto-refresh resumes normally once the run finishes.
		return m, tea.Batch(
			tea.Tick(benchmarkRefreshInterval, func(t time.Time) tea.Msg {
				return benchmarkTickMsg{t: t}
			}),
			m.fetchRuns(),
		)

	case ConfigReloadedMsg:
		m.minAccuracy = msg.Thresholds.Defaults.MinAccuracy
		m.minROI = msg.Thresholds.Defaults.MinROIScore
		return m, nil

	case BenchmarkDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.runs = msg.Runs
			if msg.Cycles != nil {
				m.cycles = msg.Cycles
				// Clamp cycleIndex to valid range when cycles change.
				if m.cycleIndex >= len(m.cycles) && len(m.cycles) > 0 {
					m.cycleIndex = len(m.cycles) - 1
				}
			}
			if msg.TypeByID != nil {
				m.typeByID = msg.TypeByID
			}
			if msg.TrendByID != nil {
				m.trendByID = msg.TrendByID
			}
			// Clamp cursor to actual result size.
			if m.cursor >= len(m.runs) {
				if len(m.runs) > 0 {
					m.cursor = len(m.runs) - 1
				} else {
					m.cursor = 0
				}
			}
			// Clamp offset.
			if m.offset > m.cursor {
				m.offset = m.cursor
			}
		}
		return m, nil

	case intraweekRunDoneMsg:
		// The manual run finished — clear the running flag, capture any error, and
		// immediately refresh the view so the new run appears in the table.
		m.running = false
		m.runErr = msg.Err
		return m, m.fetchRuns()

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			// Move selection one row up within the current cycle.
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
			// Unfreeze detail so it follows the cursor.
			m.detailFrozen = false
		case "down", "j":
			// Move selection one row down within the current cycle.
			if m.cursor < len(m.runs)-1 {
				m.cursor++
				if m.cursor >= m.offset+maxBenchmarkRows {
					m.offset = m.cursor - maxBenchmarkRows + 1
				}
			}
			// Unfreeze detail so it follows the cursor.
			m.detailFrozen = false
		case "pgdown":
			// Move to the next (older) cycle.
			if m.cycleIndex < len(m.cycles)-1 {
				m.cycleIndex++
				m.cursor = 0
				m.offset = 0
				m.detailFrozen = false
				return m, m.fetchRuns()
			}
		case "pgup":
			// Move to the previous (newer) cycle.
			if m.cycleIndex > 0 {
				m.cycleIndex--
				m.cursor = 0
				m.offset = 0
				m.detailFrozen = false
				return m, m.fetchRuns()
			}
		case "enter":
			// Freeze the detail panel on the currently selected run.
			if m.cursor >= 0 && m.cursor < len(m.runs) {
				m.detailFrozen = true
				m.frozenRun = m.runs[m.cursor]
				m.frozenTrend = m.trendByID[m.frozenRun.AgentID]
			}
		case "esc", "escape":
			// Unfreeze the detail panel.
			m.detailFrozen = false
		case "f5":
			// Trigger a manual intraweek benchmark run.
			// Guard: no concurrent runs and a runner must be wired.
			if !m.running && m.runner != nil {
				m.running = true
				m.runErr = nil
				r := m.runner
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()
					err := r.RunIntraweek(ctx, 7)
					return intraweekRunDoneMsg{Err: err}
				}
			}
		}
	}
	return m, nil
}

// agentTypeOrder returns a sort priority for the given agent type.
// Primary agents come first (0), then subagent (1), then all (2), then built-in (3).
// Unknown types sort last (4).
func agentTypeOrder(t string) int {
	switch t {
	case "primary":
		return 0
	case "subagent":
		return 1
	case "all":
		return 2
	case "built-in":
		return 3
	default:
		return 4
	}
}

// fetchRuns returns a command that builds the current cycle's agent rows.
// Each cycle corresponds to one Sunday-bounded week in local time.
// All agents (discovered + DB) appear; agents with no run in the cycle get a NO RUN placeholder.
func (m BenchmarkModel) fetchRuns() tea.Cmd {
	if m.bs == nil {
		return nil
	}
	// Snapshot mutable state so the closure is self-contained.
	agents := m.agents
	cycleIndex := m.cycleIndex
	knownCycles := m.cycles
	loc := time.Local

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// ── 1. Build typeByID from discovered agents + DB agents ──────────────
		typeByID := make(map[string]string, len(agents))
		for _, a := range agents {
			typeByID[a.ID] = a.Type
		}

		dbAgents, err := m.bs.ListAgents(ctx)
		if err != nil {
			return BenchmarkDataMsg{Err: err}
		}
		for _, id := range dbAgents {
			if _, found := typeByID[id]; !found {
				typeByID[id] = "primary"
			}
		}

		// Merge into ordered allIDs (discovered first, then DB-only).
		seen := make(map[string]bool)
		var allIDs []string
		for _, a := range agents {
			if !seen[a.ID] {
				seen[a.ID] = true
				allIDs = append(allIDs, a.ID)
			}
		}
		for _, id := range dbAgents {
			if !seen[id] {
				seen[id] = true
				allIDs = append(allIDs, id)
			}
		}

		// ── 2. Fetch/refresh cycle list ───────────────────────────────────────
		cycles, err := m.bs.ListRunCycles(ctx, loc, 0, 0)
		if err != nil {
			return BenchmarkDataMsg{Err: err}
		}
		// Fall back to previously known cycles so the UI stays consistent while
		// the store hasn't written any runs yet.
		if len(cycles) == 0 && len(knownCycles) > 0 {
			cycles = knownCycles
		}

		// ── 3. Determine the active cycle window ──────────────────────────────
		// cycleIndex 0 = newest cycle; clamp to valid range.
		if cycleIndex < 0 {
			cycleIndex = 0
		}
		if cycleIndex >= len(cycles) && len(cycles) > 0 {
			cycleIndex = len(cycles) - 1
		}

		var (
			cycleStart time.Time
			cycleEnd   time.Time
		)
		if len(cycles) > 0 {
			cycleStart = cycles[cycleIndex]
			cycleEnd = cycleStart.AddDate(0, 0, 7)
		}

		// ── 4. Fetch runs in the active cycle window ──────────────────────────
		var windowRuns []store.BenchmarkRun
		if !cycleStart.IsZero() {
			windowRuns, err = m.bs.QueryRunsInWindow(ctx, cycleStart.UTC(), cycleEnd.UTC())
			if err != nil {
				return BenchmarkDataMsg{Err: err}
			}
		}

		// ── 5. Build one row per (agent, model) combo + NO RUN placeholders ─────
		// Collect the latest run per (agentID, model) pair within the cycle.
		type agentModel struct{ agentID, model string }
		latestByAgentModel := make(map[agentModel]store.BenchmarkRun, len(windowRuns))
		for _, r := range windowRuns {
			key := agentModel{r.AgentID, r.Model}
			if existing, ok := latestByAgentModel[key]; !ok || r.RunAt.After(existing.RunAt) {
				latestByAgentModel[key] = r
			}
		}

		// Track which agents have at least one real run in this cycle.
		agentsWithRuns := make(map[string]bool, len(windowRuns))
		for key := range latestByAgentModel {
			agentsWithRuns[key.agentID] = true
		}

		var page []store.BenchmarkRun
		for key, run := range latestByAgentModel {
			_ = key
			page = append(page, run)
		}
		// Add NO RUN placeholder for agents with zero runs in the cycle.
		for _, agentID := range allIDs {
			if !agentsWithRuns[agentID] {
				page = append(page, store.BenchmarkRun{AgentID: agentID})
			}
		}

		// Sort: status (active first) → agentID asc → run_at desc → SampleSize desc.
		// NO DATA placeholders go last.
		statusOrder := map[store.RunStatus]int{
			store.RunStatusActive:     0,
			store.RunStatusSuperseded: 1,
		}
		sort.Slice(page, func(i, j int) bool {
			// NO DATA placeholders always go last.
			if isNoData(page[i]) != isNoData(page[j]) {
				return !isNoData(page[i])
			}
			// Active runs come before superseded runs.
			iStatus := statusOrder[page[i].Status]
			jStatus := statusOrder[page[j].Status]
			if iStatus != jStatus {
				return iStatus < jStatus
			}
			// Within same status, sort by agentID asc.
			if page[i].AgentID != page[j].AgentID {
				return page[i].AgentID < page[j].AgentID
			}
			// Within same agent, sort by run_at desc (most recent first).
			if page[i].RunAt != page[j].RunAt {
				return page[i].RunAt.After(page[j].RunAt)
			}
			// Tiebreaker: SampleSize desc.
			return page[i].SampleSize > page[j].SampleSize
		})

		// ── 6. Fetch verdict trends for each agent in the page (last 8 weeks) ─
		trendByID := make(map[string][]string, len(page))
		seenAgentIDs := make(map[string]struct{}, len(page))
		for _, run := range page {
			if run.AgentID == "" {
				continue
			}
			if _, seen := seenAgentIDs[run.AgentID]; seen {
				continue
			}
			seenAgentIDs[run.AgentID] = struct{}{}

			trend, err := m.bs.GetVerdictTrend(ctx, run.AgentID, 8)
			if err == nil {
				trendByID[run.AgentID] = trend
			}
		}

		return BenchmarkDataMsg{Runs: page, Cycles: cycles, TypeByID: typeByID, TrendByID: trendByID}
	}
}

// View renders the benchmark history tab.
func (m BenchmarkModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Run Cycle") + "\n")

	// F5 indicator — always visible below the title, same as Benchmark Summary.
	f5TopStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true) // yellow
	dimTop := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	if m.running {
		runningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true)
		sb.WriteString(runningStyle.Render("  ⏳ Running intraweek benchmark...") + "\n\n")
	} else if m.runErr != nil {
		errRunStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		sb.WriteString(errRunStyle.Render(fmt.Sprintf("  ✗ Intraweek run failed: %v", m.runErr)) + "\n\n")
	} else {
		sb.WriteString(dimTop.Render("  Press") + " " + f5TopStyle.Render("F5") + dimTop.Render(" to run an intraweek benchmark now") + "\n\n")
	}

	if m.loading {
		sb.WriteString(dimStyle.Render("  Loading…") + "\n")
		return sb.String()
	}
	if m.err != nil {
		sb.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", m.err)) + "\n")
		return sb.String()
	}
	if len(m.runs) == 0 && len(m.agents) == 0 {
		sb.WriteString(dimStyle.Render("  No agents discovered and no benchmark runs yet.") + "\n")
		return sb.String()
	}
	if len(m.runs) == 0 {
		sb.WriteString(dimStyle.Render("  No benchmark runs yet. Run a benchmark to see history here.") + "\n")
		return sb.String()
	}

	// Header.
	sb.WriteString(renderRow(benchColNames, benchColWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(benchColWidths)) + "\n")

	// Scroll indicator above if there are rows above the visible window.
	if m.offset > 0 {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more above", m.offset)) + "\n")
	}

	// Compute current model per agent (highest run_at with Status='active').
	// Index invariant: currentModelByAgent stores absolute indexes into m.runs
	// (not page-relative indexes). The render loop also iterates m.runs with
	// absolute index i, so currentModelByAgent[agentID] == i is valid.
	currentModelByAgent := make(map[string]int) // agent → run index
	for i, run := range m.runs {
		if !isNoData(run) && run.Status == store.RunStatusActive {
			if _, exists := currentModelByAgent[run.AgentID]; !exists || m.runs[currentModelByAgent[run.AgentID]].RunAt.Before(run.RunAt) {
				currentModelByAgent[run.AgentID] = i
			}
		}
	}

	// Data rows — render only the visible window [offset, offset+maxBenchmarkRows).
	end := m.offset + maxBenchmarkRows
	if end > len(m.runs) {
		end = len(m.runs)
	}
	for i := m.offset; i < end; i++ {
		run := m.runs[i]
		row := formatBenchmarkRow(run, m.pricing)
		baseStyle := lipgloss.NewStyle()
		isNoDataRow := isNoData(run)
		// For NO DATA rows: keep grey text, but if the cursor is on the row
		// show only the background highlight (so the cursor doesn't disappear).
		if isNoDataRow {
			baseStyle = dimStyle
			if i == m.cursor {
				baseStyle = dimStyle.Copy().Background(lipgloss.Color("236"))
			}
		} else if i == m.cursor {
			baseStyle = cursorStyle
		}
		// Render columns before Verdict without special colour.
		// verdictColIdx = 6 (Time, Agent, Model, Samples, Accuracy, Avg Response, Verdict, → Switch To, Savings)
		rendered := renderRow(row[:verdictColIdx], benchColWidths[:verdictColIdx], baseStyle)

		// Add visual marker (●) to agent cell if this is the current active run for the agent.
		isCurrent := !isNoDataRow && run.Status == store.RunStatusActive && currentModelByAgent[run.AgentID] == i
		if isCurrent {
			// Replace the agent column with a marker prefix.
			agentCell := row[1]
			agentWithMarker := "● " + agentCell
			// Re-render the first column (Time) + modified Agent column.
			rendered = renderRow(row[0:1], benchColWidths[0:1], baseStyle)
			rendered += baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[1], agentWithMarker))
			// Add back the remaining columns (Model, Samples, Accuracy, Avg Response).
			rendered += " " + renderRow(row[2:verdictColIdx], benchColWidths[2:verdictColIdx], baseStyle)
		}

		// Verdict column: coloured independently.
		var verdictCell string
		if isNoDataRow {
			verdictCell = baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[verdictColIdx], row[verdictColIdx]))
		} else if run.Status == store.RunStatusSuperseded {
			// Superseded runs show "CHANGED" in grey.
			verdictCell = verdictOther.Render(
				fmt.Sprintf("%-*s", benchColWidths[verdictColIdx], "CHANGED"))
		} else {
			verdictCell = verdictStyle(run.Verdict).Render(
				fmt.Sprintf("%-*s", benchColWidths[verdictColIdx], row[verdictColIdx]))
		}
		rendered += verdictCell
		// → Switch To column (index 7).
		rendered += " " + baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[7], row[7]))
		// Savings column (index 8).
		rendered += " " + baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[8], row[8]))
		// Write the row directly — do NOT re-wrap with baseStyle.Render() as that
		// would strip the inner ANSI colour codes (verdict colour, etc.).
		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	// Scroll indicator below if there are rows below the visible window.
	below := len(m.runs) - end
	if below > 0 {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more below", below)) + "\n")
	}

	// Pagination footer: show cycle number (1-based from newest).
	sb.WriteString("\n")
	totalCycles := len(m.cycles)
	var cycleLabel string
	if totalCycles > 0 {
		cycleStart := m.cycles[m.cycleIndex]
		// cycleIndex 0 = most recent cycle = highest number (totalCycles).
		// cycleIndex totalCycles-1 = oldest = cycle 1.
		currentNum := totalCycles - m.cycleIndex
		cycleLabel = fmt.Sprintf("cycle %d/%d  (week of %s)",
			currentNum, totalCycles, cycleStart.Local().Format("2006-01-02"))
	} else {
		cycleLabel = "cycle 1/1"
	}
	footerText := fmt.Sprintf("  %d rows  |  %s  (↑↓ scroll, PgUp/PgDn cycle, Enter freeze detail)",
		len(m.runs), cycleLabel)
	sb.WriteString(dimStyle.Render(footerText))
	sb.WriteString("\n")

	// Detail panel for the selected run.
	// When detailFrozen, show the frozen snapshot — it won't change on background refresh.
	if m.cursor >= 0 && m.cursor < len(m.runs) {
		sb.WriteString("\n")
		var detailRun store.BenchmarkRun
		var trend []string
		if m.detailFrozen {
			detailRun = m.frozenRun
			trend = m.frozenTrend
			sb.WriteString(dimStyle.Render("  [Detail frozen — press Esc to unfreeze]") + "\n")
		} else {
			detailRun = m.runs[m.cursor]
			trend = m.trendByID[detailRun.AgentID]
		}
		sb.WriteString(renderDetailPanel(detailRun, m.pricing, trend, m.minAccuracy, m.minROI))
	}

	return sb.String()
}

// renderDetailPanel renders the decision rationale panel for the selected run.
// trend is the verdict history for the agent (oldest first); pass nil if unavailable.
func renderDetailPanel(run store.BenchmarkRun, pricing map[string]float64, trend []string, minAccuracy float64, minROI float64) string {
	var sb strings.Builder

	// Prevent terminal auto-wrapping from pushing/popping the main table out of
	// view when switching rows. Use a generous limit so multi-line fields like
	// the trend legend are never truncated.
	const maxDetailValueLen = 200
	clamp := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) <= maxDetailValueLen {
			return s
		}
		return s[:maxDetailValueLen-1] + "…"
	}

	divider := strings.Repeat("─", totalWidth(benchColWidths))
	sb.WriteString(dimStyle.Render(divider) + "\n")
	sb.WriteString(detailLabelStyle.Render("Decision Rationale") + "\n")
	sb.WriteString(dimStyle.Render(divider) + "\n")

	// Handle NO_DATA placeholder rows.
	if isNoData(run) {
		writeDetailField(&sb, "Agent", run.AgentID)
		writeDetailField(&sb, "Status", "No benchmark runs recorded yet for this agent.")
		return sb.String()
	}

	// Reason is always derived dynamically from numeric fields so historical
	// runs reflect the current formula, not the stale text stored in the DB.
	reason := clamp(renderReason(run))

	// Verdict line: show switch arrow if applicable.
	verdictLine := string(run.Verdict)
	if (run.Verdict == store.VerdictSwitch || run.Verdict == store.VerdictUrgentSwitch) && run.RecommendedModel != "" {
		verdictLine = fmt.Sprintf("%s → %s", run.Verdict, run.RecommendedModel)
	}

	// Cost savings for detail panel.
	_, savingsStr := computeSavings(run.Model, run.RecommendedModel, run.Verdict, pricing)

	// Format fields with aligned labels.
	writeDetailField(&sb, "Agent", run.AgentID)
	writeDetailField(&sb, "Model", store.NormalizeModelName(run.Model))
	writeDetailField(&sb, "Verdict", verdictLine)
	writeDetailField(&sb, "Cost", fmt.Sprintf("$%.2f  Savings: %s", run.TotalCostUSD, savingsStr))
	writeDetailField(&sb, "Samples", fmt.Sprintf("%d events", run.SampleSize))
	sb.WriteString("\n")
	writeDetailField(&sb, "Reason", reason)

	explanation := buildVerdictExplanation(run, pricing, minAccuracy, minROI)
	switch explanation.FailureType {
	case "quality-gap":
		if explanation.IsFreeToPayTransition {
			renderScenario1(&sb, run, explanation)
		} else {
			renderScenario2(&sb, run, explanation)
		}
	case "cost-optimization":
		renderScenario3(&sb, run, explanation)
	case "cost-data-missing":
		renderScenario5(&sb, run, explanation)
	case "keep":
		renderScenario4(&sb, run, explanation)
	default:
		renderScenarioUnknown(&sb, run)
	}

	writeDetailField(&sb, "Context", clamp(evaluateAgentContext(run)))

	// Trend line: show last N verdicts with direction indicator.
	if len(trend) > 0 {
		trendStr := formatVerdictTrend(trend)
		writeDetailField(&sb, "Trend", clamp(trendStr))
	}

	return sb.String()
}

// verdictAbbrev returns a single-character abbreviation for a verdict.
// Legend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  C=CHANGED  ?=INSUFFICIENT_DATA
func verdictAbbrev(v string) string {
	switch store.VerdictType(v) {
	case store.VerdictKeep:
		return "K"
	case store.VerdictSwitch:
		return "S"
	case store.VerdictUrgentSwitch:
		return "U"
	case store.VerdictInsufficientData:
		return "?"
	case store.VerdictType("CHANGED"):
		return "C"
	default:
		return "?"
	}
}

// trendDirectionStyled returns the direction indicator with color applied.
//   - improving → bright green (82)
//   - degrading → bright red   (196)
//   - stable    → bright blue  (39)
func trendDirectionStyled(verdicts []string) string {
	dir := trendDirection(verdicts)
	switch dir {
	case "↑ improving":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true).Render(dir)
	case "↓ degrading":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render(dir)
	case "→ unknown":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Bold(true).Render(dir)
	default: // → stable
		return lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true).Render(dir)
	}
}

// formatVerdictTrend formats the last 5 verdicts as abbreviated single chars
// separated by arrows, followed by a colored direction indicator.
// Legend line is appended below.
// e.g. "K → K → S → C → ?  (↓ degrading)\n  Legend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  C=CHANGED  ?=INSUFFICIENT_DATA"
func formatVerdictTrend(trend []string) string {
	if len(trend) == 0 {
		return "-"
	}
	// Take last 5 entries (most recent).
	window := trend
	if len(window) > 5 {
		window = window[len(window)-5:]
	}
	abbrevs := make([]string, len(window))
	for i, v := range window {
		abbrevs[i] = verdictAbbrev(v)
	}
	trendLine := strings.Join(abbrevs, " → ")
	direction := trendDirectionStyled(trend)
	return fmt.Sprintf("%s  (%s)\nLegend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  C=CHANGED  ?=INSUFFICIENT_DATA", trendLine, direction)
}

// verdictSeverity returns a numeric severity for a verdict (lower = better).
func verdictSeverity(v string) int {
	switch store.VerdictType(v) {
	case store.VerdictKeep:
		return 0
	case store.VerdictInsufficientData:
		return 1
	case store.VerdictType("CHANGED"):
		return 1
	case store.VerdictSwitch:
		return 2
	case store.VerdictUrgentSwitch:
		return 3
	default:
		return 1
	}
}

// trendDirection returns a direction indicator string for a slice of verdict strings.
// If the most recent verdict is INSUFFICIENT_DATA or CHANGED, direction is "unknown" —
// we cannot assess trend direction from transition/non-comparable points.
// If only older entries are INSUFFICIENT_DATA/CHANGED (but the last is not),
// comparison is still valid and uses the most recent comparable baseline.
func trendDirection(verdicts []string) string {
	if len(verdicts) < 2 {
		return "→ stable"
	}
	last := verdicts[len(verdicts)-1]

	// If the latest verdict has insufficient data, we cannot determine direction.
	if last == string(store.VerdictInsufficientData) || last == "CHANGED" {
		return "→ unknown"
	}

	// Find the most recent non-INSUFFICIENT_DATA / non-CHANGED verdict before the last one
	// to use as the comparison baseline.
	first := ""
	for i := len(verdicts) - 2; i >= 0; i-- {
		if verdicts[i] != string(store.VerdictInsufficientData) && verdicts[i] != "CHANGED" {
			first = verdicts[i]
			break
		}
	}
	if first == "" {
		// All previous verdicts were INSUFFICIENT_DATA/CHANGED — no baseline to compare.
		return "→ unknown"
	}

	firstSev := verdictSeverity(first)
	lastSev := verdictSeverity(last)

	if lastSev < firstSev {
		return "↑ improving"
	}
	if lastSev > firstSev {
		return "↓ degrading"
	}
	return "→ stable"
}

// renderReason generates the Reason string dynamically from the run's numeric
// fields. This is intentionally NOT read from run.DecisionReason so that any
// improvements to the formula are reflected immediately in historical runs.
func renderReason(run store.BenchmarkRun) string {
	switch run.Verdict {
	case store.VerdictInsufficientData:
		return fmt.Sprintf("Insufficient data: only %d events (minimum 50 required)", run.SampleSize)

	case store.VerdictUrgentSwitch:
		var parts []string
		if run.Accuracy < 0.70 {
			parts = append(parts, fmt.Sprintf("URGENT: accuracy %.1f%% is critically low", run.Accuracy*100))
		}
		if len(parts) == 0 {
			parts = append(parts, "URGENT: critical threshold breached")
		}
		return strings.Join(parts, "; ")

	case store.VerdictSwitch:
		var parts []string
		if run.Accuracy < 0.95 {
			parts = append(parts, fmt.Sprintf("accuracy %.1f%% below threshold 95.0%%", run.Accuracy*100))
		}
		if run.ROIScore > 0 && run.ROIScore < 0.05 {
			parts = append(parts, fmt.Sprintf("ROI %.2f below threshold 0.05 (cost too high relative to accuracy)", run.ROIScore))
		}
		if len(parts) == 0 {
			parts = append(parts, "threshold breached")
		}
		return strings.Join(parts, "; ")

	case store.VerdictKeep:
		var parts []string
		parts = append(parts, fmt.Sprintf("accuracy=%.1f%%", run.Accuracy*100))
		if run.AvgTurnMs > 0 {
			parts = append(parts, fmt.Sprintf("avg_response=%.1fs", run.AvgTurnMs/1000))
		}
		if run.ROIScore > 0 {
			parts = append(parts, fmt.Sprintf("roi=%.2f", run.ROIScore))
		} else {
			parts = append(parts, "roi=N/A (free model or no billing data)")
		}
		return fmt.Sprintf("All thresholds passed (%s)", strings.Join(parts, ", "))

	default:
		return "-"
	}
}

// VerdictExplanation holds structured context for decision explainability rendering.
type VerdictExplanation struct {
	// Current state.
	RequiredQuality    float64
	CurrentQuality     float64
	RequiredROI        float64
	CurrentROI         float64
	CurrentCostLabel   string
	IsCurrentModelFree bool

	// Scenario classification.
	FailureType           string
	IsFreeToPayTransition bool
	IsQualityConstrained  bool
	IsInterimRec          bool

	// Recommendation context.
	RecommendedModel     string
	RecommendedQuality   string
	RecommendedCostLabel string

	// Impact.
	QualityGapStr       string
	CostImpactStr       string
	HasReliableCostData bool
}

// buildVerdictExplanation classifies verdict explainability scenarios from run data.
func buildVerdictExplanation(run store.BenchmarkRun, pricing map[string]float64, minAccuracy float64, minROI float64) VerdictExplanation {
	if run.Verdict == store.VerdictInsufficientData {
		return VerdictExplanation{}
	}
	if pricing == nil {
		return VerdictExplanation{}
	}

	currentPrice, hasCurrentPrice := pricing[run.Model]
	recommendedPrice, hasRecommendedPrice := pricing[run.RecommendedModel]
	isCurrentFree := hasCurrentPrice && currentPrice == 0
	hasReliableCostData := run.TotalCostUSD > 0
	roiActive := hasCurrentPrice && currentPrice > 0 && hasReliableCostData

	ex := VerdictExplanation{
		RequiredQuality:     minAccuracy,
		CurrentQuality:      run.Accuracy,
		RequiredROI:         minROI,
		CurrentROI:          run.ROIScore,
		CurrentCostLabel:    formatCurrentCostLabel(run, pricing),
		IsCurrentModelFree:  isCurrentFree,
		RecommendedModel:    run.RecommendedModel,
		RecommendedQuality:  fmt.Sprintf("≥%.0f%% (meets threshold)", minAccuracy*100),
		QualityGapStr:       formatQualityGap(run.Accuracy, minAccuracy),
		HasReliableCostData: hasReliableCostData,
	}

	if run.RecommendedModel != "" {
		ex.RecommendedModel = store.NormalizeModelName(run.RecommendedModel)
		if hasRecommendedPrice {
			ex.RecommendedCostLabel = formatEstimatedCostLabel(recommendedPrice)
		}
	}

	currentCostForImpact := currentPrice
	if hasReliableCostData && run.ROIScore > 0 {
		// Compare costs at the same per-session scale:
		// - recommendedPrice is configured per-session pricing
		// - current cost is derived from ROI (accuracy / ROI = cost_per_session)
		currentCostForImpact = run.Accuracy / run.ROIScore
	}
	if isCurrentFree {
		currentCostForImpact = 0
	}
	if run.RecommendedModel != "" && hasRecommendedPrice {
		ex.CostImpactStr = formatCostImpact(currentCostForImpact, recommendedPrice)
	}

	if run.Accuracy < minAccuracy {
		ex.FailureType = "quality-gap"
		if isCurrentFree {
			ex.IsFreeToPayTransition = true
		} else if !hasReliableCostData {
			// Deliberately grouped as cost-data-missing: both missing billing telemetry
			// and incomplete pricing block reliable cost guidance for a paid model.
			ex.FailureType = "cost-data-missing"
			ex.IsInterimRec = true
		}
		return ex
	}

	requiresSwitch := run.Verdict == store.VerdictSwitch || run.Verdict == store.VerdictUrgentSwitch
	recommendedMaintainsQuality := run.Accuracy >= minAccuracy && run.RecommendedModel != ""
	if requiresSwitch && run.ROIScore < minROI {
		if !roiActive || !hasRecommendedPrice {
			// Deliberately grouped as cost-data-missing: ROI-triggered switch cannot be
			// explained reliably without both current and recommended cost inputs.
			ex.FailureType = "cost-data-missing"
			return ex
		}
		if !recommendedMaintainsQuality {
			ex.FailureType = "keep"
			return ex
		}
		if recommendedPrice > 0 && recommendedPrice < currentCostForImpact {
			ex.FailureType = "cost-optimization"
			ex.IsQualityConstrained = true
			return ex
		}
	}

	ex.FailureType = "keep"
	return ex
}

func formatCostImpact(currentCost float64, recommendedCost float64) string {
	if currentCost <= 0 {
		if recommendedCost <= 0 {
			return "was $0"
		}
		return fmt.Sprintf("was $0 (+$%.2f/session)", recommendedCost)
	}
	delta := recommendedCost - currentCost
	if delta > 0 {
		return fmt.Sprintf("+$%.2f/session", delta)
	}
	if delta < 0 {
		return fmt.Sprintf("saves $%.2f/session", -delta)
	}
	return "$0/session change"
}

func formatQualityGap(current float64, required float64) string {
	diff := (current - required) * 100
	if diff >= 0 {
		return fmt.Sprintf("+%.0f%%", diff)
	}
	return fmt.Sprintf("%.0f%%", diff)
}

func formatCurrentCostLabel(run store.BenchmarkRun, pricing map[string]float64) string {
	currentPrice, inPricing := pricing[run.Model]

	// If model not in pricing table, we don't know its cost
	if !inPricing {
		if run.TotalCostUSD > 0 {
			return fmt.Sprintf("$%.2f/session (actual)", run.TotalCostUSD)
		}
		return "unknown (no billing telemetry)"
	}

	// Model is in pricing table
	if currentPrice == 0 {
		return "free"
	}
	if run.TotalCostUSD > 0 {
		return fmt.Sprintf("$%.2f/session (actual)", run.TotalCostUSD)
	}
	// currentPrice > 0 is guaranteed at this point
	return fmt.Sprintf("$%.2f/session (estimated)", currentPrice)
}

func formatEstimatedCostLabel(cost float64) string {
	if cost <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f/session (estimated)", cost)
}

func renderScenario1(sb *strings.Builder, run store.BenchmarkRun, ex VerdictExplanation) {
	writeDetailField(sb, "Scenario", "⚠ QUALITY INSUFFICIENT (Free Model)")
	writeDetailField(sb, "Current", fmt.Sprintf("%s (%s)", store.NormalizeModelName(run.Model), ex.CurrentCostLabel))
	writeDetailField(sb, "Threshold", fmt.Sprintf("%.0f%% accuracy", ex.RequiredQuality*100))
	writeDetailField(sb, "Accuracy", fmt.Sprintf("%.1f%% (gap %s)", ex.CurrentQuality*100, ex.QualityGapStr))
	writeDetailField(sb, "Best Option", formatRecommendedModel(ex.RecommendedModel))
	writeDetailField(sb, "Cost", formatRecommendedCost(ex.RecommendedCostLabel, ex.CostImpactStr))
	writeDetailField(sb, "Decision", switchDecisionText(run.Verdict, "accept cost to meet quality requirement"))
}

func renderScenario2(sb *strings.Builder, run store.BenchmarkRun, ex VerdictExplanation) {
	writeDetailField(sb, "Scenario", "⚠ QUALITY INSUFFICIENT (Paid Model)")
	writeDetailField(sb, "Current", fmt.Sprintf("%s (%s)", store.NormalizeModelName(run.Model), ex.CurrentCostLabel))
	writeDetailField(sb, "Threshold", fmt.Sprintf("%.0f%% accuracy", ex.RequiredQuality*100))
	writeDetailField(sb, "Accuracy", fmt.Sprintf("%.1f%% (gap %s)", ex.CurrentQuality*100, ex.QualityGapStr))
	writeDetailField(sb, "Best Option", formatRecommendedModel(ex.RecommendedModel))
	writeDetailField(sb, "Cost", formatRecommendedCost(ex.RecommendedCostLabel, ex.CostImpactStr))
	writeDetailField(sb, "Decision", switchDecisionText(run.Verdict, "tier upgrade required"))
}

func renderScenario3(sb *strings.Builder, run store.BenchmarkRun, ex VerdictExplanation) {
	writeDetailField(sb, "Scenario", "✓ QUALITY SUFFICIENT — Cost Optimization Available")
	writeDetailField(sb, "Current", fmt.Sprintf("%s (%s)", store.NormalizeModelName(run.Model), ex.CurrentCostLabel))
	writeDetailField(sb, "Threshold", fmt.Sprintf("%.0f%% accuracy", ex.RequiredQuality*100))
	writeDetailField(sb, "Accuracy", fmt.Sprintf("%.1f%% (gap %s)", ex.CurrentQuality*100, ex.QualityGapStr))
	writeDetailField(sb, "ROI", fmt.Sprintf("%.2f (threshold %.2f)", ex.CurrentROI, ex.RequiredROI))
	writeDetailField(sb, "Optimize", formatRecommendedModel(ex.RecommendedModel))
	writeDetailField(sb, "Cost", formatRecommendedCost(ex.RecommendedCostLabel, ex.CostImpactStr))
	writeDetailField(sb, "Decision", switchDecisionText(run.Verdict, "reduce cost while preserving quality"))
}

func renderScenario4(sb *strings.Builder, run store.BenchmarkRun, ex VerdictExplanation) {
	if ex.IsCurrentModelFree {
		writeDetailField(sb, "Scenario", "✓ QUALITY SUFFICIENT + FREE")
	} else {
		writeDetailField(sb, "Scenario", "✓ QUALITY SUFFICIENT")
	}
	writeDetailField(sb, "Current", fmt.Sprintf("%s (%s)", store.NormalizeModelName(run.Model), ex.CurrentCostLabel))
	writeDetailField(sb, "Threshold", fmt.Sprintf("%.0f%% accuracy", ex.RequiredQuality*100))
	writeDetailField(sb, "Accuracy", fmt.Sprintf("%.1f%% (gap %s)", ex.CurrentQuality*100, ex.QualityGapStr))
	writeDetailField(sb, "Decision", "✅ KEEP")
}

func renderScenario5(sb *strings.Builder, run store.BenchmarkRun, ex VerdictExplanation) {
	writeDetailField(sb, "Scenario", "⚠ QUALITY INSUFFICIENT (Cost Data Unavailable)")
	writeDetailField(sb, "Current", fmt.Sprintf("%s (%s)", store.NormalizeModelName(run.Model), ex.CurrentCostLabel))
	writeDetailField(sb, "Threshold", fmt.Sprintf("%.0f%% accuracy", ex.RequiredQuality*100))
	writeDetailField(sb, "Accuracy", fmt.Sprintf("%.1f%% (gap %s)", ex.CurrentQuality*100, ex.QualityGapStr))
	writeDetailField(sb, "Interim", formatRecommendedModel(ex.RecommendedModel))
	if ex.RecommendedCostLabel == "" {
		writeDetailField(sb, "Cost", "unknown")
	} else {
		writeDetailField(sb, "Cost", ex.RecommendedCostLabel)
	}
	if run.Verdict == store.VerdictUrgentSwitch {
		writeDetailField(sb, "Decision", "URGENT SWITCH (temporary)")
	} else {
		writeDetailField(sb, "Decision", "SWITCH (temporary)")
	}
	writeDetailField(sb, "Note", "Data pending — will optimize for cost after billing telemetry is available")
}

func renderScenarioUnknown(sb *strings.Builder, run store.BenchmarkRun) {
	writeDetailField(sb, "Scenario", "Decision rationale unavailable")
	writeDetailField(sb, "Current", store.NormalizeModelName(run.Model))
	writeDetailField(sb, "Decision", string(run.Verdict))
}

func formatRecommendedModel(model string) string {
	if model == "" {
		return "N/A"
	}
	return fmt.Sprintf("🎯 %s", model)
}

func formatRecommendedCost(costLabel string, impactLabel string) string {
	if costLabel == "" && impactLabel == "" {
		return "unknown"
	}
	if costLabel == "" {
		return impactLabel
	}
	if impactLabel == "" {
		return costLabel
	}
	return fmt.Sprintf("%s (%s)", costLabel, impactLabel)
}

func switchDecisionText(verdict store.VerdictType, message string) string {
	if verdict == store.VerdictUrgentSwitch {
		return fmt.Sprintf("URGENT SWITCH — %s", message)
	}
	return fmt.Sprintf("SWITCH — %s", message)
}

// evaluateAgentContext returns a qualitative assessment of agent mission fulfillment.
// Uses accuracy and sample size as signals — tool_success_rate is excluded because
// it is always 1.0 in practice and provides no signal.
func evaluateAgentContext(run store.BenchmarkRun) string {
	acc := run.Accuracy
	n := run.SampleSize

	// For INSUFFICIENT_DATA, note we have limited evidence.
	insufficientPrefix := ""
	if run.Verdict == store.VerdictInsufficientData {
		insufficientPrefix = "Limited data — "
	}

	highAcc := acc >= 0.99
	goodAcc := acc >= 0.95

	switch run.AgentID {
	case "sdd-orchestrator":
		if highAcc && n >= 50 {
			return insufficientPrefix + "Coordinating effectively across agents"
		} else if goodAcc {
			return insufficientPrefix + "Coordination mostly effective — minor errors detected"
		}
		return insufficientPrefix + "Coordination issues — orchestrator may need guidance"

	case "sdd-apply":
		if highAcc {
			return insufficientPrefix + "Implementations landing correctly"
		} else if goodAcc {
			return insufficientPrefix + "Most implementations successful — some corrections needed"
		}
		return insufficientPrefix + "Implementation failures detected — review task definitions"

	case "sdd-explore":
		if highAcc && n >= 50 {
			return insufficientPrefix + "Thorough exploration — investigations well-grounded"
		} else if goodAcc {
			return insufficientPrefix + "Adequate exploration — consider wider codebase coverage"
		}
		return insufficientPrefix + "Shallow or error-prone exploration"

	case "sdd-verify":
		if highAcc {
			return insufficientPrefix + "Validation executing correctly — spec compliance confirmed"
		} else if goodAcc {
			return insufficientPrefix + "Validation mostly passing — some spec gaps detected"
		}
		return insufficientPrefix + "Validation failures — implementation may not match specs"

	case "sdd-spec":
		if highAcc {
			return insufficientPrefix + "Specs being written correctly"
		}
		return insufficientPrefix + "Spec generation issues — review proposal inputs"

	case "sdd-design":
		if highAcc {
			return insufficientPrefix + "Design artifacts generated correctly"
		}
		return insufficientPrefix + "Design generation issues — proposal may need more detail"

	case "sdd-propose":
		if highAcc {
			return insufficientPrefix + "Proposals created correctly from explorations"
		}
		return insufficientPrefix + "Proposal failures — check exploration output quality"

	case "sdd-tasks":
		if highAcc {
			return insufficientPrefix + "Task breakdown working correctly"
		}
		return insufficientPrefix + "Task breakdown issues — specs may be ambiguous"

	case "sdd-init":
		if highAcc {
			return insufficientPrefix + "Bootstrap executing correctly"
		}
		return insufficientPrefix + "Bootstrap failures — check project configuration"

	case "sdd-archive":
		if highAcc {
			return insufficientPrefix + "Archiving completing correctly"
		}
		return insufficientPrefix + "Archive failures — verify change artifacts are complete"

	case "igris":
		if highAcc {
			return insufficientPrefix + "Conversational and coordination tasks completing successfully"
		} else if goodAcc {
			return insufficientPrefix + "Mostly effective — occasional errors in task handling"
		}
		return insufficientPrefix + "Elevated error rate — review recent task complexity"

	default:
		if highAcc {
			return insufficientPrefix + "Agent performing within expected parameters"
		} else if goodAcc {
			return insufficientPrefix + "Mostly within parameters — minor issues detected"
		}
		return insufficientPrefix + "Performance below expected thresholds for this role"
	}
}

// writeDetailField writes a single label: value line to the string builder.
func writeDetailField(sb *strings.Builder, label, value string) {
	sb.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-9s", label+":")))
	sb.WriteString(" ")
	sb.WriteString(detailPanelStyle.Render(value))
	sb.WriteString("\n")
}

// isNoData returns true when a BenchmarkRun is a placeholder (no real run data).
// A run is considered NO_DATA when RunAt is the zero time (never been run).
func isNoData(run store.BenchmarkRun) bool {
	return run.RunAt.IsZero()
}

// formatBenchmarkRow converts a BenchmarkRun into display columns.
// Columns: Time | Agent | Model | Samples | Accuracy | Avg Response | Verdict | → Switch To | Savings
// For NO_DATA rows, metric fields are rendered as "-".
// Intraweek runs are labelled with "(IW)" suffix on the Time column.
func formatBenchmarkRow(run store.BenchmarkRun, pricing map[string]float64) []string {
	// Handle placeholder rows (agent discovered but no runs yet).
	if isNoData(run) {
		return []string{"-", run.AgentID, "-", "-", "-", "-", "NO DATA", "-", "-"}
	}

	date := run.RunAt.Local().Format("2006-01-02 15:04")
	// Append "(IW)" marker for intraweek runs.
	if run.RunKind == store.RunKindIntraweek {
		date += " (IW)"
	}

	model := run.Model
	if model == "" {
		model = "-"
	}
	samples := fmt.Sprintf("%d", run.SampleSize)

	accuracy := fmt.Sprintf("%.1f%%", run.Accuracy*100)
	// Use AvgTurnMs (clean turn latency from complete events only).
	// Fall back to P95LatencyMs for runs recorded before the migration.
	turnMs := run.AvgTurnMs
	if turnMs <= 0 {
		turnMs = run.P95LatencyMs
	}
	var avgResp string
	if turnMs <= 0 {
		avgResp = "0.0s"
	} else {
		avgResp = fmt.Sprintf("%.1fs", turnMs/1000)
	}

	// → Switch To column: show RecommendedModel only for SWITCH/URGENT_SWITCH.
	switchTo := "-"
	if run.RecommendedModel != "" &&
		(run.Verdict == store.VerdictSwitch || run.Verdict == store.VerdictUrgentSwitch) {
		switchTo = run.RecommendedModel
	}

	// Savings column.
	_, savingsStr := computeSavings(run.Model, run.RecommendedModel, run.Verdict, pricing)

	return []string{date, run.AgentID, model, samples, accuracy, avgResp, string(run.Verdict), switchTo, savingsStr}
}

// computeSavings returns the savings ratio (0.0–1.0) and a formatted string
// (e.g. "~45%") given the current and recommended model names.
// Returns (0, "-") when the calculation is not applicable or pricing is unknown.
func computeSavings(currentModel, recommendedModel string, verdict store.VerdictType, pricing map[string]float64) (float64, string) {
	if verdict != store.VerdictSwitch && verdict != store.VerdictUrgentSwitch {
		return 0, "-"
	}
	if recommendedModel == "" {
		return 0, "-"
	}
	if len(pricing) == 0 {
		return 0, "-"
	}
	currentPrice, ok1 := pricing[currentModel]
	recommendedPrice, ok2 := pricing[recommendedModel]
	if !ok1 || !ok2 {
		return 0, "?"
	}
	if currentPrice <= 0 || recommendedPrice <= 0 {
		// Free models or invalid prices do not produce savings.
		return 0, "-"
	}
	savings := (1 - recommendedPrice/currentPrice) * 100
	if savings <= 0 {
		return 0, "-"
	}
	return savings, fmt.Sprintf("~%.0f%%", savings)
}

// verdictStyle returns the lipgloss style for a verdict.
func verdictStyle(v store.VerdictType) lipgloss.Style {
	switch v {
	case store.VerdictKeep:
		return verdictKeep
	case store.VerdictSwitch:
		return verdictSwitch
	case store.VerdictUrgentSwitch:
		return verdictUrgent
	default:
		return verdictOther
	}
}
