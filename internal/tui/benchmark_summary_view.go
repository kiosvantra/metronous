package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/store"
)

// benchmarkSummaryRefreshInterval is slower than the detailed tab because summary data
// only changes when a new benchmark run is saved — not every 2 seconds.
const benchmarkSummaryRefreshInterval = 30 * time.Second

// benchmarkSummaryTickMsg is sent by the auto-refresh ticker.
type benchmarkSummaryTickMsg struct{ t time.Time }

// BenchmarkSummaryDataMsg carries aggregated per-agent/model summary rows.
type BenchmarkSummaryDataMsg struct {
	Rows                       []summaryRow
	LastWeeklyRunAt            time.Time
	LastIntraweekRunAt         time.Time
	LastWeeklyAttemptAt        time.Time
	LastIntraweekAttemptAt     time.Time
	LastWeeklyAttemptStatus    store.BenchmarkAttemptStatus
	LastIntraweekAttemptStatus store.BenchmarkAttemptStatus
	Err                        error
}

// summaryRow holds the aggregated metrics for a single (agent, model) pair.
type summaryRow struct {
	AgentID      string
	Model        string
	RawModel     string  // un-normalized model name with provider prefix (matches detailed view)
	IsActive     bool    // true when this model has run_status='active' in its most recent run
	Runs         int     // total benchmark runs (weekly + intraweek)
	AvgAccuracy  float64 // weighted average accuracy (all non-insufficient runs)
	AvgTurnMs    float64 // weighted average turn duration (all non-insufficient runs)
	TotalCostUSD float64 // cost from the run used for LastVerdict
	HealthScore  float64 // composite 0-100 (higher is better)
	LastVerdict  store.VerdictType
	LastRunAt    time.Time
}

// summaryColWidths and summaryColNames describe the summary table columns.
// Columns: Agent | Model | Runs | Accuracy | Avg Response | Last Cost | Health | Last Verdict
var (
	summaryColWidths = []int{18, 22, 5, 10, 13, 12, 8, 20}
	summaryColNames  = []string{"Agent", "Model", "Runs", "Accuracy", "Avg Response", "Last Cost", "Health", "Last Verdict"}
)

// healthStyle returns a colour for the health score (0-100).
func healthStyle(score float64) lipgloss.Style {
	switch {
	case score >= 80:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")) // green
	case score >= 50:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")) // yellow
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	}
}

// clearRunErrMsg is sent after a short delay to clear the run error and restore the F5 hint.
type clearRunErrMsg struct{}

// BenchmarkSummaryModel is the Bubble Tea sub-model for the benchmark summary tab.
type BenchmarkSummaryModel struct {
	bs                         store.BenchmarkStore
	rows                       []summaryRow
	err                        error
	cursor                     int
	offset                     int
	loading                    bool
	lastViewLines              int
	runner                     IntraweekRunner
	running                    bool
	runErr                     error
	lastWeeklyRunAt            time.Time
	lastIntraweekRunAt         time.Time
	lastWeeklyAttemptAt        time.Time
	lastIntraweekAttemptAt     time.Time
	lastWeeklyAttemptStatus    store.BenchmarkAttemptStatus
	lastIntraweekAttemptStatus store.BenchmarkAttemptStatus
	minROI                     float64 // from thresholds config
	// width and height are updated from tea.WindowSizeMsg so the view can
	// adapt column widths to the current terminal size.
	width  int
	height int
}

const maxSummaryRows = 10

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// currentSummaryColWidths returns a copy of summaryColWidths adjusted to fit
// within the current terminal width. When the width is unknown (zero) the
// original widths are returned unchanged.
func (m BenchmarkSummaryModel) currentSummaryColWidths() []int {
	out := make([]int, len(summaryColWidths))
	copy(out, summaryColWidths)
	if m.width <= 0 {
		return out
	}
	available := m.width - 4
	if available < 0 {
		available = m.width
	}
	return clampColumnWidths(out, available)
}

