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

// TrackingDataMsg carries a fresh batch of session summaries from the store.
// Exported so tests can inject synthetic data.
type TrackingDataMsg struct {
	Sessions []store.SessionSummary
	Err      error
}

// trackingDataMsg is the internal alias retained for fetchSessions command.
type trackingDataMsg = TrackingDataMsg

// trackingSessionEventsMsg carries the events for a session popup.
type trackingSessionEventsMsg struct {
	SessionID string
	Events    []store.Event
	Err       error
}

// TrackingModel is the Bubble Tea sub-model for the real-time tracking tab.
type TrackingModel struct {
	es       store.EventStore
	sessions []store.SessionSummary
	err      error
	// cursor is the index into the sessions slice (one row per session, always collapsed).
	cursor int
	// pageOffset is the number of sessions skipped (newest first).
	// PgDn increases pageOffset (moves toward older sessions).
	// PgUp decreases pageOffset (moves toward newer sessions).
	pageOffset    int
	loading       bool
	lastViewLines int

	// Popup state — frozen at moment of opening, not updated by background refresh.
	popupOpen      bool
	popupSessionID string
	popupEvents    []store.Event
	popupLoading   bool
	// Popup viewport: cursor within the 20-row viewport, offset for PgUp/PgDn.
	popupCursor int
	popupOffset int
}

// Column header widths.
var (
	colWidths = []int{20, 16, 12, 22, 8, 8, 8}
	colNames  = []string{"Time", "Agent", "Type", "Model", "In", "Out", "Spent"}

	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cursorStyle  = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	popupBgStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("33")).
			Padding(0, 1)
	popupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	popupDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	popupRowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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
		m.fetchSessions(),
	)
}

// Update handles tick, data, and key messages.
func (m TrackingModel) Update(msg tea.Msg) (TrackingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case trackingTickMsg:
		// Schedule next tick and fetch sessions (popup data is NOT updated).
		return m, tea.Batch(
			tea.Tick(trackingRefreshInterval, func(t time.Time) tea.Msg {
				return trackingTickMsg{t: t}
			}),
			m.fetchSessions(),
		)

	case trackingDataMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.sessions = msg.Sessions
			// Clamp cursor to session count.
			if m.cursor >= len(m.sessions) {
				if len(m.sessions) > 0 {
					m.cursor = len(m.sessions) - 1
				} else {
					m.cursor = 0
				}
			}
		}
		// Popup data is intentionally NOT updated here — it stays frozen.

	case trackingSessionEventsMsg:
		// Only update popup if this response matches the current popup session.
		if m.popupOpen && msg.SessionID == m.popupSessionID {
			m.popupLoading = false
			if msg.Err == nil {
				m.popupEvents = msg.Events
			}
		}

	case tea.KeyMsg:
		// Esc always closes the popup first.
		if msg.Type == tea.KeyEsc && m.popupOpen {
			m.popupOpen = false
			m.popupEvents = nil
			m.popupSessionID = ""
			m.popupLoading = false
			m.popupCursor = 0
			m.popupOffset = 0
			return m, nil
		}

		// If popup is open, route navigation keys into popup viewport.
		if m.popupOpen {
			switch msg.String() {
			case "up", "k":
				if m.popupCursor > 0 {
					m.popupCursor--
				} else if m.popupOffset > 0 {
					m.popupOffset -= maxTrackingRows
					if m.popupOffset < 0 {
						m.popupOffset = 0
					}
					m.popupCursor = maxTrackingRows - 1
				}
			case "down", "j":
				visibleCount := len(m.popupEvents) - m.popupOffset
				if visibleCount > maxTrackingRows {
					visibleCount = maxTrackingRows
				}
				if visibleCount < 0 {
					visibleCount = 0
				}
				if m.popupCursor < visibleCount-1 {
					m.popupCursor++
				} else if m.popupOffset+maxTrackingRows < len(m.popupEvents) {
					m.popupOffset += maxTrackingRows
					m.popupCursor = 0
				}
			case "pgdown":
				newOffset := m.popupOffset + maxTrackingRows
				if newOffset < len(m.popupEvents) {
					m.popupOffset = newOffset
					m.popupCursor = 0
				}
			case "pgup":
				if m.popupOffset >= maxTrackingRows {
					m.popupOffset -= maxTrackingRows
				} else {
					m.popupOffset = 0
				}
				m.popupCursor = 0
			}
			return m, nil
		}

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter":
			// Open popup for the selected session, freeze events at this moment.
			if m.cursor >= 0 && m.cursor < len(m.sessions) {
				sid := m.sessions[m.cursor].SessionID
				m.popupOpen = true
				m.popupSessionID = sid
				m.popupEvents = nil
				m.popupLoading = true
				m.popupCursor = 0
				m.popupOffset = 0
				return m, m.fetchSessionEvents(sid)
			}
		case "pgdown":
			m.pageOffset += maxTrackingRows
			m.cursor = 0
			return m, m.fetchSessions()
		case "pgup":
			if m.pageOffset >= maxTrackingRows {
				m.pageOffset -= maxTrackingRows
			} else {
				m.pageOffset = 0
			}
			m.cursor = 0
			return m, m.fetchSessions()
		case "end":
			m.pageOffset = 0
			m.cursor = 0
			return m, m.fetchSessions()
		case "home":
			// Jump to oldest page using event count as approximation.
			if m.es != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				total, err := m.es.CountEvents(ctx, store.EventQuery{})
				if err == nil && total > 0 {
					lastPageOffset := ((total - 1) / maxTrackingRows) * maxTrackingRows
					m.pageOffset = lastPageOffset
					m.cursor = 0
					return m, m.fetchSessions()
				}
			}
			m.pageOffset = 0
			m.cursor = 0
			return m, m.fetchSessions()
		}
	}
	return m, nil
}

