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

// trackingSessionEventsMsg carries the events for an expanded session.
type trackingSessionEventsMsg struct {
	SessionID string
	Events    []store.Event
	Err       error
}

// sessionState holds the expand/collapse state and cached events for a session row.
type sessionState struct {
	expanded bool
	events   []store.Event
	loading  bool
}

// TrackingModel is the Bubble Tea sub-model for the real-time tracking tab.
type TrackingModel struct {
	es       store.EventStore
	sessions []store.SessionSummary
	// sessionStates maps session_id → expand state and cached events.
	sessionStates map[string]*sessionState
	err           error
	// cursor is the index into the flat rendered list (collapsed or expanded rows).
	cursor int
	// pageOffset is the number of sessions skipped (newest first).
	// PgDn increases pageOffset (moves toward older sessions).
	// PgUp decreases pageOffset (moves toward newer sessions).
	pageOffset int
	loading    bool
}

// Column header widths (same columns as before; no extra columns in step 1).
var (
	colWidths = []int{20, 16, 12, 22, 8, 8, 8}
	colNames  = []string{"Time", "Agent", "Type", "Model", "In", "Out", "Spent"}

	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cursorStyle   = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	expandedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
)

// NewTrackingModel creates a TrackingModel wired to the given EventStore.
func NewTrackingModel(es store.EventStore) TrackingModel {
	return TrackingModel{
		es:            es,
		loading:       true,
		sessionStates: make(map[string]*sessionState),
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

// flatRow represents one rendered row in the tracking view.
// It is either a collapsed session row or one event inside an expanded session.
type flatRow struct {
	// isSession is true when this row represents the collapsed/header session line.
	isSession bool
	// sessionIdx is the index into m.sessions for this row's session.
	sessionIdx int
	// eventIdx is the index into the session's events slice (only valid when !isSession).
	eventIdx int
}

// buildFlatRows builds the ordered list of visible rows based on expand state.
func (m TrackingModel) buildFlatRows() []flatRow {
	var rows []flatRow
	for i, s := range m.sessions {
		rows = append(rows, flatRow{isSession: true, sessionIdx: i})
		st := m.sessionStates[s.SessionID]
		if st != nil && st.expanded {
			for j := range st.events {
				rows = append(rows, flatRow{isSession: false, sessionIdx: i, eventIdx: j})
			}
		}
	}
	return rows
}

// Update handles tick, data, and key messages.
func (m TrackingModel) Update(msg tea.Msg) (TrackingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case trackingTickMsg:
		// Schedule next tick and fetch sessions.
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
			// Preserve expand state for sessions still in the new list.
			if m.sessionStates == nil {
				m.sessionStates = make(map[string]*sessionState)
			}
			m.sessions = msg.Sessions
			// Clamp cursor to flat row count.
			rows := m.buildFlatRows()
			if m.cursor >= len(rows) {
				if len(rows) > 0 {
					m.cursor = len(rows) - 1
				} else {
					m.cursor = 0
				}
			}
		}

	case trackingSessionEventsMsg:
		if m.sessionStates == nil {
			m.sessionStates = make(map[string]*sessionState)
		}
		st := m.sessionStates[msg.SessionID]
		if st == nil {
			st = &sessionState{}
			m.sessionStates[msg.SessionID] = st
		}
		st.loading = false
		if msg.Err == nil {
			st.events = msg.Events
		}

	case tea.KeyMsg:
		rows := m.buildFlatRows()

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(rows)-1 {
				m.cursor++
			}
		case " ", "enter":
			// Toggle expand/collapse for the session at the current cursor row.
			if m.cursor >= 0 && m.cursor < len(rows) {
				row := rows[m.cursor]
				sid := m.sessions[row.sessionIdx].SessionID
				if m.sessionStates == nil {
					m.sessionStates = make(map[string]*sessionState)
				}
				st := m.sessionStates[sid]
				if st == nil {
					st = &sessionState{}
					m.sessionStates[sid] = st
				}
				if st.expanded {
					// Collapse: move cursor to the session header row.
					// Find the header index for this session.
					for ri, r := range rows {
						if r.isSession && r.sessionIdx == row.sessionIdx {
							m.cursor = ri
							break
						}
					}
					st.expanded = false
				} else {
					// Expand: if events not yet loaded, fetch them.
					st.expanded = true
					if len(st.events) == 0 && !st.loading {
						st.loading = true
						return m, m.fetchSessionEvents(sid)
					}
				}
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
					// Rough estimate: total events / avg events per session ≈ total/3, capped.
					// For simplicity, jump to a large offset and let the query return empty.
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
	if len(m.sessions) == 0 {
		sb.WriteString(dimStyle.Render("  No events yet. Start tracking to see data here.") + "\n")
		return sb.String()
	}

	// Header row.
	sb.WriteString(renderRow(colNames, colWidths, headerStyle))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", totalWidth(colWidths)) + "\n")

	rows := m.buildFlatRows()

	for ri, row := range rows {
		s := m.sessions[row.sessionIdx]
		st := m.sessionStates[s.SessionID]

		isCursor := ri == m.cursor

		if row.isSession {
			// Collapsed session header: use complete event data from summary.
			prefix := "+ "
			if st != nil && st.expanded {
				prefix = "- "
			}
			cells := formatSessionRow(s, prefix)
			style := lipgloss.NewStyle()
			if isCursor {
				style = cursorStyle
			}
			sb.WriteString(style.Render(renderRow(cells, colWidths, lipgloss.NewStyle())))
			sb.WriteString("\n")
		} else {
			// Expanded event row: indented, dimmed.
			if st == nil || row.eventIdx >= len(st.events) {
				continue
			}
			ev := st.events[row.eventIdx]
			cells := formatEventRow(ev)
			// Indent the first cell (Time) to show nesting.
			if len(cells) > 0 {
				cells[0] = "  " + cells[0]
			}
			style := expandedStyle
			if isCursor {
				style = cursorStyle
			}
			sb.WriteString(style.Render(renderRow(cells, colWidths, lipgloss.NewStyle())))
			sb.WriteString("\n")
		}
	}

	// Pagination footer.
	sb.WriteString("\n")
	pageNum := m.pageOffset/maxTrackingRows + 1
	footer := fmt.Sprintf("  %d sessions shown  |  page %d  (PgUp/PgDn, ↑↓, Space/Enter to expand)",
		len(m.sessions), pageNum)
	sb.WriteString(dimStyle.Render(footer))
	sb.WriteString("\n")

	return sb.String()
}

// formatSessionRow converts a SessionSummary into display columns for the collapsed row.
func formatSessionRow(s store.SessionSummary, prefix string) []string {
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

	// Prefix the agent column with +/- expand indicator.
	agentCell := prefix + s.AgentID

	return []string{ts, agentCell, "complete", s.Model, in, out, spent}
}

// formatEventRow converts a store.Event into display columns.
func formatEventRow(ev store.Event) []string {
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
