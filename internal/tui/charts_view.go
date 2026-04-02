package tui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/store"
)

const chartsRefreshInterval = 2 * time.Second

type chartsTickMsg struct{ t time.Time }

type ChartsDataMsg struct {
	MonthStart                   time.Time
	Rows                         []store.DailyCostByModelRow
	ModelStats                   map[string]*chartModelStats
	SelectedModels               []string
	CostSelectedModels           []string
	PerformanceSelectedModels    []string
	ResponsibilitySelectedModels []string
	Err                          error
}

// ChartsModel renders daily cost charts for the selected month.
type ChartsModel struct {
	es store.EventStore
	bs store.BenchmarkStore

	width  int
	height int

	monthStart time.Time // local midnight of the 1st day of selected month

	loading bool
	err     error

	dailyRows []store.DailyCostByModelRow

	selectedModels []string

	performanceSelectedModels    []string
	responsibilitySelectedModels []string
	modelStats                   map[string]*chartModelStats

	cursorDayIndex int // 0-based within the selected month
}

func daysInMonth(monthStart time.Time) int {
	start := time.Date(monthStart.Year(), monthStart.Month(), 1, 0, 0, 0, 0, monthStart.Location())
	count := 0
	for d := start; d.Month() == start.Month(); d = d.AddDate(0, 0, 1) {
		count++
	}
	return count
}

func sameChartMonth(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month()
}

func rankModelsByCost(rows []store.DailyCostByModelRow, limit int) []string {
	totals := make(map[string]float64)
	for _, r := range rows {
		totals[r.Model] += r.TotalCostUSD
	}

	type item struct {
		model string
		cost  float64
	}
	items := make([]item, 0, len(totals))
	for model, cost := range totals {
		items = append(items, item{model: model, cost: cost})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].cost != items[j].cost {
			return items[i].cost > items[j].cost
		}
		return items[i].model < items[j].model
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[i].model)
	}
	return out
}

func rankModelsByPerformance(summaries []store.BenchmarkModelSummary, active map[string]struct{}, limit int) []string {
	type item struct {
		model string
		score float64
		cost  float64
	}
	items := make([]item, 0, len(active))
	for _, s := range summaries {
		if _, ok := active[s.Model]; !ok {
			continue
		}
		items = append(items, item{
			model: s.Model,
			score: computeHealthScore(s.AvgAccuracy, s.AvgP95Ms, s.LastVerdict),
			cost:  s.TotalCostUSD,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].cost != items[j].cost {
			return items[i].cost > items[j].cost
		}
		return items[i].model < items[j].model
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[i].model)
	}
	return out
}