// fetchSessions returns a command that queries the EventStore for the current page of sessions.
func (m TrackingModel) fetchSessions() tea.Cmd {
	if m.es == nil {
		return nil
	}
	offset := m.pageOffset
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sessions, err := m.es.QuerySessions(ctx, store.SessionQuery{
			Limit:  maxTrackingRows,
			Offset: offset,
		})
		return TrackingDataMsg{Sessions: sessions, Err: err}
	}
}

// fetchSessionEvents returns a command that loads events for a specific session.
func (m TrackingModel) fetchSessionEvents(sessionID string) tea.Cmd {
	if m.es == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		events, err := m.es.GetSessionEvents(ctx, sessionID)
		return trackingSessionEventsMsg{SessionID: sessionID, Events: events, Err: err}
	}
}

// View renders the tracking tab.
func (m *TrackingModel) View() string {
	// Always render the full background list at a fixed height, then overlay the popup.
	bg := m.renderBackground()

	if m.popupOpen {
		overlay := m.renderPopup()
		out := overlayPopup(bg, overlay)
		// Stabilize line count to prevent terminal remnants.
		out = m.stabilizeLines(out)
		return out
	}

	return m.stabilizeLines(bg)
}

// renderBackground renders the fixed-height session list (background layer).
func (m *TrackingModel) renderBackground() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Real-time Event Stream") + "\n\n")

	if m.loading {
		sb.WriteString(dimStyle.Render("  Loading…") + "\n")
		// Pad to fixed height so overlay math is consistent.
		for i := 0; i < maxTrackingRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}
	if m.err != nil {
		sb.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", m.err)) + "\n")
		for i := 0; i < maxTrackingRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}
	if len(m.sessions) == 0 {
		sb.WriteString(dimStyle.Render("  No events yet. Start tracking to see data here.") + "\n")
		for i := 0; i < maxTrackingRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}

	// Header row.
	sb.WriteString(renderRow(colNames, colWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(colWidths)) + "\n")

	// Session rows — always collapsed, one per session.
	for ri, s := range m.sessions {
		isCursor := ri == m.cursor
		cells := formatSessionRow(s)
		style := lipgloss.NewStyle()
		if isCursor {
			style = cursorStyle
		}
		sb.WriteString(style.Render(renderRow(cells, colWidths, lipgloss.NewStyle())))
		sb.WriteString("\n")
	}

	// Pad to maxTrackingRows so the background always occupies a fixed height.
	for i := len(m.sessions); i < maxTrackingRows; i++ {
		sb.WriteString("\n")
	}

	// Pagination footer.
	sb.WriteString("\n")
	pageNum := m.pageOffset/maxTrackingRows + 1
	footer := fmt.Sprintf("  %d sessions shown  |  page %d  (PgUp/PgDn, ↑↓, Enter to open timeline)",
		len(m.sessions), pageNum)
	sb.WriteString(dimStyle.Render(footer))
	sb.WriteString("\n")

	return sb.String()
}