// NewBenchmarkSummaryModel creates a BenchmarkSummaryModel wired to the given BenchmarkStore.
// r is an optional IntraweekRunner; pass nil to disable F5 manual runs.
func NewBenchmarkSummaryModel(bs store.BenchmarkStore, r IntraweekRunner) BenchmarkSummaryModel {
	return BenchmarkSummaryModel{
		bs:      bs,
		loading: true,
		offset:  0,
		runner:  r,
		minROI:  0.05, // matches config.DefaultThresholdValues
	}
}

// Init returns the initial fetch command and starts the auto-refresh ticker.
func (m BenchmarkSummaryModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchSummary(),
		tea.Tick(benchmarkSummaryRefreshInterval, func(t time.Time) tea.Msg {
			return benchmarkSummaryTickMsg{t: t}
		}),
	)
}

// Update handles data, tick, and key messages.
func (m BenchmarkSummaryModel) Update(msg tea.Msg) (BenchmarkSummaryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case ConfigReloadedMsg:
		m.minROI = msg.Thresholds.Defaults.MinROIScore
		return m, nil

	case benchmarkSummaryTickMsg:
		return m, tea.Batch(
			tea.Tick(benchmarkSummaryRefreshInterval, func(t time.Time) tea.Msg {
				return benchmarkSummaryTickMsg{t: t}
			}),
			m.fetchSummary(),
		)

	case intraweekRunDoneMsg:
		m.running = false
		m.runErr = msg.Err
		cmds := []tea.Cmd{m.fetchSummary()}
		if msg.Err != nil {
			// Auto-clear the error after 4 seconds so F5 hint comes back.
			cmds = append(cmds, tea.Tick(4*time.Second, func(t time.Time) tea.Msg {
				return clearRunErrMsg{}
			}))
		}
		return m, tea.Batch(cmds...)

	case clearRunErrMsg:
		m.runErr = nil
		return m, nil

	case BenchmarkSummaryDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.rows = msg.Rows
			m.lastWeeklyRunAt = msg.LastWeeklyRunAt
			m.lastIntraweekRunAt = msg.LastIntraweekRunAt
			m.lastWeeklyAttemptAt = msg.LastWeeklyAttemptAt
			m.lastIntraweekAttemptAt = msg.LastIntraweekAttemptAt
			m.lastWeeklyAttemptStatus = msg.LastWeeklyAttemptStatus
			m.lastIntraweekAttemptStatus = msg.LastIntraweekAttemptStatus
			if m.cursor >= len(m.rows) {
				if len(m.rows) > 0 {
					m.cursor = len(m.rows) - 1
				} else {
					m.cursor = 0
				}
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			// Clamp offset so cursor is visible.
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
			if m.cursor >= m.offset+maxSummaryRows {
				m.offset = m.cursor - (maxSummaryRows - 1)
			}
			// Clamp offset to valid range.
			maxOffset := maxInt(0, len(m.rows)-maxSummaryRows)
			if m.offset > maxOffset {
				m.offset = maxOffset
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
				if m.cursor >= m.offset+maxSummaryRows {
					m.offset = m.cursor - (maxSummaryRows - 1)
				}
			}
		case "f5":
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

// fetchSummary returns a command that queries the BenchmarkStore and builds summary rows
// aggregated per (agent, model) pair.
//
// Aggregation scope: ALL runs (weekly + intraweek) are included in weighted metric
// averages (accuracy, response time, ROI, cost). More data produces better averages.
// The run count (Runs field) counts all runs. INSUFFICIENT_DATA runs are excluded from
// weighted averages but kept as fallback metrics.
//
// Display filter: only (agent, model) pairs that had at least one run in the 4 most
// recent distinct weekly cycles (by WindowStart) are shown. Older models are silently
// excluded.
//
// Active model marker: matches the detailed view — the model whose most recent run has
// run_status='active' is marked with IsActive=true. Only one model per agent is active.
//
// Model display: table column shows the normalized name (provider prefix stripped) via
// formatSummaryRow(). The raw provider-prefixed name is preserved in summaryRow.RawModel
// for use in the Agent History Summary detail panel below the table.
func (m BenchmarkSummaryModel) fetchSummary() tea.Cmd {
	if m.bs == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		status, err := loadBenchmarkRunStatus(ctx, m.bs)
		if err != nil {
			return BenchmarkSummaryDataMsg{Err: err}
		}

		agentIDs, err := m.bs.ListAgents(ctx)
		if err != nil {
			return BenchmarkSummaryDataMsg{Err: err}
		}

		// Aggregate per (agent, normalized-model) pair.
		type key struct{ agent, model string }
		type agg struct {
			rawModel     string  // most recent RawModel for display
			runs         int     // total run count (weekly + intraweek)
			totalSamples int     // weighted sum denominator (non-insufficient runs)
			sumAccuracy  float64 // weighted accuracy sum
			sumP95       float64 // weighted turn ms sum
			sumROI       float64 // weighted ROI sum
			roiSamples   int
			lastCostUSD  float64
			lastVerdict  store.VerdictType
			lastRunAt    time.Time       // most recent NON-INSUFFICIENT run (for verdict/cost display)
			mostRecentAt time.Time       // actual most recent run (for active-model tiebreaking)
			lastStatus   store.RunStatus // status of the actual most recent run for this model
			// Fallback metrics from the most recent run (used when all runs are
			// INSUFFICIENT_DATA so we don't display misleading 0% accuracy).
			lastAccuracy    float64
			lastP95         float64
			lastROI         float64
			weeklyWindowIDs map[int64]bool // distinct weekly WindowStart values (ms UTC)
		}
		aggMap := make(map[key]*agg)

		// Collect all distinct weekly WindowStart values across all agents to determine
		// the 4 most recent weekly cycles for the display filter.
		weeklyWindowSet := make(map[int64]bool)

		for _, agentID := range agentIDs {
			// Fetch up to 200 runs to cover ~4 years of weekly data.
			runs, err := m.bs.GetRuns(ctx, agentID, 200)
			if err != nil {
				continue
			}
			for _, r := range runs {
				if r.RunAt.IsZero() {
					continue
				}
				k := key{agentID, store.NormalizeModelName(r.Model)}
				a := aggMap[k]
				if a == nil {
					a = &agg{weeklyWindowIDs: make(map[int64]bool)}
					aggMap[k] = a
				}

				// Track distinct weekly WindowStart timestamps for the display filter.
				// Pre-migration rows have window_start=0; fall back to RunAt truncated
				// to the nearest week boundary (Sunday midnight UTC) as a proxy so that
				// old weekly runs are not silently excluded from the recent-cycles filter.
				isWeekly := r.RunKind == store.RunKindWeekly || r.RunKind == ""
				if isWeekly {
					var wID int64
					if !r.WindowStart.IsZero() {
						wID = r.WindowStart.UTC().UnixMilli()
					} else {
						// Approximate the weekly window using RunAt rounded down to
						// the most recent Sunday midnight UTC (7-day boundary).
						wID = r.RunAt.UTC().Truncate(7 * 24 * time.Hour).UnixMilli()
					}
					weeklyWindowSet[wID] = true
					a.weeklyWindowIDs[wID] = true
				}

				// INSUFFICIENT_DATA runs are excluded from weighted metric averages
				// because they have too few samples to be statistically meaningful.
				// However we keep their raw metrics as a fallback so that pairs
				// where ALL runs are insufficient don't show a misleading 0% accuracy.
				isInsufficient := r.Verdict == store.VerdictInsufficientData || r.SampleSize < 50
				if !isInsufficient {
					samples := r.SampleSize
					if samples <= 0 {
						samples = 1
					}
					a.totalSamples += samples
					a.sumAccuracy += r.Accuracy * float64(samples)
					// Use AvgTurnMs (turn duration from complete events only).
					// Fall back to AvgLatencyMs for runs recorded before the migration.
					turnMs := r.AvgTurnMs
					if turnMs <= 0 {
						turnMs = r.AvgLatencyMs
					}
					a.sumP95 += turnMs * float64(samples)
					// Accumulate ROI only when cost data is reliable (roi > 0).
					if r.ROIScore > 0 {
						a.sumROI += r.ROIScore * float64(samples)
						a.roiSamples += samples
					}
				}
				a.runs++

				// Track the actual most recent run for status and RawModel (all runs,
				// not just weekly), because run_status='active' is set on the most recent
				// run regardless of its kind.
				if r.RunAt.After(a.mostRecentAt) || a.mostRecentAt.IsZero() {
					a.mostRecentAt = r.RunAt
					a.lastStatus = r.Status
					// Use RawModel from the most recent run for display.
					if r.RawModel != "" {
						a.rawModel = r.RawModel
					}
					// Always update fallback metrics from the most recent run.
					a.lastAccuracy = r.Accuracy
					turnMs := r.AvgTurnMs
					if turnMs <= 0 {
						turnMs = r.AvgLatencyMs
					}
					a.lastP95 = turnMs
					a.lastROI = r.ROIScore
				}

				// Track the most recent NON-INSUFFICIENT run separately for
				// verdict/cost display (lastRunAt). This avoids showing misleading
				// INSUFFICIENT_DATA verdicts when a more meaningful verdict exists.
				if !isInsufficient {
					if r.RunAt.After(a.lastRunAt) || a.lastRunAt.IsZero() {
						a.lastRunAt = r.RunAt
						a.lastVerdict = r.Verdict
						a.lastCostUSD = r.TotalCostUSD
					}
				} else if a.lastVerdict == "" || a.lastVerdict == store.VerdictInsufficientData {
					// No prior non-insufficient run — use insufficient as fallback verdict.
					if r.RunAt.After(a.lastRunAt) || a.lastRunAt.IsZero() {
						a.lastRunAt = r.RunAt
						a.lastVerdict = r.Verdict
						a.lastCostUSD = r.TotalCostUSD
					}
				}
			}
		}

		// Determine the active model per agent: the (agent, model) pair whose most
		// recent run has run_status='active'. Uses mostRecentAt (the actual most
		// recent run time) for tiebreaking — not lastRunAt which only tracks
		// non-insufficient runs. Mirrors the detailed view logic at
		// benchmark_view.go:currentModelByAgent.
		activeModelByAgent := make(map[string]string)    // agentID → normalized model name
		activeRunAtByAgent := make(map[string]time.Time) // agentID → mostRecentAt of winner
		for k, a := range aggMap {
			if a.lastStatus == store.RunStatusActive {
				if prev, ok := activeRunAtByAgent[k.agent]; !ok || a.mostRecentAt.After(prev) {
					activeModelByAgent[k.agent] = k.model
					activeRunAtByAgent[k.agent] = a.mostRecentAt
				}
			}
		}

		// Compute the 4 most recent distinct weekly cycle timestamps.
		// Only (agent, model) pairs that had at least one run in any of these cycles
		// will be displayed; older models are silently excluded.
		const recentWeeklyCycles = 4
		recentWeeklyIDs := make(map[int64]bool)
		if len(weeklyWindowSet) > 0 {
			allWeeklyIDs := make([]int64, 0, len(weeklyWindowSet))
			for id := range weeklyWindowSet {
				allWeeklyIDs = append(allWeeklyIDs, id)
			}
			sort.Slice(allWeeklyIDs, func(i, j int) bool { return allWeeklyIDs[i] > allWeeklyIDs[j] })
			limit := recentWeeklyCycles
			if limit > len(allWeeklyIDs) {
				limit = len(allWeeklyIDs)
			}
			for _, id := range allWeeklyIDs[:limit] {
				recentWeeklyIDs[id] = true
			}
		}
		// isActiveInRecentCycles returns true when the (agent, model) pair had at least
		// one weekly run in any of the 4 most recent weekly cycles. When no weekly cycles
		// exist at all (DB has only intraweek runs), the filter is skipped and all pairs
		// are shown.
		isActiveInRecentCycles := func(a *agg) bool {
			if len(recentWeeklyIDs) == 0 {
				return true // no weekly runs in DB — skip filter
			}
			for id := range a.weeklyWindowIDs {
				if recentWeeklyIDs[id] {
					return true
				}
			}
			return false
		}

		minROI := m.minROI

		// Build sorted rows.
		var rows []summaryRow
		for k, a := range aggMap {
			// Display filter: skip pairs not active in the last 4 weekly cycles,
			// but always keep the agent's currently active model (matches detailed view).
			isActive := activeModelByAgent[k.agent] == k.model
			if !isActive && !isActiveInRecentCycles(a) {
				continue
			}

			avgAcc := 0.0
			avgP95 := 0.0
			avgROI := 0.0
			if a.totalSamples > 0 {
				// We have valid (non-insufficient) runs — use weighted averages.
				avgAcc = a.sumAccuracy / float64(a.totalSamples)
				avgP95 = a.sumP95 / float64(a.totalSamples)
			} else {
				// All runs were INSUFFICIENT_DATA — use the most recent run's raw
				// metrics so we don't display a misleading 0% accuracy / 0ms latency.
				avgAcc = a.lastAccuracy
				avgP95 = a.lastP95
			}
			if a.roiSamples > 0 {
				avgROI = a.sumROI / float64(a.roiSamples)
			} else {
				avgROI = a.lastROI
			}
			health := computeHealthScore(avgAcc, avgP95, a.lastVerdict, avgROI, minROI)

			// Store the raw (provider-prefixed) model name in Model so it is available
			// to formatSummaryRow (which normalizes it for table display) and to View()
			// (which shows it as a dim secondary line for active rows).
			// Fall back to the normalized key when RawModel was never recorded.
			displayModel := a.rawModel
			if displayModel == "" {
				displayModel = k.model // fall back to normalized name
			}

			rows = append(rows, summaryRow{
				AgentID:      k.agent,
				Model:        displayModel,
				RawModel:     a.rawModel,
				IsActive:     isActive,
				Runs:         a.runs,
				AvgAccuracy:  avgAcc,
				AvgTurnMs:    avgP95,
				TotalCostUSD: a.lastCostUSD,
				HealthScore:  health,
				LastVerdict:  a.lastVerdict,
				LastRunAt:    a.lastRunAt,
			})
		}

		// Sort: cascade sort matching the Detailed view pattern.
		// NO DATA rows (Runs==0) go last.
		// Within each agent: active model (IsActive=true) first, then other models by health desc.
		// Agents are ordered by their active model's health score desc (or best model if no active).
		// Tiebreaker: agentID asc, then model asc.
		// agentBestHealth maps agentID → health score used for inter-agent ordering.
		// Active model's health is authoritative; for agents with no active model,
		// use the highest health score across all their models.
		agentBestHealth := make(map[string]float64)
		agentHasActive := make(map[string]bool)
		for _, r := range rows {
			if r.IsActive {
				agentBestHealth[r.AgentID] = r.HealthScore
				agentHasActive[r.AgentID] = true
			}
		}
		// For agents with no active model, accumulate the max health score.
		for _, r := range rows {
			if !agentHasActive[r.AgentID] && r.HealthScore > agentBestHealth[r.AgentID] {
				agentBestHealth[r.AgentID] = r.HealthScore
			}
		}
		isNoDataRow := func(r summaryRow) bool { return r.Runs == 0 }
		// SliceStable prevents floating-point-induced reorderings when HealthScore values
		// are equal (or differ only in the last ULP due to non-deterministic map iteration
		// accumulation order). The tiebreaker chain below is fully deterministic, so stable
		// sort produces the same output on every refresh.
		sort.SliceStable(rows, func(i, j int) bool {
			// NO DATA placeholders always go last.
			if isNoDataRow(rows[i]) != isNoDataRow(rows[j]) {
				return !isNoDataRow(rows[i])
			}
			// Sort agents by their active model's health desc (best agent first).
			hi := agentBestHealth[rows[i].AgentID]
			hj := agentBestHealth[rows[j].AgentID]
			if rows[i].AgentID != rows[j].AgentID {
				if hi != hj {
					return hi > hj
				}
				return rows[i].AgentID < rows[j].AgentID
			}
			// Same agent: active model comes first.
			if rows[i].IsActive != rows[j].IsActive {
				return rows[i].IsActive
			}
			// Same agent, both non-active (or both active): sort by health desc.
			if rows[i].HealthScore != rows[j].HealthScore {
				return rows[i].HealthScore > rows[j].HealthScore
			}
			return rows[i].Model < rows[j].Model
		})

		return BenchmarkSummaryDataMsg{
			Rows:                       rows,
			LastWeeklyRunAt:            status.lastWeeklyRunAt,
			LastIntraweekRunAt:         status.lastIntraweekRunAt,
			LastWeeklyAttemptAt:        status.lastWeeklyAttemptAt,
			LastIntraweekAttemptAt:     status.lastIntraweekAttemptAt,
			LastWeeklyAttemptStatus:    status.lastWeeklyAttemptStatus,
			LastIntraweekAttemptStatus: status.lastIntraweekAttemptStatus,
		}
	}
}

// computeHealthScore returns a 0-100 composite score.
//
// Formula:
//   - Accuracy:  60 pts  (accuracy * 60) — primary signal
//   - Verdict:   25 pts  (KEEP=25, SWITCH=5, URGENT_SWITCH=0, INSUFFICIENT=10, other=5)
//   - ROI:       15 pts  (7=neutral when no cost data; scaled 0-15 by minROIScore reference)
//
// Latency is intentionally excluded from HealthScore because the available
// p95_latency_ms data is noisy (includes cumulative session time, not per-call
// latency). It will be reintroduced once clean turn-level latency data is captured.
//
// roiScore is accuracy / cost_per_session; 0 means no cost data available.
// minROIScore is the threshold from config used as the reference point for scaling.
func computeHealthScore(accuracy, _ float64, verdict store.VerdictType, roiScore, minROIScore float64) float64 {
	// Accuracy: 0-60 pts — the most reliable signal we have.
	accPart := accuracy * 60

	// Verdict: 0-25 pts.
	var verdictPart float64
	switch verdict {
	case store.VerdictKeep:
		verdictPart = 25
	case store.VerdictSwitch:
		verdictPart = 5
	case store.VerdictUrgentSwitch:
		verdictPart = 0
	case store.VerdictInsufficientData:
		verdictPart = 10
	default:
		verdictPart = 5
	}

	// ROI: 0-15 pts.
	// No cost data (free model or no billing) → neutral 7pts.
	// Paid model with roi → scaled 0-15 using minROIScore as reference.
	var roiPart float64
	if roiScore <= 0 || minROIScore <= 0 {
		roiPart = 7
	} else {
		roiPart = 15 * min64(1, roiScore/minROIScore)
	}

	score := accPart + verdictPart + roiPart
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// min64 returns the minimum of two float64 values.
func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// max64 returns the maximum of two float64 values.
func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// View renders the benchmark summary tab.
func (m *BenchmarkSummaryModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Benchmark History Summary") + "\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  Last weekly saved run: %s", formatBenchmarkRunStatus(m.lastWeeklyRunAt))) + "\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  Last weekly attempt: %s", formatBenchmarkAttemptStatus(m.lastWeeklyAttemptAt, m.lastWeeklyAttemptStatus))) + "\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  Last intraweek saved run: %s", formatBenchmarkRunStatus(m.lastIntraweekRunAt))) + "\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  Last intraweek attempt: %s", formatBenchmarkAttemptStatus(m.lastIntraweekAttemptAt, m.lastIntraweekAttemptStatus))) + "\n\n")

	// F5 indicator — always visible below the title.
	f5Style := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true) // yellow
	dimS := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	if m.running {
		runningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true)
		sb.WriteString(runningStyle.Render("  ⏳ Running intraweek benchmark...") + "\n\n")
	} else if m.runErr != nil {
		errRunStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		sb.WriteString(errRunStyle.Render(fmt.Sprintf("  ✗ Intraweek run failed: %v", m.runErr)) + "\n\n")
	} else {
		sb.WriteString(dimS.Render("  Press") + " " + f5Style.Render("F5") + dimS.Render(" to run an intraweek benchmark now") + "\n\n")
	}

	if m.loading {
		sb.WriteString(dimStyle.Render("  Loading…") + "\n")
		return sb.String()
	}
	if m.err != nil {
		sb.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", m.err)) + "\n")
		return sb.String()
	}
	if len(m.rows) == 0 {
		sb.WriteString(dimStyle.Render("  No benchmark runs yet. Run a benchmark to see the summary here.") + "\n")
		return sb.String()
	}

	widths := m.currentSummaryColWidths()

	// Header.
	sb.WriteString(renderRow(summaryColNames, widths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(widths)) + "\n")

	// Data rows.
	offset := m.offset
	maxOffset := maxInt(0, len(m.rows)-maxSummaryRows)
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	end := minInt(len(m.rows), offset+maxSummaryRows)
	visible := []summaryRow(nil)
	if offset < len(m.rows) {
		visible = m.rows[offset:end]
	}

	for i, row := range visible {
		absIdx := offset + i

		// Insert a faint divider between agent groups so that contiguous
		// rows for the same agent are visually grouped together.
		if absIdx > 0 {
			prev := m.rows[absIdx-1]
			if prev.AgentID != row.AgentID {
				divider := strings.Repeat("\u2500", totalWidth(widths))
				sb.WriteString(dimStyle.Render(divider) + "\n")
			}
		}

		baseStyle := lipgloss.NewStyle()
		isInactive := !row.IsActive
		if isInactive {
			// Inactive models are rendered in a darker gray so the active
			// model remains visually primary, mirroring the detailed view
			// where non-current rows are visually softer than the active one.
			baseStyle = dimStyle
			if absIdx == m.cursor {
				baseStyle = dimStyle.Copy().Background(lipgloss.Color("236"))
			}
		} else if absIdx == m.cursor {
			baseStyle = cursorStyle
		}

		// Render non-health columns.
		cells := formatSummaryRow(row)
		// Columns before Health (index 6): Agent, Model, Runs, Accuracy, P95, Total Cost (indices 0-5).
		const healthColIdx = 6
		const verdictColIdx2 = 7

		// Add visual marker (●) to the agent column for the active model,
		// mirroring the detailed view's isCurrent logic.
		var rendered string
		if row.IsActive {
			// Truncate plain agentCell to (width-2) visible chars, then prepend the marker.
			agentCell := cells[0]
			maxAgentLen := widths[0] - 2 // reserve 2 visible chars for "● "
			if len(agentCell) > maxAgentLen {
				agentCell = agentCell[:maxAgentLen-1] + "…"
			}
			greenMarker := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("●")
			agentPadded := fmt.Sprintf("%-*s", maxAgentLen, agentCell)
			agentColRendered := baseStyle.Render(greenMarker + " " + agentPadded)
			rendered = agentColRendered + " " + renderRow(cells[1:healthColIdx], widths[1:healthColIdx], baseStyle)
		} else {
			rendered = renderRow(cells[:healthColIdx], widths[:healthColIdx], baseStyle)
		}

		// Health column with colour.
		healthCell := healthStyle(row.HealthScore).Inherit(baseStyle).Render(
			fmt.Sprintf("%-*s", widths[healthColIdx], cells[healthColIdx]))
		rendered += healthCell
		// Last Verdict column: only the active model shows its last verdict.
		// Non-active models always display "-" (dim, no colour styling).
		verdictCell := ""
		if !row.IsActive {
			verdictCell = baseStyle.Render(
				fmt.Sprintf("%-*s", widths[verdictColIdx2], "-"))
		} else if row.LastVerdict == store.VerdictInsufficientData {
			verdictCell = baseStyle.Render(
				fmt.Sprintf("%-*s", widths[verdictColIdx2], cells[verdictColIdx2]))
		} else {
			// Remove cursor background from this specific column.
			verdictCell = verdictStyle(row.LastVerdict).Render(
				fmt.Sprintf("%-*s", widths[verdictColIdx2], cells[verdictColIdx2]))
		}
		rendered += " " + verdictCell

		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	// Footer.
	sb.WriteString("\n")
	pageNum := 1
	if len(m.rows) > 0 {
		pageNum = m.offset/maxSummaryRows + 1
	}
	totalPages := 1
	if len(m.rows) > 0 {
		totalPages = (len(m.rows) + maxSummaryRows - 1) / maxSummaryRows
	}
	footer := fmt.Sprintf("  %d agent/model pair(s)  |  page %d/%d  |  ↑↓ to navigate  |  2: switch to Detailed view",
		len(m.rows), pageNum, totalPages)
	sb.WriteString(dimStyle.Render(footer))
	sb.WriteString("\n")

	// Selected row detail.
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		r := m.rows[m.cursor]
		sb.WriteString("\n")
		divider := strings.Repeat("─", totalWidth(widths))
		sb.WriteString(dimStyle.Render(divider) + "\n")
		sb.WriteString(detailLabelStyle.Render("Agent History Summary") + "\n")
		sb.WriteString(dimStyle.Render("Weighted historical averages (weekly + intraweek) — showing models active in the last 4 weekly cycles") + "\n")
		sb.WriteString(dimStyle.Render(divider) + "\n")
		writeDetailField(&sb, "Agent", r.AgentID)
		writeDetailField(&sb, "Model", r.Model)
		writeDetailField(&sb, "Runs", fmt.Sprintf("%d benchmark run(s)", r.Runs))
		writeDetailField(&sb, "Accuracy", fmt.Sprintf("%.1f%%  (weighted avg)", r.AvgAccuracy*100))
		writeDetailField(&sb, "Avg Response", fmt.Sprintf("%s  (weighted avg)", formatDuration(r.AvgTurnMs)))
		writeDetailField(&sb, "Cost", fmt.Sprintf("$%.4f  (from last verdict run)", r.TotalCostUSD))
		writeDetailField(&sb, "Health", fmt.Sprintf("%.0f / 100", r.HealthScore))
		writeDetailField(&sb, "Verdict", string(r.LastVerdict))
		if !r.LastRunAt.IsZero() {
			writeDetailField(&sb, "Last Run", r.LastRunAt.Local().Format("2006-01-02 15:04"))
		}
	}

	out := sb.String()
	// Stabilize output height across cursor moves so the terminal does not show
	// remnants or cause implicit scrolling.
	lineCount := strings.Count(out, "\n")
	if lineCount < m.lastViewLines {
		out += strings.Repeat("\n", m.lastViewLines-lineCount)
	}
	m.lastViewLines = maxInt(m.lastViewLines, lineCount)
	return out
}

// formatSummaryRow converts a summaryRow into display columns.
func formatSummaryRow(r summaryRow) []string {
	runs := fmt.Sprintf("%d", r.Runs)
	accuracy := fmt.Sprintf("%.1f%%", r.AvgAccuracy*100)
	p95 := formatDuration(r.AvgTurnMs)
	cost := fmt.Sprintf("$%.4f", r.TotalCostUSD)
	health := fmt.Sprintf("%.0f", r.HealthScore)
	verdict := string(r.LastVerdict)
	if verdict == "" {
		verdict = "-"
	}
	model := store.NormalizeModelName(r.Model)
	if model == "" {
		model = "-"
	}
	return []string{r.AgentID, model, runs, accuracy, p95, cost, health, verdict}
}