func filterCostRows(rows []store.DailyCostByModelRow, selected []string) []store.DailyCostByModelRow {
	if len(selected) == 0 {
		return nil
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, model := range selected {
		selectedSet[model] = struct{}{}
	}
	out := make([]store.DailyCostByModelRow, 0, len(rows))
	for _, r := range rows {
		if _, ok := selectedSet[r.Model]; ok {
			out = append(out, r)
		}
	}
	return out
}

type chartPanelData struct {
	selectedModels []string
	colors         map[string]lipgloss.Color
	blocks         map[string]string
	costs          map[string]map[string]float64 // dayKey -> model -> cost
	totals         map[string]float64
}

type chartSummaryEntry struct {
	Model               string
	Color               lipgloss.Color
	HealthScore         float64
	ResponsibilityScore float64
	TotalCostUSD        float64
}

type chartModelStats struct {
	Model               string
	Runs                int
	TotalSamples        int
	SumAccuracy         float64
	SumP95              float64
	LastVerdict         store.VerdictType
	LastRunAt           time.Time
	HealthScore         float64
	ResponsibilityScore float64
	TotalCostUSD        float64
	roleWeightSum       float64
	roleWeightedScore   float64
}

func buildChartPanelData(rows []store.DailyCostByModelRow, selected []string) chartPanelData {
	data := chartPanelData{
		selectedModels: selected,
		colors:         make(map[string]lipgloss.Color, len(selected)),
		blocks:         make(map[string]string, len(selected)),
		costs:          make(map[string]map[string]float64),
		totals:         make(map[string]float64, len(selected)),
	}
	if len(selected) == 0 {
		return data
	}

	palette := []lipgloss.Color{
		lipgloss.Color("82"),
		lipgloss.Color("226"),
		lipgloss.Color("196"),
		lipgloss.Color("39"),
		lipgloss.Color("213"),
		lipgloss.Color("51"),
		lipgloss.Color("99"),
		lipgloss.Color("208"),
		lipgloss.Color("141"),
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, model := range selected {
		selectedSet[model] = struct{}{}
	}

	for _, model := range selected {
		c := palette[len(data.colors)%len(palette)]
		data.colors[model] = c
		data.blocks[model] = lipgloss.NewStyle().Foreground(c).Render("█")
	}

	for _, r := range rows {
		if _, ok := selectedSet[r.Model]; !ok {
			continue
		}
		dayKey := r.Day.Format("2006-01-02")
		bucket := data.costs[dayKey]
		if bucket == nil {
			bucket = make(map[string]float64)
			data.costs[dayKey] = bucket
		}
		bucket[r.Model] += r.TotalCostUSD
		data.totals[r.Model] += r.TotalCostUSD
	}

	return data
}

func buildChartColorMap(models []string) map[string]lipgloss.Color {
	palette := []lipgloss.Color{
		lipgloss.Color("82"),
		lipgloss.Color("226"),
		lipgloss.Color("196"),
		lipgloss.Color("39"),
		lipgloss.Color("213"),
		lipgloss.Color("51"),
		lipgloss.Color("99"),
		lipgloss.Color("208"),
		lipgloss.Color("141"),
	}
	colors := make(map[string]lipgloss.Color, len(models))
	for i, model := range models {
		if _, ok := colors[model]; ok {
			continue
		}
		colors[model] = palette[i%len(palette)]
	}
	return colors
}

func responsibilityWeightForAgent(agentID string) float64 {
	switch agentID {
	case "sdd-orchestrator":
		return 1.00
	case "sdd-apply":
		return 0.98
	case "sdd-verify":
		return 0.96
	case "sdd-explore":
		return 0.94
	case "sdd-design":
		return 0.92
	case "sdd-spec":
		return 0.90
	case "sdd-propose":
		return 0.88
	case "sdd-tasks":
		return 0.87
	case "sdd-init":
		return 0.85
	case "sdd-archive":
		return 0.86
	default:
		if strings.HasPrefix(agentID, "sdd-") {
			return 0.90
		}
		if agentID == "build" || agentID == "plan" || agentID == "general" || agentID == "explore" {
			return 0.80
		}
		return 0.75
	}
}

func aggregateChartsModelStats(runs []store.BenchmarkRun) map[string]*chartModelStats {
	stats := make(map[string]*chartModelStats)
	for _, run := range runs {
		if run.Model == "" || run.RunAt.IsZero() {
			continue
		}
		s := stats[run.Model]
		if s == nil {
			s = &chartModelStats{Model: run.Model}
			stats[run.Model] = s
		}

		s.Runs++
		isInsufficient := run.Verdict == store.VerdictInsufficientData || run.SampleSize < 50
		if !isInsufficient {
			samples := run.SampleSize
			if samples <= 0 {
				samples = 1
			}
			s.TotalSamples += samples
			s.SumAccuracy += run.Accuracy * float64(samples)
			s.SumP95 += run.P95LatencyMs * float64(samples)
		}

		if run.RunAt.After(s.LastRunAt) {
			if !isInsufficient {
				s.LastRunAt = run.RunAt
				s.LastVerdict = run.Verdict
			} else if s.LastVerdict == "" || s.LastVerdict == store.VerdictInsufficientData {
				s.LastRunAt = run.RunAt
				s.LastVerdict = run.Verdict
			}
		}

		weight := responsibilityWeightForAgent(run.AgentID)
		if run.SampleSize <= 0 {
			run.SampleSize = 1
		}
		s.roleWeightSum += float64(run.SampleSize)
		weightedScore := computeHealthScore(run.Accuracy, run.P95LatencyMs, run.Verdict) * weight
		s.roleWeightedScore += weightedScore * float64(run.SampleSize)
	}

	for _, s := range stats {
		if s.TotalSamples > 0 {
			avgAcc := s.SumAccuracy / float64(s.TotalSamples)
			avgP95 := s.SumP95 / float64(s.TotalSamples)
			s.HealthScore = computeHealthScore(avgAcc, avgP95, s.LastVerdict)
		} else {
			s.HealthScore = computeHealthScore(0, 0, s.LastVerdict)
		}
		if s.roleWeightSum > 0 {
			s.ResponsibilityScore = s.roleWeightedScore / s.roleWeightSum
		} else {
			s.ResponsibilityScore = s.HealthScore * 0.75
		}
	}

	return stats
}

func rankChartsByScore(stats map[string]*chartModelStats, scoreFn func(*chartModelStats) float64, limit int) []string {
	type item struct {
		model string
		score float64
		cost  float64
	}
	items := make([]item, 0, len(stats))
	for _, s := range stats {
		items = append(items, item{model: s.Model, score: scoreFn(s), cost: s.TotalCostUSD})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].cost != items[j].cost {
			return items[i].cost > items[j].cost
		}
		return items[i].model < items[j].model
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[i].model)
	}
	return out
}

