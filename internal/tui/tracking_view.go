package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kiosvantra/metronous/internal/config"
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
	es         store.EventStore
	thresholds config.Thresholds
	sessions   []store.SessionSummary
	err        error
	// cursor is the index into the sessions slice (one row per session, always collapsed).
	cursor int
	// pageOffset is the number of sessions skipped (newest first).
	// PgDn increases pageOffset (moves toward older sessions).
	// PgUp decreases pageOffset (moves toward newer sessions).
	pageOffset    int
	loading       bool
	lastViewLines int
	// width and height are updated from tea.WindowSizeMsg so the view can
	// adapt row counts and column widths to the current terminal size.
	width  int
	height int

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
	colWidths = []int{20, 16, 12, 22, 8, 8, 14, 10, 10}
	colNames  = []string{"Time", "Agent", "Type", "Model", "In(accum)", "Out(accum)", "Spent(total)", "Session", "Dur"}

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cursorStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	// Very light gray so low-cost sessions do not feel "disabled".
	spentOkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	spentWarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	spentBadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	sevGreenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	sevAmberStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	sevRedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	popupBgStyle  = lipgloss.NewStyle().
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
		es:         es,
		thresholds: config.DefaultThresholdValues(),
		loading:    true,
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
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

	case ConfigReloadedMsg:
		m.thresholds = msg.Thresholds

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
			maxRows := m.maxVisibleRows()
			switch msg.String() {
			case "up", "k":
				if m.popupCursor > 0 {
					m.popupCursor--
				} else if m.popupOffset > 0 {
					m.popupOffset -= maxRows
					if m.popupOffset < 0 {
						m.popupOffset = 0
					}
					m.popupCursor = maxRows - 1
				}
			case "down", "j":
				visibleCount := len(m.popupEvents) - m.popupOffset
				if visibleCount > maxRows {
					visibleCount = maxRows
				}
				if visibleCount < 0 {
					visibleCount = 0
				}
				if m.popupCursor < visibleCount-1 {
					m.popupCursor++
				} else if m.popupOffset+maxRows < len(m.popupEvents) {
					m.popupOffset += maxRows
					m.popupCursor = 0
				}
			case "pgdown":
				newOffset := m.popupOffset + maxRows
				if newOffset < len(m.popupEvents) {
					m.popupOffset = newOffset
					m.popupCursor = 0
				}
			case "pgup":
				if m.popupOffset >= maxRows {
					m.popupOffset -= maxRows
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
			m.pageOffset += m.maxVisibleRows()
			m.cursor = 0
			return m, m.fetchSessions()
		case "pgup":
			if m.pageOffset >= m.maxVisibleRows() {
				m.pageOffset -= m.maxVisibleRows()
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
					rows := m.maxVisibleRows()
					lastPageOffset := ((total - 1) / rows) * rows
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

// maxVisibleRows returns the number of rows that should be visible in the
// tracking table or popup for the current terminal height. It never exceeds
// maxTrackingRows so the layout remains stable on tall terminals.
func (m TrackingModel) maxVisibleRows() int {
	if m.height <= 0 {
		return maxTrackingRows
	}
	// Reserve a handful of lines for the title, headers, footer, and status so
	// the table does not push them off-screen when the terminal is short.
	rows := m.height - 10
	if rows < 5 {
		rows = 5
	}
	if rows > maxTrackingRows {
		rows = maxTrackingRows
	}
	return rows
}

// fetchSessions returns a command that queries the EventStore for the current page of sessions.
func (m TrackingModel) fetchSessions() tea.Cmd {
	if m.es == nil {
		return nil
	}
	offset := m.pageOffset
	limit := m.maxVisibleRows()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sessions, err := m.es.QuerySessions(ctx, store.SessionQuery{
			Limit:  limit,
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

	maxRows := m.maxVisibleRows()
	effectiveColWidths := colWidths
	if m.width > 0 {
		// Leave a small margin so the table does not hug the window edge.
		available := m.width - 4
		if available < 0 {
			available = m.width
		}
		effectiveColWidths = clampColumnWidths(colWidths, available)
	}

	if m.loading {
		sb.WriteString(dimStyle.Render("  Loading…") + "\n")
		// Pad to fixed height so overlay math is consistent.
		for i := 0; i < maxRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}
	if m.err != nil {
		sb.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", m.err)) + "\n")
		for i := 0; i < maxRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}
	if len(m.sessions) == 0 {
		sb.WriteString(dimStyle.Render("  No events yet. Start tracking to see data here.") + "\n")
		for i := 0; i < maxRows+3; i++ {
			sb.WriteString("\n")
		}
		return sb.String()
	}

	// Header row.
	sb.WriteString(renderRowMain(colNames, effectiveColWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(effectiveColWidths)) + "\n")

	// Session rows — always collapsed, one per session.
	for ri, s := range m.sessions {
		isCursor := ri == m.cursor
		cells := formatSessionRow(s)
		baseStyle := lipgloss.NewStyle()
		if isCursor {
			baseStyle = cursorStyle
		}
		effective := m.thresholds.EffectiveThresholds(s.AgentID)
		spentStyle, durationStyle := severityStylesForCostAndDuration(effective, m.thresholds.TrackingDurationSeverity, m.thresholds.UrgentTriggers.MaxCostSpikeMultiplier, s.CostUSD, s.DurationMs)
		sb.WriteString(renderSessionRowMain(cells, effectiveColWidths, baseStyle, isCursor, spentStyle, durationStyle))
		sb.WriteString("\n")
	}

	// Pad to maxTrackingRows so the background always occupies a fixed height.
	for i := len(m.sessions); i < maxRows; i++ {
		sb.WriteString("\n")
	}

	// Pagination footer.
	sb.WriteString("\n")
	pageNum := m.pageOffset/maxRows + 1
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

	// Total spent is shown in the main table header (spent total from complete),
	// so we do not duplicate it here to keep the popup layout stable.

	if m.popupLoading {
		// Show fixed-height placeholder while events are loading.
		sb.WriteString(popupDimStyle.Render("  Loading events…") + "\n")
		rows := m.maxVisibleRows()
		for i := 0; i < rows+2; i++ {
			sb.WriteString("\n")
		}
	} else if len(m.popupEvents) == 0 {
		sb.WriteString(popupDimStyle.Render("  No events found for this session.") + "\n")
		rows := m.maxVisibleRows()
		for i := 0; i < rows+2; i++ {
			sb.WriteString("\n")
		}
	} else {
		// Timeline columns (no metadata).
		// Spent values here are cumulative snapshots at the time of each event.
		colW := []int{20, 14, 24, 8, 8, 12, 12}
		if m.width > 0 {
			available := m.width - 4
			if available < 0 {
				available = m.width
			}
			colW = clampColumnWidths(colW, available)
		}
		colH := []string{"Time", "Type", "Model", "In", "Out", "Spent(acc)", "Spent(step)"}
		sb.WriteString(popupHeaderStyle.Render(renderRow(colH, colW, lipgloss.NewStyle())) + "\n")
		sb.WriteString(strings.Repeat("─", totalWidth(colW)) + "\n")

		// Compute the visible window [popupOffset, popupOffset+maxVisibleRows).
		start := m.popupOffset
		rows := m.maxVisibleRows()
		end := start + rows
		if end > len(m.popupEvents) {
			end = len(m.popupEvents)
		}
		visible := m.popupEvents[start:end]

		for ri, ev := range visible {
			// Compute prevCost from the event just before this one in the full slice.
			var prevCost float64
			absIdx := start + ri
			if absIdx > 0 && m.popupEvents[absIdx-1].CostUSD != nil {
				prevCost = *m.popupEvents[absIdx-1].CostUSD
			}
			cells := formatEventRowCompact(ev, prevCost)
			row := renderRow(cells, colW, lipgloss.NewStyle())
			if ri == m.popupCursor {
				sb.WriteString(cursorStyle.Render(row) + "\n")
			} else {
				sb.WriteString(popupRowStyle.Render(row) + "\n")
			}
		}

		// Pad viewport to fixed height so popup box stays stable.
		for i := len(visible); i < rows; i++ {
			sb.WriteString("\n")
		}

		// Scroll indicator.
		totalEvents := len(m.popupEvents)
		pageNum := m.popupOffset/rows + 1
		totalPages := (totalEvents + rows - 1) / rows
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
	// Avoid mid-line splicing (ANSI codes + rune width) which can cause
	// misalignment artifacts. Instead, replace whole lines where the popup
	// should appear.
	bgLines := strings.Split(bg, "\n")
	popupLines := strings.Split(popup, "\n")
	// Center the popup vertically within the background while keeping at least
	// a couple of lines of context above it.
	startRow := 0
	if len(bgLines) > len(popupLines) {
		startRow = (len(bgLines) - len(popupLines)) / 2
		if startRow < 2 {
			startRow = 2
		}
	}

	for pi, pline := range popupLines {
		bi := startRow + pi
		if bi >= len(bgLines) {
			bgLines = append(bgLines, "")
		}
		bgLines[bi] = pline
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

	sessionShort := shortSessionID(s.SessionID)

	durationCell := "-"
	if s.DurationMs != nil && *s.DurationMs > 0 {
		durationCell = formatDuration(float64(*s.DurationMs))
	}

	return []string{ts, s.AgentID, "complete", s.Model, in, out, spent, sessionShort, durationCell}
}

func shortSessionID(sid string) string {
	const n = 8
	if len(sid) <= n {
		return sid
	}
	return sid[len(sid)-n:]
}

// formatEventRowCompact converts a store.Event into compact display columns (for popup).
// prevCost is the accumulated cost of the previous event — used to compute the per-step delta.
func formatEventRowCompact(ev store.Event, prevCost float64) []string {
	ts := ev.Timestamp.Local().Format("2006-01-02 15:04:05")

	in := "-"
	out := "-"
	if ev.PromptTokens != nil && *ev.PromptTokens > 0 {
		in = fmt.Sprintf("%d", *ev.PromptTokens)
	}
	if ev.CompletionTokens != nil && *ev.CompletionTokens > 0 {
		out = fmt.Sprintf("%d", *ev.CompletionTokens)
	}

	spentAcc := "-"
	spentStep := "-"
	if ev.CostUSD != nil && *ev.CostUSD > 0 {
		spentAcc = fmt.Sprintf("$%.4f", *ev.CostUSD)
		delta := *ev.CostUSD - prevCost
		if delta > 0 {
			spentStep = fmt.Sprintf("$%.4f", delta)
		}
	}

	return []string{ts, ev.EventType, ev.Model, in, out, spentAcc, spentStep}
}

// renderRowMain renders rows for the main (background) Tracking table.
// It colorizes the Spent column (values that look like $...) in bright red.
func renderRowMain(cols []string, widths []int, style lipgloss.Style) string {
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

func severityStylesForCostAndDuration(thresholds config.DefaultThresholds, tracking config.TrackingDurationSeverityConfig, spikeMult float64, costUSD *float64, durationMs *int) (spentStyle lipgloss.Style, durationStyle lipgloss.Style) {
	// Spent semaforo is based on configured cost thresholds.
	maxCost := thresholds.MaxCostUSDPerSession
	maxSpike := maxCost * spikeMult

	spentStyle = spentOkStyle
	if costUSD == nil || *costUSD <= 0 {
		// Keep low cost style for empty/unknown.
		spentStyle = spentOkStyle
	} else {
		if *costUSD > maxSpike {
			spentStyle = spentBadStyle
		} else if *costUSD > maxCost {
			spentStyle = spentWarnStyle
		} else {
			spentStyle = spentOkStyle
		}
	}

	// Duration semaforo uses the tracking UI display bands.
	durationStyle = dimStyle
	if durationMs == nil || *durationMs <= 0 {
		return spentStyle, durationStyle
	}

	switch tracking.Classify(float64(*durationMs)) {
	case config.DurationSeverityGood:
		return spentStyle, sevGreenStyle
	case config.DurationSeverityWarn:
		return spentStyle, sevAmberStyle
	case config.DurationSeverityCritical:
		return spentStyle, sevRedStyle
	default:
		return spentStyle, durationStyle
	}
}

func renderSessionRowMain(cols []string, widths []int, baseStyle lipgloss.Style, isCursor bool, spentStyle, durationStyle lipgloss.Style) string {
	var sb strings.Builder
	spentCol := 6
	durationCol := 8

	for i, col := range cols {
		if i >= len(widths) {
			break
		}
		w := widths[i]
		cell := col
		if len(cell) > w {
			cell = cell[:w-1] + "…"
		}

		cellStyle := baseStyle
		if i == spentCol {
			cellStyle = spentStyle
		}
		if i == durationCol {
			cellStyle = durationStyle
		}

		sb.WriteString(cellStyle.Render(fmt.Sprintf("%-*s", w, cell)))
		sb.WriteString(" ")
	}
	return sb.String()
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
