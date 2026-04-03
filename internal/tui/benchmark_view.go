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
// Columns: Time | Agent | Type | Accuracy | Avg Response | Verdict | → Model | Savings
// "Time" shows full date+time (YYYY-MM-DD HH:MM) so width is 17 to avoid truncation.
var (
	benchColWidths = []int{17, 16, 9, 10, 13, 18, 16, 8}
	benchColNames  = []string{"Time", "Agent", "Type", "Accuracy", "Avg Response", "Verdict", "→ Model", "Savings"}
)

// verdictColIdx is the index of the Verdict column in benchColNames/benchColWidths.
// Defined as a constant so the rendering code stays in sync with the column layout.
const verdictColIdx = 5

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
	runs      []store.BenchmarkRun // rows for the current cycle (one per agent, placeholder if no run)
	agents    []discovery.AgentInfo
	typeByID  map[string]string   // agentID → type label (primary/subagent/built-in/all)
	trendByID map[string][]string // agentID → verdict trend (oldest first)
	err       error
	// cursor is the row index within the current cycle's agent list.
	cursor  int
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
}

// NewBenchmarkModel creates a BenchmarkModel wired to the given BenchmarkStore.
// dataDir is the Metronous data directory (e.g. ~/.metronous/data); pricing is
// loaded from dataDir/../thresholds.json. Pass an empty string to disable pricing.
// workDir is used for project-level agent discovery; pass os.Getwd() from the caller.
// r is an optional IntraweekRunner; pass nil to disable F5 manual runs.
func NewBenchmarkModel(bs store.BenchmarkStore, dataDir string, workDir string, r IntraweekRunner) BenchmarkModel {
	return BenchmarkModel{
		bs:      bs,
		loading: true,
		pricing: loadModelPricing(dataDir),
		agents:  discovery.DiscoverAgents(workDir),
		workDir: workDir,
		runner:  r,
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
			}
			// Unfreeze detail so it follows the cursor.
			m.detailFrozen = false
		case "down", "j":
			// Move selection one row down within the current cycle.
			if m.cursor < len(m.runs)-1 {
				m.cursor++
			}
			// Unfreeze detail so it follows the cursor.
			m.detailFrozen = false
		case "pgdown":
			// Move to the next (older) cycle.
			if m.cycleIndex < len(m.cycles)-1 {
				m.cycleIndex++
				m.cursor = 0
				m.detailFrozen = false
				return m, m.fetchRuns()
			}
		case "pgup":
			// Move to the previous (newer) cycle.
			if m.cycleIndex > 0 {
				m.cycleIndex--
				m.cursor = 0
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

		// Build a lookup: agentID → run in this cycle (last run if multiple per agent per cycle).
		runByAgent := make(map[string]store.BenchmarkRun, len(windowRuns))
		for _, r := range windowRuns {
			if existing, ok := runByAgent[r.AgentID]; !ok || r.RunAt.After(existing.RunAt) {
				runByAgent[r.AgentID] = r
			}
		}

		// ── 5. Build one row per agent (NO RUN placeholder if absent) ─────────
		var page []store.BenchmarkRun
		for _, agentID := range allIDs {
			if run, ok := runByAgent[agentID]; ok {
				page = append(page, run)
			} else {
				// Placeholder: AgentID set, RunAt zero → isNoData() returns true.
				page = append(page, store.BenchmarkRun{AgentID: agentID})
			}
		}

		// Sort: primary → subagent → all → built-in, then alphabetical.
		sort.Slice(page, func(i, j int) bool {
			ti := agentTypeOrder(typeByID[page[i].AgentID])
			tj := agentTypeOrder(typeByID[page[j].AgentID])
			if ti != tj {
				return ti < tj
			}
			return page[i].AgentID < page[j].AgentID
		})

		// ── 6. Fetch verdict trends for each agent in the page (last 8 weeks) ─
		trendByID := make(map[string][]string, len(page))
		for _, run := range page {
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

	// Data rows — m.runs already contains only the current page (maxBenchmarkRows rows max).
	// The cursor is a local index within this page.
	for i, run := range m.runs {
		agentType := m.typeByID[run.AgentID]
		row := formatBenchmarkRow(run, agentType, m.pricing)
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
		// verdictColIdx = 5 (Time, Agent, Type, Accuracy, P95 Latency, Verdict, → Model, Savings)
		rendered := renderRow(row[:verdictColIdx], benchColWidths[:verdictColIdx], baseStyle)
		// Verdict column: remove cursor background from this specific column.
		var verdictCell string
		if isNoDataRow {
			verdictCell = baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[verdictColIdx], row[verdictColIdx]))
		} else {
			verdictCell = verdictStyle(run.Verdict).Render(
				fmt.Sprintf("%-*s", benchColWidths[verdictColIdx], row[verdictColIdx]))
		}
		rendered += verdictCell
		// → Model column (index 6).
		rendered += " " + baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[6], row[6]))
		// Savings column (index 7).
		rendered += " " + baseStyle.Render(fmt.Sprintf("%-*s", benchColWidths[7], row[7]))
		// Write the row directly — do NOT re-wrap with baseStyle.Render() as that
		// would strip the inner ANSI colour codes (verdict colour, etc.).
		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	// Pagination footer: show cycle number (1-based from newest).
	sb.WriteString("\n")
	totalCycles := len(m.cycles)
	var cycleLabel string
	if totalCycles > 0 {
		cycleStart := m.cycles[m.cycleIndex]
		cycleLabel = fmt.Sprintf("cycle %d/%d  (week of %s)",
			m.cycleIndex+1, totalCycles, cycleStart.Local().Format("2006-01-02"))
	} else {
		cycleLabel = "cycle 1/1"
	}
	footerText := fmt.Sprintf("  %d agents  |  %s  (PgUp/PgDn to change cycle, ↑↓ to select, Enter to freeze detail)",
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
		sb.WriteString(renderDetailPanel(detailRun, m.pricing, trend))
	}

	return sb.String()
}