func buildChartSummaryEntries(modelOrder []string, stats map[string]*chartModelStats, totals map[string]float64) []chartSummaryEntry {
	colors := buildChartColorMap(modelOrder)
	entries := make([]chartSummaryEntry, 0, len(modelOrder))
	for _, model := range modelOrder {
		s := stats[model]
		if s == nil {
			s = &chartModelStats{Model: model}
		}
		entries = append(entries, chartSummaryEntry{
			Model:               model,
			Color:               colors[model],
			HealthScore:         s.HealthScore,
			ResponsibilityScore: s.ResponsibilityScore,
			TotalCostUSD:        totals[model],
		})
	}
	return entries
}

func chartScoreBar(value, maxValue float64, width int) string {
	if width <= 0 {
		return ""
	}
	if maxValue <= 0 {
		return strings.Repeat("·", width)
	}
	filled := int(math.Round((value / maxValue) * float64(width)))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("·", width-filled)
}

func truncateSummaryText(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

func truncateProviderOnly(modelID string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	idx := strings.LastIndex(modelID, "/")
	if idx < 0 {
		// No provider separator — never truncate the model part.
		return modelID
	}
	provider := modelID[:idx]
	model := modelID[idx+1:]
	modelW := lipgloss.Width(model)
	if modelW >= maxWidth {
		// Keep full model.
		return model
	}
	providerAvailable := maxWidth - modelW - 1 // minus '/'
	if providerAvailable <= 0 {
		return model
	}
	providerW := lipgloss.Width(provider)
	if providerW <= providerAvailable {
		return provider + "/" + model
	}
	// Truncate provider only.
	if providerAvailable == 1 {
		return "…/" + model
	}
	// Provider truncation by runes with ellipsis.
	runes := []rune(provider)
	// This is an approximation since rune width may differ; lipgloss.Width is
	// still used for the maxWidth checks.
	cut := providerAvailable - 1
	if cut < 1 {
		cut = 1
	}
	if len(runes) < cut {
		cut = len(runes)
	}
	truncProvider := string(runes[:cut])
	return truncProvider + "…/" + model
}

func renderSummaryCard(title string, entries []chartSummaryEntry, width int, showResponsibility bool) string {
	if width <= 0 {
		width = 40
	}
	innerWidth := width - 4
	if innerWidth < 24 {
		innerWidth = 24
	}
	borderStyle := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)

	headline := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render(title)
	lines := []string{headline}
	if len(entries) == 0 {
		lines = append(lines, "No benchmark data for the selected month.")
		return borderStyle.Render(strings.Join(lines, "\n"))
	}

	maxCost := 0.0
	for _, entry := range entries {
		if entry.TotalCostUSD > maxCost {
			maxCost = entry.TotalCostUSD
		}
	}

	for i, entry := range entries {
		rank := fmt.Sprintf("%d.", i+1)
		bullet := lipgloss.NewStyle().Foreground(entry.Color).Render("█")
		modelWidth := innerWidth - 34
		if showResponsibility {
			modelWidth = innerWidth - 38
		}
		if modelWidth < 10 {
			modelWidth = 10
		}
		model := truncateProviderOnly(entry.Model, modelWidth)
		cost := fmt.Sprintf("$%.2f", entry.TotalCostUSD)
		barWidth := 8
		if innerWidth > 56 {
			barWidth = 10
		}
		bar := lipgloss.NewStyle().Foreground(entry.Color).Render(chartScoreBar(entry.TotalCostUSD, maxCost, barWidth))

		var score string
		if showResponsibility {
			score = fmt.Sprintf("R %.0f | H %.0f", entry.ResponsibilityScore, entry.HealthScore)
		} else {
			score = fmt.Sprintf("H %.0f", entry.HealthScore)
		}

		line := fmt.Sprintf("%s %s %s  %s  %s %s", rank, bullet, model, score, cost, bar)
		lines = append(lines, line)
	}

	return borderStyle.Render(strings.Join(lines, "\n"))
}

