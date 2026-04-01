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
	AvgP95Ms     float64 // weighted average P95 latency
	TotalCostUSD float64 // sum of all run costs
	HealthScore  float64 // composite 0-100 (higher is better)
	LastVerdict  store.VerdictType
	LastRunAt    time.Time
}

// summaryColWidths and summaryColNames describe the summary table columns.
// Columns: Agent | Model | Runs | Accuracy | P95 | Total Cost | Health | Last Verdict
var (
	summaryColWidths = []int{18, 22, 5, 10, 12, 12, 8, 20}
	summaryColNames  = []string{"Agent", "Model", "Runs", "Accuracy", "P95 Latency", "Total Cost", "Health", "Last Verdict"}
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
	bs      store.BenchmarkStore
	rows    []summaryRow
	err     error
	cursor  int
	loading bool
}

// NewBenchmarkSummaryModel creates a BenchmarkSummaryModel wired to the given BenchmarkStore.
func NewBenchmarkSummaryModel(bs store.BenchmarkStore) BenchmarkSummaryModel {
	return BenchmarkSummaryModel{
		bs:      bs,
		loading: true,
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
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
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
			totalCost    float64
			lastVerdict  store.VerdictType
			lastRunAt    time.Time
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
				k := key{agentID, r.Model}
				a := aggMap[k]
				if a == nil {
					a = &agg{}
					aggMap[k] = a
				}
				samples := r.SampleSize
				if samples <= 0 {
					samples = 1
				}
				a.runs++
				a.totalSamples += samples
				a.sumAccuracy += r.Accuracy * float64(samples)
				a.sumP95 += r.P95LatencyMs * float64(samples)
				a.totalCost += r.TotalCostUSD
				// Track the latest run's verdict.
				if r.RunAt.After(a.lastRunAt) {
					a.lastRunAt = r.RunAt
					a.lastVerdict = r.Verdict
				}
			}
		}

		// Build sorted rows.
		var rows []summaryRow
		for k, a := range aggMap {
			avgAcc := 0.0
			avgP95 := 0.0
			if a.totalSamples > 0 {
				avgAcc = a.sumAccuracy / float64(a.totalSamples)
				avgP95 = a.sumP95 / float64(a.totalSamples)
			}
			health := computeHealthScore(avgAcc, avgP95, a.lastVerdict)
			rows = append(rows, summaryRow{
				AgentID:      k.agent,
				Model:        k.model,
				Runs:         a.runs,
				AvgAccuracy:  avgAcc,
				AvgP95Ms:     avgP95,
				TotalCostUSD: a.totalCost,
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
// Formula (Option B):
//   - Accuracy contributes 50 points (accuracy * 50).
//   - P95 latency contributes 30 points (30 * max(0, 1 - p95/10000)).
//   - Verdict contributes 20 points: KEEP=20, SWITCH=10, URGENT_SWITCH=0, other=5.
func computeHealthScore(accuracy, p95Ms float64, verdict store.VerdictType) float64 {
	accPart := accuracy * 50
	latPart := 30 * max64(0, 1-p95Ms/10000)
	var verdictPart float64
	switch verdict {
	case store.VerdictKeep:
		verdictPart = 20
	case store.VerdictSwitch:
		verdictPart = 10
	case store.VerdictUrgentSwitch:
		verdictPart = 0
	default:
		verdictPart = 5
	}
	score := accPart + latPart + verdictPart
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// max64 returns the maximum of two float64 values.
func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// View renders the benchmark summary tab.
func (m BenchmarkSummaryModel) View() string {
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
	for i, row := range m.rows {
		baseStyle := lipgloss.NewStyle()
		if i == m.cursor {
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
		// Last Verdict column with verdict colour.
		verdictCell := verdictStyle(row.LastVerdict).Inherit(baseStyle).Render(
			fmt.Sprintf("%-*s", summaryColWidths[verdictColIdx2], cells[verdictColIdx2]))
		rendered += " " + verdictCell

		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	// Footer.
	sb.WriteString("\n")
	footer := fmt.Sprintf("  %d agent/model pair(s)  |  ↑↓ to navigate  |  3: switch to Detailed view",
		len(m.rows))
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
		writeDetailField(&sb, "P95", fmt.Sprintf("%.0fms  (weighted avg)", r.AvgP95Ms))
		writeDetailField(&sb, "Cost", fmt.Sprintf("$%.4f  (total across all runs)", r.TotalCostUSD))
		writeDetailField(&sb, "Health", fmt.Sprintf("%.0f / 100", r.HealthScore))
		writeDetailField(&sb, "Verdict", string(r.LastVerdict))
		if !r.LastRunAt.IsZero() {
			writeDetailField(&sb, "Last Run", r.LastRunAt.Local().Format("2006-01-02 15:04"))
		}
	}

	return sb.String()
}

// formatSummaryRow converts a summaryRow into display columns.
func formatSummaryRow(r summaryRow) []string {
	runs := fmt.Sprintf("%d", r.Runs)
	accuracy := fmt.Sprintf("%.1f%%", r.AvgAccuracy*100)
	p95 := fmt.Sprintf("%.0fms", r.AvgP95Ms)
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