// renderDetailPanel renders the decision rationale panel for the selected run.
// trend is the verdict history for the agent (oldest first); pass nil if unavailable.
func renderDetailPanel(run store.BenchmarkRun, pricing map[string]float64, trend []string) string {
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

	// Avoid multi-line layout shifts from DecisionReason (which can contain
	// newlines). Keeping the detail panel single-line per field prevents
	// terminal scrolling artifacts while moving the cursor.
	reason := strings.ReplaceAll(run.DecisionReason, "\n", " ")
	reason = clamp(reason)

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
	writeDetailField(&sb, "Context", clamp(evaluateAgentContext(run)))

	// Trend line: show last N verdicts with direction indicator.
	if len(trend) > 0 {
		trendStr := formatVerdictTrend(trend)
		writeDetailField(&sb, "Trend", clamp(trendStr))
	}

	return sb.String()
}

// verdictAbbrev returns a single-character abbreviation for a verdict.
// Legend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  ?=INSUFFICIENT_DATA
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
// e.g. "K → K → S → K → ?  (↓ degrading)\n  Legend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  ?=INSUFFICIENT_DATA"
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
	return fmt.Sprintf("%s  (%s)\nLegend: K=KEEP  S=SWITCH  U=URGENT_SWITCH  ?=INSUFFICIENT_DATA", trendLine, direction)
}

// verdictSeverity returns a numeric severity for a verdict (lower = better).
func verdictSeverity(v string) int {
	switch store.VerdictType(v) {
	case store.VerdictKeep:
		return 0
	case store.VerdictSwitch:
		return 2
	case store.VerdictUrgentSwitch:
		return 3
	default:
		return 1
	}
}