func renderSummaryCards(width int, performance, responsibility []chartSummaryEntry) []string {
	if width <= 0 {
		width = 80
	}
	const gap = 4
	minCardWidth := 34
	if width < minCardWidth*2+gap {
		left := renderSummaryCard("Performance Top 3 of the Month", performance, width, false)
		right := renderSummaryCard("Responsibility Top 3 of the Month", responsibility, width, true)
		return []string{left, "", right}
	}
	cardWidth := (width - gap) / 2
	if cardWidth < minCardWidth {
		left := renderSummaryCard("Performance Top 3 of the Month", performance, width, false)
		right := renderSummaryCard("Responsibility Top 3 of the Month", responsibility, width, true)
		return []string{left, "", right}
	}
	left := renderSummaryCard("Performance Top 3 of the Month", performance, cardWidth, false)
	right := renderSummaryCard("Responsibility Top 3 of the Month", responsibility, cardWidth, true)
	return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right), "\n")
}

func wrapLegendLines(selected []string, totals map[string]float64, colors map[string]lipgloss.Color, width int) []string {
	if len(selected) == 0 {
		return nil
	}
	legendLines := []string{}
	line := "Legend: "
	for i, model := range selected {
		entry := fmt.Sprintf("%s %s ($%.2f)", lipgloss.NewStyle().Foreground(colors[model]).Render("█"), model, totals[model])
		sep := "  "
		if i == 0 {
			sep = ""
		}
		candidate := line + sep + entry
		if width > 0 && lipgloss.Width(candidate) > maxInt(40, width-2) && line != "Legend: " {
			legendLines = append(legendLines, line)
			line = strings.Repeat(" ", len("Legend: ")) + entry
		} else {
			line = candidate
		}
	}
	legendLines = append(legendLines, line)
	return legendLines
}

func panelBarHeight(totalHeight int) int {
	barHeight := 8
	if totalHeight > 0 {
		barHeight = (totalHeight - 18) / 3
		if barHeight < 5 {
			barHeight = 5
		}
		if barHeight > 12 {
			barHeight = 12
		}
	}
	return barHeight
}