// renderPopup renders the modal popup with the frozen session timeline.
// Only maxTrackingRows event rows are shown at a time; popupOffset and popupCursor
// control which slice is visible. popupEvents is never refetched here.
func (m *TrackingModel) renderPopup() string {
	var sb strings.Builder

	// Title.
	title := fmt.Sprintf("Session Timeline: %s", m.popupSessionID)
	sb.WriteString(popupHeaderStyle.Render(title) + "\n")
	sb.WriteString(strings.Repeat("─", min(len(title)+4, 80)) + "\n")

	// Compute total spent from the session's complete event (actual final cost).
	totalSpent := "-"
	if !m.popupLoading {
		var maxCost float64
		found := false
		for _, ev := range m.popupEvents {
			if ev.EventType != "complete" {
				continue
			}
			if ev.CostUSD == nil {
				continue
			}
			if *ev.CostUSD <= 0 {
				continue
			}
			if !found || *ev.CostUSD > maxCost {
				maxCost = *ev.CostUSD
				found = true
			}
		}
		if found {
			totalSpent = fmt.Sprintf("$%.4f", maxCost)
		}
	}
	// Show in the popup header so the user can see real final cost.
	sb.WriteString(popupDimStyle.Render(fmt.Sprintf("  Total spent: %s", totalSpent)) + "\n")

	if m.popupLoading {
		// Show fixed-height placeholder while events are loading.
		sb.WriteString(popupDimStyle.Render("  Loading events…") + "\n")
		for i := 0; i < maxTrackingRows+2; i++ {
			sb.WriteString("\n")
		}
	} else if len(m.popupEvents) == 0 {
		sb.WriteString(popupDimStyle.Render("  No events found for this session.") + "\n")
		for i := 0; i < maxTrackingRows+2; i++ {
			sb.WriteString("\n")
		}
	} else {
		// Timeline columns (no metadata).
		colW := []int{20, 14, 24, 8, 8, 8}
		colH := []string{"Time", "Type", "Model", "In", "Out", "Spent"}
		sb.WriteString(popupHeaderStyle.Render(renderRow(colH, colW, lipgloss.NewStyle())) + "\n")
		sb.WriteString(strings.Repeat("─", totalWidth(colW)) + "\n")

		// Compute the visible window [popupOffset, popupOffset+maxTrackingRows).
		start := m.popupOffset
		end := start + maxTrackingRows
		if end > len(m.popupEvents) {
			end = len(m.popupEvents)
		}
		visible := m.popupEvents[start:end]

		for ri, ev := range visible {
			cells := formatEventRowCompact(ev)
			row := renderRow(cells, colW, lipgloss.NewStyle())
			if ri == m.popupCursor {
				sb.WriteString(cursorStyle.Render(row) + "\n")
			} else {
				sb.WriteString(popupRowStyle.Render(row) + "\n")
			}
		}

		// Pad viewport to fixed height so popup box stays stable.
		for i := len(visible); i < maxTrackingRows; i++ {
			sb.WriteString("\n")
		}

		// Scroll indicator.
		totalEvents := len(m.popupEvents)
		pageNum := m.popupOffset/maxTrackingRows + 1
		totalPages := (totalEvents + maxTrackingRows - 1) / maxTrackingRows
		scrollInfo := fmt.Sprintf("  %d/%d events  |  page %d/%d  (↑↓ / PgUp/PgDn)", totalEvents, totalEvents, pageNum, totalPages)
		if totalPages == 1 {
			scrollInfo = fmt.Sprintf("  %d events", totalEvents)
		}
		sb.WriteString(popupDimStyle.Render(scrollInfo) + "\n")
	}

	sb.WriteString(popupDimStyle.Render("  Esc to close"))

	return popupBgStyle.Render(sb.String())
}

// overlayPopup places the popup box in the center of the background string.
func overlayPopup(bg, popup string) string {
	bgLines := strings.Split(bg, "\n")
	popupLines := strings.Split(popup, "\n")

	// Find popup width.
	popupW := 0
	for _, l := range popupLines {
		if len(l) > popupW {
			popupW = len(l)
		}
	}

	// Find background width.
	bgW := 0
	for _, l := range bgLines {
		if len(l) > bgW {
			bgW = len(l)
		}
	}

	// Position: horizontally centered, vertically at row 4 (after header).
	startRow := 4
	startCol := (bgW - popupW) / 2
	if startCol < 2 {
		startCol = 2
	}

	// Merge popup into background lines.
	for pi, pline := range popupLines {
		bi := startRow + pi
		if bi >= len(bgLines) {
			bgLines = append(bgLines, "")
		}
		bgLine := bgLines[bi]
		// Pad bgLine to startCol if needed.
		if len(bgLine) < startCol {
			bgLine += strings.Repeat(" ", startCol-len(bgLine))
		}
		// Replace characters in the bg line with the popup line.
		if startCol+len(pline) <= len(bgLine) {
			bgLines[bi] = bgLine[:startCol] + pline + bgLine[startCol+len(pline):]
		} else {
			bgLines[bi] = bgLine[:startCol] + pline
		}
	}

	return strings.Join(bgLines, "\n")
}

// stabilizeLines ensures the output always occupies at least as many lines as
// the previous render, preventing terminal remnant artifacts.
func (m *TrackingModel) stabilizeLines(out string) string {
	lineCount := strings.Count(out, "\n")
	if lineCount < m.lastViewLines {
		out += strings.Repeat("\n", m.lastViewLines-lineCount)
	}
	m.lastViewLines = max(m.lastViewLines, lineCount)
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// formatSessionRow converts a SessionSummary into display columns for the collapsed row.
func formatSessionRow(s store.SessionSummary) []string {
	ts := s.Timestamp.Local().Format("2006-01-02 15:04:05")

	in := "-"
	out := "-"
	if s.PromptTokens != nil && *s.PromptTokens > 0 {
		in = fmt.Sprintf("%d", *s.PromptTokens)
	}
	if s.CompletionTokens != nil && *s.CompletionTokens > 0 {
		out = fmt.Sprintf("%d", *s.CompletionTokens)
	}

	spent := "-"
	if s.CostUSD != nil && *s.CostUSD > 0 {
		spent = fmt.Sprintf("$%.4f", *s.CostUSD)
	}

	return []string{ts, s.AgentID, "complete", s.Model, in, out, spent}
}

// formatEventRowCompact converts a store.Event into compact display columns (for popup).
func formatEventRowCompact(ev store.Event) []string {
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

	return []string{ts, ev.EventType, ev.Model, in, out, spent}
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
