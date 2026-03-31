package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/store"
)

const (
	trackingRefreshInterval = 2 * time.Second
	maxTrackingRows         = 20
)

// trackingTickMsg is sent by the auto-refresh ticker.
type trackingTickMsg struct{ t time.Time }

// TrackingDataMsg carries a fresh batch of events from the store.
// Exported so tests can inject synthetic data.
type TrackingDataMsg struct {
	Events []store.Event
	Err    error
}

// trackingDataMsg is the internal alias retained for the fetchEvents command.
type trackingDataMsg = TrackingDataMsg

// TrackingModel is the Bubble Tea sub-model for the real-time tracking tab.
type TrackingModel struct {
	es      store.EventStore
	events  []store.Event
	err     error
	cursor  int
	loading bool
	// detailOpen toggles an event detail overlay.
	detailOpen  bool
	detailIndex int
}

// Column header widths.
var (
	colWidths = []int{20, 16, 12, 22, 8, 8, 8}
	colNames  = []string{"Time", "Agent", "Type", "Model", "In", "Out", "Spent"}

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cursorStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
)

// NewTrackingModel creates a TrackingModel wired to the given EventStore.
func NewTrackingModel(es store.EventStore) TrackingModel {
	return TrackingModel{
		es:      es,
		loading: true,
	}
}

// Init returns the initial tick command to start auto-refresh.
func (m TrackingModel) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(trackingRefreshInterval, func(t time.Time) tea.Msg {
			return trackingTickMsg{t: t}
		}),
		m.fetchEvents(),
	)
}

// Update handles tick and data messages.
func (m TrackingModel) Update(msg tea.Msg) (TrackingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case trackingTickMsg:
		// Schedule next tick and fetch data.
		return m, tea.Batch(
			tea.Tick(trackingRefreshInterval, func(t time.Time) tea.Msg {
				return trackingTickMsg{t: t}
			}),
			m.fetchEvents(),
		)

	case trackingDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.events = msg.Events
			if m.cursor >= len(m.events) {
				if len(m.events) > 0 {
					m.cursor = len(m.events) - 1
				} else {
					m.cursor = 0
				}
			}
		}

	case tea.KeyMsg:
		if m.detailOpen {
			switch msg.String() {
			case "esc", "escape":
				m.detailOpen = false
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.events)-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.events) {
				m.detailOpen = true
				m.detailIndex = m.cursor
			}
		}
	}
	return m, nil
}

// fetchEvents returns a command that queries the EventStore for recent events.
func (m TrackingModel) fetchEvents() tea.Cmd {
	if m.es == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		events, err := m.es.QueryEvents(ctx, store.EventQuery{
			Limit: maxTrackingRows,
		})
		return TrackingDataMsg{Events: events, Err: err}
	}
}

// View renders the tracking tab.
func (m TrackingModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Real-time Event Stream") + "\n\n")

	if m.loading {
		sb.WriteString(dimStyle.Render("  Loading…") + "\n")
		return sb.String()
	}
	if m.err != nil {
		sb.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", m.err)) + "\n")
		return sb.String()
	}
	if len(m.events) == 0 {
		sb.WriteString(dimStyle.Render("  No events yet. Start tracking to see data here.") + "\n")
		return sb.String()
	}

	if m.detailOpen {
		idx := m.detailIndex
		if idx < 0 || idx >= len(m.events) {
			m.detailOpen = false
			return sb.String()
		}
		ev := m.events[idx]
		return sb.String() + renderEventDetail(ev)
	}

	// Header row.
	sb.WriteString(renderRow(colNames, colWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(colWidths)) + "\n")

	// Data rows.
	for i, ev := range m.events {
		row := formatEventRow(ev)
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = cursorStyle
		}
		sb.WriteString(style.Render(renderRow(row, colWidths, lipgloss.NewStyle())))
		sb.WriteString("\n")
	}

	// Summary counters.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  %d events shown (last %s)", len(m.events), trackingRefreshInterval)))
	sb.WriteString("\n")

	return sb.String()
}