func renderChartPanel(title string, days []time.Time, cursorDayIndex, width, height int, data chartPanelData) []string {
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render(title)}
	if len(days) == 0 || len(data.selectedModels) == 0 {
		lines = append(lines, "No chart data for the selected month.")
		return lines
	}

	cellWidth := 4
	leftGutter := 10
	maxCols := width - leftGutter - 1
	if maxCols <= 0 {
		maxCols = 1
	}
	chunkSize := maxCols / cellWidth
	if chunkSize <= 0 {
		chunkSize = 5
	}
	if chunkSize < 5 {
		chunkSize = 5
	}
	if chunkSize > len(days) {
		chunkSize = len(days)
	}

	barHeight := panelBarHeight(height)

	dayTotals := make([]float64, 0, len(days))
	for _, d := range days {
		rowCosts := data.costs[d.Format("2006-01-02")]
		total := 0.0
		for _, model := range data.selectedModels {
			if rowCosts != nil {
				total += rowCosts[model]
			}
		}
		dayTotals = append(dayTotals, total)
	}

	chartLinesStart := len(lines)
	for chunkStart := 0; chunkStart < len(days); chunkStart += chunkSize {
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > len(days) {
			chunkEnd = len(days)
		}
		chunkDays := days[chunkStart:chunkEnd]
		chunkTotals := dayTotals[chunkStart:chunkEnd]

		maxTotal := 0.0
		minPositive := 0.0
		for _, v := range chunkTotals {
			if v > maxTotal {
				maxTotal = v
			}
			if v > 0 && (minPositive == 0 || v < minPositive) {
				minPositive = v
			}
		}
		if maxTotal <= 0 {
			maxTotal = 1
		}
		if minPositive <= 0 {
			minPositive = maxTotal
		}

		logMin := math.Log10(minPositive)
		logMax := math.Log10(maxTotal)
		uniformPositive := logMax-logMin < 1e-9
		if uniformPositive {
			logMax = logMin + 1
		}

		costToBlocks := func(cost float64) int {
			if cost <= 0 || len(data.selectedModels) == 0 {
				return 0
			}
			if uniformPositive {
				return barHeight
			}
			lg := math.Log10(cost)
			r := (lg - logMin) / (logMax - logMin)
			if r < 0 {
				r = 0
			}
			if r > 1 {
				r = 1
			}
			return int(math.Round(r * float64(barHeight)))
		}

		formatY := func(v float64) string {
			if v >= 100 {
				return fmt.Sprintf("$%.0f", v)
			}
			if v >= 10 {
				return fmt.Sprintf("$%.1f", v)
			}
			return fmt.Sprintf("$%.2f", v)
		}

		tickCount := 5
		yTicks := make([]float64, 0, tickCount)
		for i := 0; i < tickCount; i++ {
			r := float64(i) / float64(tickCount-1)
			val := math.Pow(10, logMax-(r*(logMax-logMin)))
			yTicks = append(yTicks, val)
		}

		tickRowLabels := make(map[int]string)
		for _, v := range yTicks {
			b := costToBlocks(v)
			if b < 1 {
				continue
			}
			if b > barHeight {
				b = barHeight
			}
			tickRowLabels[b] = formatY(v)
		}

		heightsPerDay := make([][]int, len(chunkDays))
		for i, d := range chunkDays {
			rowCosts := data.costs[d.Format("2006-01-02")]
			totalCost := chunkTotals[i]
			totalBlocks := costToBlocks(totalCost)
			if totalBlocks <= 0 || rowCosts == nil {
				heightsPerDay[i] = make([]int, len(data.selectedModels))
				continue
			}

			floors := make([]int, len(data.selectedModels))
			fracs := make([]float64, len(data.selectedModels))
			sumFloors := 0
			for j, model := range data.selectedModels {
				c := rowCosts[model]
				segExact := (float64(totalBlocks) * c) / totalCost
				f := int(segExact)
				floors[j] = f
				fracs[j] = segExact - float64(f)
				sumFloors += f
			}

			rem := totalBlocks - sumFloors
			heights := make([]int, len(data.selectedModels))
			copy(heights, floors)
			if rem > 0 {
				idx := make([]int, len(data.selectedModels))
				for k := range idx {
					idx[k] = k
				}
				sort.SliceStable(idx, func(a, b int) bool {
					return fracs[idx[a]] > fracs[idx[b]]
				})
				for k := 0; k < rem && k < len(idx); k++ {
					heights[idx[k]]++
				}
			}
			heightsPerDay[i] = heights
		}

		for y := barHeight; y >= 1; y-- {
			label := tickRowLabels[y]
			pad := leftGutter - len(label)
			if pad < 1 {
				pad = 1
			}
			row := strings.Repeat(" ", pad) + label + "|"

			for i := range chunkDays {
				seg := -1
				cum := 0
				heights := heightsPerDay[i]
				for j := range data.selectedModels {
					cum += heights[j]
					if y <= cum {
						seg = j
						break
					}
				}
				if seg < 0 {
					row += strings.Repeat(" ", cellWidth)
					continue
				}
				model := data.selectedModels[seg]
				cell := data.blocks[model]
				globalIdx := chunkStart + i
				if globalIdx == cursorDayIndex {
					cell = lipgloss.NewStyle().Foreground(data.colors[model]).Background(lipgloss.Color("240")).Render("█")
				}
				row += " " + cell + " " + " "
			}
			lines = append(lines, row)
		}

		base := strings.Repeat(" ", leftGutter) + "+"
		for range chunkDays {
			base += strings.Repeat("-", cellWidth)
		}
		lines = append(lines, base)

		labelRow := strings.Repeat(" ", leftGutter) + " "
		for _, d := range chunkDays {
			lab := fmt.Sprintf("%2d", d.Day())
			if cellWidth > 2 {
				lab = " " + lab + strings.Repeat(" ", cellWidth-3)
			}
			if len(lab) < cellWidth {
				lab += strings.Repeat(" ", cellWidth-len(lab))
			}
			if len(lab) > cellWidth {
				lab = lab[:cellWidth]
			}
			labelRow += lab
		}
		lines = append(lines, labelRow)

		if chunkEnd < len(days) {
			lines = append(lines, "")
		}
	}

	if len(data.selectedModels) > 0 {
		legendLines := wrapLegendLines(data.selectedModels, data.totals, data.colors, width)
		if len(lines) > chartLinesStart {
			lines = append(lines, "")
		}
		lines = append(lines, legendLines...)
	}

	return lines
}

