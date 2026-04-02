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

// benchmarkSummaryRefreshInterval matches the benchmark detailed tab refresh cadence.
const benchmarkSummaryRefreshInterval = 2 * time.Second

// benchmarkSummaryTickMsg is sent by the auto-refresh ticker.
type benchmarkSummaryTickMsg struct{ t time.Time }

// BenchmarkSummaryDataMsg carries aggregated per-agent/model summary rows.
type BenchmarkSummaryDataMsg struct {
	Rows []summaryRow
	Err  error
}

// summaryRow holds the aggregated metrics for a single (agent, model) pair.
type summaryRow struct {
	AgentID      string
	Model        string
	Runs         int     // total benchmark runs
	AvgAccuracy  float64 // weighted average accuracy
	AvgTurnMs    float64 // weighted average turn duration (complete events only)
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

// BenchmarkSummaryModel is the Bubble Tea sub-model for the benchmark summary tab.
type BenchmarkSummaryModel struct {
	bs            store.BenchmarkStore
	rows          []summaryRow
	err           error
	cursor        int
	offset        int
	loading       bool
	lastViewLines int
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

// NewBenchmarkSummaryModel creates a BenchmarkSummaryModel wired to the given BenchmarkStore.
func NewBenchmarkSummaryModel(bs store.BenchmarkStore) BenchmarkSummaryModel {
	return BenchmarkSummaryModel{
		bs:      bs,
		loading: true,
		offset:  0,
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
	case benchmarkSummaryTickMsg:
		return m, tea.Batch(
			tea.Tick(benchmarkSummaryRefreshInterval, func(t time.Time) tea.Msg {
				return benchmarkSummaryTickMsg{t: t}
			}),
			m.fetchSummary(),
		)

	case BenchmarkSummaryDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.rows = msg.Rows
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
		}
	}
	return m, nil
}

// fetchSummary returns a command that queries the BenchmarkStore and builds summary rows
// aggregated per (agent, model) pair across all stored runs.
// It fetches up to 200 recent runs per agent (covering many weeks) and computes
// weighted averages by SampleSize.
func (m BenchmarkSummaryModel) fetchSummary() tea.Cmd {
	if m.bs == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		agentIDs, err := m.bs.ListAgents(ctx)
		if err != nil {
			return BenchmarkSummaryDataMsg{Err: err}
		}

		// Aggregate per (agent, model) pair.
		type key struct{ agent, model string }
		type agg struct {
			runs         int
			totalSamples int
			sumAccuracy  float64
			sumP95       float64
			sumROI       float64
			roiSamples   int
			lastCostUSD  float64
			lastVerdict  store.VerdictType
			lastRunAt    time.Time
			// Fallback metrics from the most recent run (used when all runs are
			// INSUFFICIENT_DATA so we don't display misleading 0% accuracy).
			lastAccuracy float64
			lastP95      float64
			lastROI      float64
		}
		aggMap := make(map[key]*agg)

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
					a = &agg{}
					aggMap[k] = a
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

				// Always track the most recent run's raw metrics as fallback.
				if r.RunAt.After(a.lastRunAt) || a.lastRunAt.IsZero() {
					if !isInsufficient {
						a.lastRunAt = r.RunAt
						a.lastVerdict = r.Verdict
						a.lastCostUSD = r.TotalCostUSD
					} else if a.lastVerdict == "" || a.lastVerdict == store.VerdictInsufficientData {
						a.lastRunAt = r.RunAt
						a.lastVerdict = r.Verdict
						a.lastCostUSD = r.TotalCostUSD
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
			}
		}

		const defaultMinROI = 0.05 // matches config.DefaultThresholdValues

		// Build sorted rows.
		var rows []summaryRow
		for k, a := range aggMap {
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
			health := computeHealthScore(avgAcc, avgP95, a.lastVerdict, avgROI, defaultMinROI)
			rows = append(rows, summaryRow{
				AgentID:      k.agent,
				Model:        k.model,
				Runs:         a.runs,
				AvgAccuracy:  avgAcc,
				AvgTurnMs:    avgP95,
				TotalCostUSD: a.lastCostUSD,
				HealthScore:  health,
				LastVerdict:  a.lastVerdict,
				LastRunAt:    a.lastRunAt,
			})
		}

		// Sort: healthiest first (desc), then alphabetical by agent.
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].HealthScore != rows[j].HealthScore {
				return rows[i].HealthScore > rows[j].HealthScore
			}
			if rows[i].AgentID != rows[j].AgentID {
				return rows[i].AgentID < rows[j].AgentID
			}
			return rows[i].Model < rows[j].Model
		})

		return BenchmarkSummaryDataMsg{Rows: rows}
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

	sb.WriteString(titleStyle.Render("Benchmark Summary") + "\n\n")

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

	// Header.
	sb.WriteString(renderRow(summaryColNames, summaryColWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(summaryColWidths)) + "\n")

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
		baseStyle := lipgloss.NewStyle()
		if absIdx == m.cursor {
			baseStyle = cursorStyle
		}

		// Render non-health columns.
		cells := formatSummaryRow(row)
		// Columns before Health (index 6): Agent, Model, Runs, Accuracy, P95, Total Cost (indices 0-5).
		const healthColIdx = 6
		const verdictColIdx2 = 7

		rendered := renderRow(cells[:healthColIdx], summaryColWidths[:healthColIdx], baseStyle)
		// Health column with colour.
		healthCell := healthStyle(row.HealthScore).Inherit(baseStyle).Render(
			fmt.Sprintf("%-*s", summaryColWidths[healthColIdx], cells[healthColIdx]))
		rendered += healthCell
		// Last Verdict column: for INSUFFICIENT_DATA rows we show grey text,
		// and still allow the cursor background highlight.
		verdictCell := ""
		if row.LastVerdict == store.VerdictInsufficientData {
			verdictCell = baseStyle.Render(
				fmt.Sprintf("%-*s", summaryColWidths[verdictColIdx2], cells[verdictColIdx2]))
		} else {
			// Remove cursor background from this specific column.
			verdictCell = verdictStyle(row.LastVerdict).Render(
				fmt.Sprintf("%-*s", summaryColWidths[verdictColIdx2], cells[verdictColIdx2]))
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
	footer := fmt.Sprintf("  %d agent/model pair(s)  |  page %d/%d  |  ↑↓ to navigate  |  3: switch to Detailed view",
		len(m.rows), pageNum, totalPages)
	sb.WriteString(dimStyle.Render(footer))
	sb.WriteString("\n")

	// Selected row detail.
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		r := m.rows[m.cursor]
		sb.WriteString("\n")
		divider := strings.Repeat("─", totalWidth(summaryColWidths))
		sb.WriteString(dimStyle.Render(divider) + "\n")
		sb.WriteString(detailLabelStyle.Render("Agent Summary") + "\n")
		sb.WriteString(dimStyle.Render(divider) + "\n")
		writeDetailField(&sb, "Agent", r.AgentID)
		writeDetailField(&sb, "Model", r.Model)
		writeDetailField(&sb, "Runs", fmt.Sprintf("%d benchmark run(s)", r.Runs))
		writeDetailField(&sb, "Accuracy", fmt.Sprintf("%.1f%%  (weighted avg)", r.AvgAccuracy*100))
		writeDetailField(&sb, "Avg Response", fmt.Sprintf("%.1fs  (weighted avg)", r.AvgTurnMs/1000))
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
	p95 := fmt.Sprintf("%.1fs", r.AvgTurnMs/1000)
	cost := fmt.Sprintf("$%.4f", r.TotalCostUSD)
	health := fmt.Sprintf("%.0f", r.HealthScore)
	verdict := string(r.LastVerdict)
	if verdict == "" {
		verdict = "-"
	}
	model := r.Model
	if model == "" {
		model = "-"
	}
	return []string{r.AgentID, model, runs, accuracy, p95, cost, health, verdict}
}