func renderEventDetail(ev store.Event) string {
	// Basic overlay with a border; no metadata by design.
	box := lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).Padding(0, 1)

	ts := ev.Timestamp.Local().Format("2006-01-02 15:04:05")

	getNonNegInt := func(p *int) string {
		if p == nil || *p <= 0 {
			return "-"
		}
		return fmt.Sprintf("%d", *p)
	}
	getInt := func(p *int) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprintf("%d", *p)
	}
	getFloat := func(p *float64) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprintf("%.4f", *p)
	}
	getBool := func(p *bool) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprintf("%v", *p)
	}
	getStr := func(p *string) string {
		if p == nil {
			return "-"
		}
		return *p
	}

	// Duration/cost are often nil; match the table behavior.
	duration := getInt(ev.DurationMs)
	quality := getFloat(ev.QualityScore)
	rework := getInt(ev.ReworkCount)
	cost := "-"
	if ev.CostUSD != nil && *ev.CostUSD > 0 {
		cost = fmt.Sprintf("$%.4f", *ev.CostUSD)
	}

	spent := cost

	lines := []string{
		"Event Detail",
		fmt.Sprintf("Time:   %s", ts),
		fmt.Sprintf("Agent:  %s", ev.AgentID),
		fmt.Sprintf("Type:   %s", ev.EventType),
		fmt.Sprintf("Model:  %s", ev.Model),
		fmt.Sprintf("In:     %s", getNonNegInt(ev.PromptTokens)),
		fmt.Sprintf("Out:    %s", getNonNegInt(ev.CompletionTokens)),
		fmt.Sprintf("Spent:  %s", spent),
		fmt.Sprintf("DurationMs: %s", duration),
		fmt.Sprintf("QualityScore: %s", quality),
		fmt.Sprintf("ReworkCount: %s", rework),
		fmt.Sprintf("ToolName: %s", getStr(ev.ToolName)),
		fmt.Sprintf("ToolSuccess: %s", getBool(ev.ToolSuccess)),
		fmt.Sprintf("ID: %s", ev.ID),
		fmt.Sprintf("SessionID: %s", ev.SessionID),
		"",
		"Press Esc to return.",
	}

	return "\n" + box.Render(strings.Join(lines, "\n")) + "\n"
}

// formatEventRow converts a store.Event into display columns.
func formatEventRow(ev store.Event) []string {
	// Example: 2006-01-02 15:04:05
	ts := ev.Timestamp.Local().Format("2006-01-02 15:04:05")

	in := "-"
	out := "-"
	if ev.PromptTokens != nil && *ev.PromptTokens > 0 {
		in = fmt.Sprintf("%d", *ev.PromptTokens)
	}
	if ev.CompletionTokens != nil && *ev.CompletionTokens > 0 {
		out = fmt.Sprintf("%d", *ev.CompletionTokens)
	}

	spent := "-"
	if ev.CostUSD != nil && *ev.CostUSD > 0 {
		spent = fmt.Sprintf("$%.4f", *ev.CostUSD)
	}

	return []string{ts, ev.AgentID, ev.EventType, ev.Model, in, out, spent}
}

// renderRow renders a table row given columns, widths, and a base style.
func renderRow(cols []string, widths []int, style lipgloss.Style) string {
	var sb strings.Builder
	for i, col := range cols {
		if i >= len(widths) {
			break
		}
		w := widths[i]
		cell := col
		if len(cell) > w {
			cell = cell[:w-1] + "…"
		}
		sb.WriteString(style.Render(fmt.Sprintf("%-*s", w, cell)))
		sb.WriteString(" ")
	}
	return sb.String()
}

// totalWidth sums column widths plus separating spaces.
func totalWidth(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w + 1
	}
	return total
}