func renderChartTooltip(days []time.Time, cursorDayIndex int, data chartPanelData) []string {
	if len(days) == 0 || len(data.selectedModels) == 0 {
		return nil
	}
	idx := cursorDayIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(days) {
		idx = len(days) - 1
	}
	cursorDay := days[idx]
	rowCosts := data.costs[cursorDay.Format("2006-01-02")]
	total := 0.0
	for _, model := range data.selectedModels {
		if rowCosts != nil {
			total += rowCosts[model]
		}
	}
	tooltipLines := []string{fmt.Sprintf("Tooltip: %s ($%.2f)", cursorDay.Format("Jan 02"), total)}
	if total <= 0 || rowCosts == nil {
		tooltipLines = append(tooltipLines, "(no spend on selected models)")
		return tooltipLines
	}
	type ent struct {
		model string
		cost  float64
	}
	ents := make([]ent, 0, len(data.selectedModels))
	for _, model := range data.selectedModels {
		ents = append(ents, ent{model: model, cost: rowCosts[model]})
	}
	sort.SliceStable(ents, func(i, j int) bool { return ents[i].cost > ents[j].cost })
	for _, ent := range ents {
		if ent.cost <= 0 {
			continue
		}
		tooltipLines = append(tooltipLines, fmt.Sprintf("- %s %s: $%.3f", data.blocks[ent.model], ent.model, ent.cost))
	}
	return tooltipLines
}

