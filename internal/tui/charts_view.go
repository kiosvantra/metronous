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
	Rows []store.DailyCostByModelRow
	Err  error
}

// ChartsModel renders daily cost charts per model for a selected month.
type ChartsModel struct {
	es store.EventStore

	width  int
	height int

	monthStart time.Time // local midnight of the 1st day of selected month

	loading bool
	err     error

	dailyRows []store.DailyCostByModelRow

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
func NewChartsModel(es store.EventStore) ChartsModel {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	return ChartsModel{
		es:             es,
		monthStart:     monthStart,
		loading:        true,
		cursorDayIndex: 0,
	}
}

func (m ChartsModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchDailyCost(),
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
			m.fetchDailyCost(),
		)

	case ChartsDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.dailyRows = msg.Rows
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
			return m, m.fetchDailyCost()
		case "right":
			m.monthStart = m.monthStart.AddDate(0, 1, 0)
			m.cursorDayIndex = 0
			m.loading = true
			return m, m.fetchDailyCost()
		}
	}

	return m, nil
}

func (m ChartsModel) fetchDailyCost() tea.Cmd {
	return func() tea.Msg {
		if m.es == nil {
			return ChartsDataMsg{Rows: nil, Err: nil}
		}
		startLocal := m.monthStart
		endLocal := startLocal.AddDate(0, 1, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		rows, err := m.es.QueryDailyCostByModel(ctx, startLocal.UTC(), endLocal.UTC())
		return ChartsDataMsg{Rows: rows, Err: err}
	}
}

func (m ChartsModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render("Charts")

	sub := fmt.Sprintf("Daily cost by model (stacked) — %s  (use ←/→)", m.monthStart.Format("January 2006"))

	palette := []lipgloss.Color{
		lipgloss.Color("82"),  // green
		lipgloss.Color("226"), // yellow/orange
		lipgloss.Color("196"), // red
		lipgloss.Color("33"),  // blue
		lipgloss.Color("93"),  // purple
		lipgloss.Color("214"), // pink
		lipgloss.Color("118"), // teal
		lipgloss.Color("45"),  // violet
	}

	// Build totals per model for legend ordering and segment capping.
	totals := make(map[string]float64)
	for _, r := range m.dailyRows {
		totals[r.Model] += r.TotalCostUSD
	}

	modelsByTotal := make([]struct {
		Model string
		Total float64
	}, 0, len(totals))
	for model, total := range totals {
		modelsByTotal = append(modelsByTotal, struct {
			Model string
			Total float64
		}{Model: model, Total: total})
	}
	sort.SliceStable(modelsByTotal, func(i, j int) bool {
		return modelsByTotal[i].Total > modelsByTotal[j].Total
	})

	maxSegments := len(palette)
	segmentModels := []string{}
	otherTotal := 0.0
	for i, mrow := range modelsByTotal {
		if i < maxSegments-1 {
			segmentModels = append(segmentModels, mrow.Model)
		} else {
			otherTotal += mrow.Total
		}
	}
	if len(modelsByTotal) > maxSegments-1 {
		segmentModels = append(segmentModels, "Other")
	} else if len(modelsByTotal) == 0 {
		segmentModels = []string{}
	}

	colors := make(map[string]lipgloss.Color)
	for i, model := range segmentModels {
		colors[model] = palette[i%len(palette)]
	}

	legendLines := []string{}
	if len(segmentModels) > 0 {
		maxLegendWidth := m.width - 2
		if maxLegendWidth < 40 {
			maxLegendWidth = 80
		}
		line := "Legend: "
		for i, model := range segmentModels {
			c := colors[model]
			block := lipgloss.NewStyle().Foreground(c).Render("█")
			entry := fmt.Sprintf("%s %s", block, model)
			sep := "  "
			if i == 0 {
				sep = ""
			}
			candidate := line + sep + entry
			if lipgloss.Width(candidate) > maxLegendWidth && line != "Legend: " {
				legendLines = append(legendLines, line)
				line = "          " + entry
			} else {
				line = candidate
			}
		}
		legendLines = append(legendLines, line)
	} else {
		legendLines = []string{"Legend: (no data for selected month)"}
	}

	// Prepare day buckets for the selected month.
	start := time.Date(m.monthStart.Year(), m.monthStart.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	days := make([]time.Time, 0, 32)
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}

	costs := make(map[string]map[string]float64) // dayKey -> model -> cost
	for _, r := range m.dailyRows {
		dayKey := r.Day.Format("2006-01-02")
		bucket := costs[dayKey]
		if bucket == nil {
			bucket = make(map[string]float64)
			costs[dayKey] = bucket
		}
		// If model is not in segmentModels, route it into Other.
		if _, ok := colors[r.Model]; ok {
			bucket[r.Model] += r.TotalCostUSD
		} else {
			bucket["Other"] += r.TotalCostUSD
		}
	}

	lines := []string{}
	lines = append(lines, title+"\n"+sub)
	if m.loading {
		lines = append(lines, "Loading…")
	}
	if m.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("Error: "+m.err.Error()))
	}

	// Vertical stacked bar chart (one column per day), with spacing and
	// logarithmic Y axis.
	cellWidth := 4
	leftGutter := 10 // includes y-axis labels

	maxCols := (m.width - leftGutter - 1)
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

	barHeight := 12
	if m.height > 0 {
		barHeight = m.height - 15
		if barHeight < 8 {
			barHeight = 8
		}
		if barHeight > 18 {
			barHeight = 18
		}
	}

	blocks := make(map[string]string)
	for _, model := range segmentModels {
		blocks[model] = lipgloss.NewStyle().Foreground(colors[model]).Render("█")
	}

	dayTotals := make([]float64, 0, len(days))
	for _, d := range days {
		rowCosts := costs[d.Format("2006-01-02")]
		total := 0.0
		for _, model := range segmentModels {
			if rowCosts != nil {
				total += rowCosts[model]
			}
		}
		dayTotals = append(dayTotals, total)
	}

	// Split chart in chunks if terminal is narrow.
	chartLinesStart := len(lines)
	for chunkStart := 0; chunkStart < len(days); chunkStart += chunkSize {
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > len(days) {
			chunkEnd = len(days)
		}
		chunkDays := days[chunkStart:chunkEnd]
		chunkTotals := dayTotals[chunkStart:chunkEnd]

		// Compute log scale bounds for this chunk.
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
			if cost <= 0 || len(segmentModels) == 0 {
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

		// Build Y ticks (logarithmic).
		tickCount := 5
		yTicks := make([]float64, 0, tickCount)
		for i := 0; i < tickCount; i++ {
			r := float64(i) / float64(tickCount-1)
			val := math.Pow(10, logMax-(r*(logMax-logMin)))
			yTicks = append(yTicks, val)
		}

		tickRowLabels := make(map[int]string) // rowBlocks -> label
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

		// Precompute stacked segment blocks per day.
		heightsPerDay := make([][]int, len(chunkDays))
		for i, d := range chunkDays {
			rowCosts := costs[d.Format("2006-01-02")]
			totalCost := chunkTotals[i]
			totalBlocks := costToBlocks(totalCost)
			if totalBlocks <= 0 || rowCosts == nil {
				heightsPerDay[i] = make([]int, len(segmentModels))
				continue
			}

			floors := make([]int, len(segmentModels))
			fracs := make([]float64, len(segmentModels))
			sumFloors := 0
			for j, model := range segmentModels {
				c := rowCosts[model]
				segExact := (float64(totalBlocks) * c) / totalCost
				f := int(segExact)
				floors[j] = f
				fracs[j] = segExact - float64(f)
				sumFloors += f
			}

			rem := totalBlocks - sumFloors
			heights := make([]int, len(segmentModels))
			copy(heights, floors)
			if rem > 0 {
				idx := make([]int, len(segmentModels))
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

		// Render chart rows.
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
				for j := range segmentModels {
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
				// Center bar in the cell.
				row += " " + blocks[segmentModels[seg]] + " " + " "
			}
			lines = append(lines, row)
		}

		// X axis baseline.
		base := strings.Repeat(" ", leftGutter) + "+"
		for range chunkDays {
			base += strings.Repeat("-", cellWidth)
		}
		lines = append(lines, base)

		// X axis labels (day number, spaced by column width).
		labelRow := strings.Repeat(" ", leftGutter) + " "
		for _, d := range chunkDays {
			lab := fmt.Sprintf("%2d", d.Day())
			if cellWidth > 2 {
				lab = " " + lab + strings.Repeat(" ", cellWidth-3)
			}
			// Ensure exact width.
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

	// Tooltip (keyboard/mouse cursor) above the legend.
	if len(days) > 0 {
		idx := m.cursorDayIndex
		if idx < 0 {
			idx = 0
		}
		if idx >= len(days) {
			idx = len(days) - 1
		}
		cursorDay := days[idx]
		dayKey := cursorDay.Format("2006-01-02")
		rowCosts := costs[dayKey]

		total := 0.0
		for _, model := range segmentModels {
			if rowCosts != nil {
				total += rowCosts[model]
			}
		}

		tooltipLines := []string{}
		tooltipLines = append(tooltipLines, fmt.Sprintf("Tooltip: %s ($%.2f)", cursorDay.Format("Jan 02"), total))
		if total <= 0 || rowCosts == nil {
			tooltipLines = append(tooltipLines, "(sin consumo en este dia)")
		} else {
			// Build entries sorted by cost desc.
			type ent struct {
				model string
				cost  float64
			}
			ents := make([]ent, 0, len(segmentModels))
			for _, model := range segmentModels {
				c := rowCosts[model]
				ents = append(ents, ent{model: model, cost: c})
			}
			sort.SliceStable(ents, func(i, j int) bool { return ents[i].cost > ents[j].cost })
			limit := 6
			if len(ents) < limit {
				limit = len(ents)
			}
			for i := 0; i < limit; i++ {
				if ents[i].cost <= 0 {
					continue
				}
				block := blocks[ents[i].model]
				tooltipLines = append(tooltipLines, fmt.Sprintf("- %s %s: $%.3f", block, ents[i].model, ents[i].cost))
			}
		}

		lines = append(lines, "")
		lines = append(lines, tooltipLines...)
	}

	// Legend below the chart.
	if len(days) > 0 {
		if len(lines) > chartLinesStart {
			lines = append(lines, "")
		}
		lines = append(lines, legendLines...)
	}

	return strings.Join(lines, "\n")
}