// trendDirection returns a direction indicator string for a slice of verdict strings.
// If the most recent verdict is INSUFFICIENT_DATA, the direction is "unknown" — we
// cannot assess the trend when the latest run lacks enough data.
// If only the first endpoint is INSUFFICIENT_DATA (but the last is not), the
// comparison is still valid and uses the last known verdict.
func trendDirection(verdicts []string) string {
	if len(verdicts) < 2 {
		return "→ stable"
	}
	last := verdicts[len(verdicts)-1]

	// If the latest verdict has insufficient data, we cannot determine direction.
	if last == string(store.VerdictInsufficientData) {
		return "→ unknown"
	}

	// Find the most recent non-INSUFFICIENT_DATA verdict before the last one
	// to use as the comparison baseline.
	first := ""
	for i := len(verdicts) - 2; i >= 0; i-- {
		if verdicts[i] != string(store.VerdictInsufficientData) {
			first = verdicts[i]
			break
		}
	}
	if first == "" {
		// All previous verdicts were INSUFFICIENT_DATA — no baseline to compare.
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

// evaluateAgentContext returns a short qualitative assessment of whether the agent
// fulfilled its mission, based on its known role and available telemetry metrics.
func evaluateAgentContext(run store.BenchmarkRun) string {
	switch run.AgentID {
	case "sdd-orchestrator":
		// Mission: coordinate, never do work inline
		// Good: high tool_success (delegates correctly)
		// Bad: if tool success < 0.8, likely doing inline work
		if run.ToolSuccessRate >= 0.9 {
			return "Coordinating effectively — delegations succeeding at expected rate"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some delegation failures detected — may be attempting inline work"
		}
		return "High failure rate — orchestrator may be bypassing delegation pattern"

	case "sdd-apply":
		// Mission: implement code changes
		// Good: high tool success (edits, writes working)
		// Bad: low success means broken implementations
		if run.ToolSuccessRate >= 0.9 {
			return "Implementations landing correctly — code changes applied successfully"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some implementation failures — review task definitions for clarity"
		}
		return "High implementation failure rate — task definitions may be incomplete"

	case "sdd-explore":
		// Mission: investigate codebase and think through ideas
		// Good: high tool success (reads, searches working)
		// Check: sample size indicates depth of exploration
		if run.SampleSize >= 50 && run.ToolSuccessRate >= 0.9 {
			return "Deep exploration with high read success — investigations thorough"
		} else if run.ToolSuccessRate >= 0.8 {
			return "Adequate exploration — consider deeper codebase analysis"
		}
		return "Shallow exploration detected — may be missing critical context"

	case "sdd-verify":
		// Mission: validate implementation against specs
		// Good: high tool success (reads, comparisons working)
		if run.ToolSuccessRate >= 0.9 {
			return "Validation passing — spec compliance checks executing correctly"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some validation failures — specs may need clarification"
		}
		return "Validation failing frequently — implementation may not match specs"

	case "sdd-spec":
		if run.ToolSuccessRate >= 0.9 {
			return "Spec writing succeeding — requirements captured correctly"
		}
		return "Spec generation issues — proposal inputs may be incomplete"

	case "sdd-design":
		if run.ToolSuccessRate >= 0.9 {
			return "Design artifacts generated successfully"
		}
		return "Design generation issues — proposal may need more detail"

	case "sdd-propose":
		if run.ToolSuccessRate >= 0.9 {
			return "Proposals being created from explorations correctly"
		}
		return "Proposal failures — exploration output may be insufficient"

	case "sdd-tasks":
		if run.ToolSuccessRate >= 0.9 {
			return "Task breakdown succeeding — specs and designs well-structured"
		}
		return "Task breakdown failures — specs may be ambiguous"

	case "sdd-init":
		if run.ToolSuccessRate >= 0.9 {
			return "Bootstrap executing correctly"
		}
		return "Bootstrap failures — check project configuration"

	case "sdd-archive":
		if run.ToolSuccessRate >= 0.9 {
			return "Archiving completing correctly"
		}
		return "Archive failures — verify change artifacts are complete"

	default:
		if run.ToolSuccessRate >= 0.9 {
			return "Agent performing within normal parameters"
		}
		return "Performance below expected thresholds for this agent role"
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
// agentType is the type label for the Type column (primary/subagent/built-in/all).
// For NO_DATA rows, metric fields are rendered as "-".
// Intraweek runs are labelled with "(IW)" suffix on the Time column to distinguish
// them from the scheduled weekly run within the same cycle.
func formatBenchmarkRow(run store.BenchmarkRun, agentType string, pricing map[string]float64) []string {
	if agentType == "" {
		agentType = "-"
	}

	// Handle placeholder rows (agent discovered but no runs yet).
	if isNoData(run) {
		return []string{"-", run.AgentID, agentType, "-", "-", "NO DATA", "-", "-"}
	}

	date := run.RunAt.Local().Format("2006-01-02 15:04")
	// Append "(IW)" marker for intraweek runs so the cycle view clearly shows
	// which runs were triggered manually vs the scheduled Sunday run.
	if run.RunKind == store.RunKindIntraweek {
		date += " (IW)"
	}

	accuracy := fmt.Sprintf("%.1f%%", run.Accuracy*100)
	// Use AvgTurnMs (clean turn latency from complete events only).
	// Fall back to P95LatencyMs for runs recorded before the migration.
	turnMs := run.AvgTurnMs
	if turnMs <= 0 {
		turnMs = run.P95LatencyMs
	}
	var p95 string
	if turnMs <= 0 {
		p95 = "0.0s"
	} else {
		p95 = fmt.Sprintf("%.1fs", turnMs/1000)
	}

	// → Model column: show RecommendedModel only for SWITCH/URGENT_SWITCH with a non-empty value.
	recommendedModel := "-"
	if run.RecommendedModel != "" &&
		(run.Verdict == store.VerdictSwitch || run.Verdict == store.VerdictUrgentSwitch) {
		recommendedModel = run.RecommendedModel
	}

	// Savings column.
	_, savingsStr := computeSavings(run.Model, run.RecommendedModel, run.Verdict, pricing)

	return []string{date, run.AgentID, agentType, accuracy, p95, string(run.Verdict), recommendedModel, savingsStr}
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