func (m *ChartsModel) handleMouse(msg tea.MouseMsg) {
	// Only attempt tooltip selection when the chart fits in a single chunk.
	if m.width <= 0 {
		return
	}
	if msg.Type != tea.MouseMotion && msg.Type != tea.MouseLeft {
		return
	}

	const cellWidth = 4
	const leftGutter = 10

	monthDays := daysInMonth(m.monthStart)
	maxCols := m.width - leftGutter - 1
	if maxCols <= 0 {
		return
	}
	chunkSize := maxCols / cellWidth
	if chunkSize < 5 {
		chunkSize = 5
	}
	if chunkSize < monthDays {
		return
	}

	// Approximate chart origin.
	chartStartX := leftGutter + 1
	idx := (msg.X - chartStartX) / cellWidth
	if idx < 0 || idx >= monthDays {
		return
	}
	m.cursorDayIndex = idx
}

// NewChartsModel creates a ChartsModel wired to the given EventStore.
func NewChartsModel(es store.EventStore, bs store.BenchmarkStore) ChartsModel {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	return ChartsModel{
		es:             es,
		bs:             bs,
		monthStart:     monthStart,
		loading:        true,
		cursorDayIndex: 0,
	}
}

func (m ChartsModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchChartData(),
		tea.Tick(chartsRefreshInterval, func(t time.Time) tea.Msg { return chartsTickMsg{t: t} }),
	)
}

func (m ChartsModel) Update(msg tea.Msg) (ChartsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.MouseMsg:
		// Tooltip driven by mouse hover/click.
		m.handleMouse(msg)
		return m, nil

	case chartsTickMsg:
		return m, tea.Batch(
			tea.Tick(chartsRefreshInterval, func(t time.Time) tea.Msg { return chartsTickMsg{t: t} }),
			m.fetchChartData(),
		)

	case ChartsDataMsg:
		if !sameChartMonth(msg.MonthStart, m.monthStart) {
			return m, nil
		}
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.dailyRows = msg.Rows
			m.modelStats = msg.ModelStats
			if len(msg.CostSelectedModels) > 0 {
				m.selectedModels = msg.CostSelectedModels
			} else {
				m.selectedModels = msg.SelectedModels
			}
			if len(msg.PerformanceSelectedModels) > 0 {
				m.performanceSelectedModels = msg.PerformanceSelectedModels
			} else {
				m.performanceSelectedModels = m.selectedModels
			}
			if len(msg.ResponsibilitySelectedModels) > 0 {
				m.responsibilitySelectedModels = msg.ResponsibilitySelectedModels
			} else {
				m.responsibilitySelectedModels = m.selectedModels
			}
			if len(m.performanceSelectedModels) == 0 {
				m.performanceSelectedModels = m.selectedModels
			}
			if len(m.responsibilitySelectedModels) == 0 {
				m.responsibilitySelectedModels = m.selectedModels
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "k":
			m.cursorDayIndex--
			if m.cursorDayIndex < 0 {
				m.cursorDayIndex = 0
			}
			return m, nil
		case "l":
			m.cursorDayIndex++
			// Clamp to days in month.
			if m.cursorDayIndex >= daysInMonth(m.monthStart) {
				m.cursorDayIndex = daysInMonth(m.monthStart) - 1
			}
			return m, nil
		case "left":
			m.monthStart = m.monthStart.AddDate(0, -1, 0)
			m.cursorDayIndex = 0
			m.loading = true
			return m, m.fetchChartData()
		case "right":
			m.monthStart = m.monthStart.AddDate(0, 1, 0)
			m.cursorDayIndex = 0
			m.loading = true
			return m, m.fetchChartData()
		}
	}

	return m, nil
}

func (m ChartsModel) fetchChartData() tea.Cmd {
	monthStart := m.monthStart
	es := m.es
	bs := m.bs
	return func() tea.Msg {
		if es == nil {
			return ChartsDataMsg{MonthStart: monthStart, Rows: nil, SelectedModels: nil, Err: nil}
		}
		startLocal := monthStart
		endLocal := startLocal.AddDate(0, 1, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		rows, err := es.QueryDailyCostByModel(ctx, startLocal.UTC(), endLocal.UTC())
		if err != nil {
			return ChartsDataMsg{MonthStart: monthStart, Err: err}
		}

		costSelected := rankModelsByCost(rows, 3)
		performanceSelected := costSelected
		responsibilitySelected := costSelected
		stats := map[string]*chartModelStats{}
		if bs != nil {
			runs, err := bs.QueryRunsInWindow(ctx, startLocal.UTC(), endLocal.UTC())
			if err != nil {
				return ChartsDataMsg{MonthStart: monthStart, Err: err}
			}
			stats = aggregateChartsModelStats(runs)
			performanceSelected = rankChartsByScore(stats, func(s *chartModelStats) float64 { return s.HealthScore }, 3)
			responsibilitySelected = rankChartsByScore(stats, func(s *chartModelStats) float64 { return s.ResponsibilityScore }, 3)
			if len(performanceSelected) == 0 {
				performanceSelected = costSelected
			}
			if len(responsibilitySelected) == 0 {
				responsibilitySelected = costSelected
			}
		}
		return ChartsDataMsg{
			MonthStart:                   monthStart,
			Rows:                         rows,
			ModelStats:                   stats,
			SelectedModels:               costSelected,
			CostSelectedModels:           costSelected,
			PerformanceSelectedModels:    performanceSelected,
			ResponsibilitySelectedModels: responsibilitySelected,
			Err:                          nil,
		}
	}
}

func (m ChartsModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render("Charts")
	sub := fmt.Sprintf("Monthly cost chart plus benchmark summary cards — %s  (←/→ month, k/l or mouse only affect the cost chart)", m.monthStart.Format("January 2006"))

	legendLine := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Render("Legend: R = Responsibility score, H = Health score")
	lines := []string{title + "\n" + sub, legendLine}
	if m.loading {
		lines = append(lines, "Loading…")
	}
	if m.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("Error: "+m.err.Error()))
	}

	start := time.Date(m.monthStart.Year(), m.monthStart.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	days := make([]time.Time, 0, 32)
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}

	costPanel := buildChartPanelData(m.dailyRows, m.selectedModels)
	lines = append(lines, "")
	if len(m.dailyRows) == 0 || len(m.selectedModels) == 0 {
		lines = append(lines, "No cost data for the selected month.")
	} else {
		lines = append(lines, renderChartPanel("Cost chart", days, m.cursorDayIndex, m.width, m.height, costPanel)...)
		tooltipLines := renderChartTooltip(days, m.cursorDayIndex, costPanel)
		if len(tooltipLines) > 0 {
			lines = append(lines, "")
			lines = append(lines, tooltipLines...)
		}
	}

	stats := m.modelStats
	if stats == nil {
		stats = map[string]*chartModelStats{}
	}
	performanceEntries := buildChartSummaryEntries(m.performanceSelectedModels, stats, costPanel.totals)
	responsibilityEntries := buildChartSummaryEntries(m.responsibilitySelectedModels, stats, costPanel.totals)
	if len(performanceEntries) == 0 && len(responsibilityEntries) == 0 {
		performanceEntries = buildChartSummaryEntries(m.selectedModels, stats, costPanel.totals)
		responsibilityEntries = performanceEntries
	}

	cardLines := renderSummaryCards(m.width, performanceEntries, responsibilityEntries)
	if len(cardLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, cardLines...)
	}

	return strings.Join(lines, "\n")
}
