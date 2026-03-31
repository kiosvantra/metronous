package tui_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/tui"
)

// ----- helpers ----------------------------------------------------------------

func sendKey(m tea.Model, key string) (tea.Model, tea.Cmd) {
	return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func sendSpecialKey(m tea.Model, keyType tea.KeyType) (tea.Model, tea.Cmd) {
	return m.Update(tea.KeyMsg{Type: keyType})
}

func newTestApp(t *testing.T) tui.AppModel {
	t.Helper()
	return tui.NewAppModel(nil, nil, "", "", "", "test")
}

// ----- Task 26: App shell tests -----------------------------------------------

func TestAppInitialModel(t *testing.T) {
	m := newTestApp(t)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected initial tab to be TabTracking (0), got %d", m.CurrentTab)
	}
}

func TestAppInit(t *testing.T) {
	m := newTestApp(t)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() returned nil cmd")
	}
}

func TestAppTabSwitchingByNumber(t *testing.T) {
	m := newTestApp(t)

	updated, _ := sendKey(m, "2")
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabBenchmark {
		t.Errorf("expected TabBenchmark after pressing 2, got %d", m.CurrentTab)
	}

	updated, _ = sendKey(m, "3")
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabConfig {
		t.Errorf("expected TabConfig after pressing 3, got %d", m.CurrentTab)
	}

	updated, _ = sendKey(m, "1")
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected TabTracking after pressing 1, got %d", m.CurrentTab)
	}
}

func TestAppTabSwitchingByArrowKeys(t *testing.T) {
	m := newTestApp(t)

	updated, _ := sendSpecialKey(m, tea.KeyRight)
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabBenchmark {
		t.Errorf("expected TabBenchmark after right arrow, got %d", m.CurrentTab)
	}

	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabConfig {
		t.Errorf("expected TabConfig after right arrow, got %d", m.CurrentTab)
	}

	updated, _ = sendSpecialKey(m, tea.KeyLeft)
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabBenchmark {
		t.Errorf("expected TabBenchmark after left arrow, got %d", m.CurrentTab)
	}
}

func TestAppArrowKeyDoesNotWrapBeyondBounds(t *testing.T) {
	m := newTestApp(t)

	updated, _ := sendSpecialKey(m, tea.KeyLeft)
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected tab to stay at TabTracking, got %d", m.CurrentTab)
	}

	updated, _ = sendKey(m, "3")
	m = updated.(tui.AppModel)
	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(tui.AppModel)
	if m.CurrentTab != tui.TabConfig {
		t.Errorf("expected tab to stay at TabConfig, got %d", m.CurrentTab)
	}
}

func TestAppWindowResize(t *testing.T) {
	m := newTestApp(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(tui.AppModel)
	if m.Width != 120 || m.Height != 40 {
		t.Errorf("expected Width=120 Height=40, got %d/%d", m.Width, m.Height)
	}
}

func TestAppQuitKey(t *testing.T) {
	m := newTestApp(t)
	_, cmd := sendKey(m, "q")
	if cmd == nil {
		t.Error("expected quit command, got nil")
	}
}

func TestAppView(t *testing.T) {
	m := newTestApp(t)
	// Without window size should not panic.
	_ = m.View()
	// With window size should contain tab names.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	v := updated.(tui.AppModel).View()
	if !strings.Contains(v, "Tracking") {
		t.Errorf("view should contain 'Tracking', got: %q", v)
	}
}

// ----- Task 27: Tracking view tests ------------------------------------------

func TestTrackingViewRendersRecentEvents(t *testing.T) {
	m := tui.NewTrackingModel(nil)

	tokens := 100
	cost := 0.001
	m, _ = m.Update(tui.TrackingDataMsg{
		Sessions: []store.SessionSummary{
			{
				SessionID:        "sess-1",
				AgentID:          "test-agent",
				Model:            "gpt-4",
				Timestamp:        time.Now(),
				PromptTokens:     &tokens,
				CompletionTokens: &tokens,
				CostUSD:          &cost,
			},
		},
	})

	view := m.View()
	if !strings.Contains(view, "test-agent") {
		t.Errorf("expected 'test-agent' in view, got: %q", view)
	}
	// Collapsed session rows always show "complete" as the Type column.
	if !strings.Contains(view, "complete") {
		t.Errorf("expected 'complete' in view, got: %q", view)
	}
}

func TestTrackingViewPollsEveryTwoSeconds(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() should return a tick command, got nil")
	}
}

func TestTrackingViewShowsEmptyState(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: nil})
	view := m.View()
	if !strings.Contains(view, "No events") {
		t.Errorf("expected empty state message, got: %q", view)
	}
}

// ----- Task 28: Benchmark view tests -----------------------------------------

func TestBenchmarkViewRendersHistoricalRuns(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{
		Runs: []store.BenchmarkRun{
			{
				AgentID:      "agent-a",
				Model:        "gpt-4",
				RunAt:        time.Now(),
				Accuracy:     0.95,
				P95LatencyMs: 1200,
				Verdict:      store.VerdictKeep,
			},
		},
	})

	view := m.View()
	if !strings.Contains(view, "agent-a") {
		t.Errorf("expected 'agent-a' in view, got: %q", view)
	}
	if !strings.Contains(view, "KEEP") {
		t.Errorf("expected 'KEEP' in view, got: %q", view)
	}
}

func TestBenchmarkViewShowsEmptyState(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: nil})
	view := m.View()
	if !strings.Contains(view, "No benchmark") {
		t.Errorf("expected empty state, got: %q", view)
	}
}

// ----- Task 29: Config view tests --------------------------------------------

func TestConfigViewEditsThresholdValue(t *testing.T) {
	m := tui.NewConfigModel("")
	m, _ = m.Update(tui.ConfigReloadedMsg{Thresholds: tui.DefaultThresholdValuesForTest()})

	initial := m.GetCurrentFieldValue()

	// Press "=" to increase the current field.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("=")})
	after := m.GetCurrentFieldValue()

	if after <= initial {
		t.Errorf("expected value to increase, got initial=%f after=%f", initial, after)
	}
}

func TestConfigViewSaveReload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/thresholds.json"

	m := tui.NewConfigModel(path)
	m, _ = m.Update(tui.ConfigReloadedMsg{Thresholds: tui.DefaultThresholdValuesForTest()})

	// Save.
	m, saveCmd := m.UpdateSave(tea.KeyMsg{})
	if saveCmd == nil {
		t.Fatal("expected save command")
	}
	result := saveCmd()
	m, _ = m.Update(result)

	view := m.View()
	if !strings.Contains(view, "Saved") {
		t.Errorf("expected 'Saved' in view after save, got: %q", view)
	}

	// Reload.
	m, reloadCmd := m.UpdateReload(tea.KeyMsg{})
	if reloadCmd == nil {
		t.Fatal("expected reload command")
	}
	result = reloadCmd()
	m, _ = m.Update(result)

	view = m.View()
	if !strings.Contains(view, "Reload") {
		t.Errorf("expected 'Reload' in view after reload, got: %q", view)
	}
}

func TestConfigViewInvalidValueShownWithError(t *testing.T) {
	m := tui.NewConfigModel("")
	m, _ = m.Update(tui.ConfigReloadedMsg{Thresholds: tui.DefaultThresholdValuesForTest()})

	// Inject an error message.
	m, _ = m.Update(tui.ConfigErrMsg{Err: nil})
	// Just ensure View() doesn't panic.
	_ = m.View()
}

// ----- Benchmark pagination tests (Task: Benchmark tab improvements) ---------

// makeRuns builds N BenchmarkRun entries with distinct timestamps.
func makeRuns(n int) []store.BenchmarkRun {
	runs := make([]store.BenchmarkRun, n)
	base := time.Now()
	for i := 0; i < n; i++ {
		runs[i] = store.BenchmarkRun{
			AgentID:  fmt.Sprintf("agent-%02d", i),
			Model:    "gpt-4",
			RunAt:    base.Add(time.Duration(-i) * time.Hour),
			Accuracy: 0.9,
			Verdict:  store.VerdictKeep,
		}
	}
	return runs
}

// TestBenchmarkPageSizeIs20 verifies the page-size constant is 20.
func TestBenchmarkPageSizeIs20(t *testing.T) {
	if tui.BenchmarkPageSize != 20 {
		t.Errorf("expected BenchmarkPageSize == 20, got %d", tui.BenchmarkPageSize)
	}
}

// TestBenchmarkViewRendersMax20Rows verifies that injecting 25 runs renders at most 20 rows.
func TestBenchmarkViewRendersMax20Rows(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(25)})

	// Count table data rows by counting lines that contain "agent-" followed by a space
	// (column padding) — this matches only table rows, not the detail panel.
	view := m.View()
	tableRowCount := 0
	for _, line := range strings.Split(view, "\n") {
		// Table data rows contain "agent-XX" padded with spaces to column width.
		// The detail panel shows "Agent:    agent-XX" which won't match this pattern.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "2026-") || strings.HasPrefix(trimmed, "-  ") {
			// Lines starting with a date/time (data rows) or "-" (no-data rows).
			if strings.Contains(trimmed, "agent-") {
				tableRowCount++
			}
		}
	}
	if tableRowCount > 20 {
		t.Errorf("expected at most 20 table data rows rendered, got %d", tableRowCount)
	}
	if tableRowCount == 0 {
		t.Errorf("expected some rows rendered, got 0 (view: %q)", view)
	}
}

// TestBenchmarkPgDnIncreasesPageOffset verifies PgDn moves pageOffset forward by one page.
func TestBenchmarkPgDnIncreasesPageOffset(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	// Inject data so the model is not in loading state.
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(20)})

	initialOffset := tui.GetBenchmarkPageOffset(m)
	if initialOffset != 0 {
		t.Fatalf("expected initial pageOffset = 0, got %d", initialOffset)
	}

	// Send PgDn.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	afterOffset := tui.GetBenchmarkPageOffset(m)
	if afterOffset != tui.BenchmarkPageSize {
		t.Errorf("after PgDn: expected pageOffset = %d, got %d", tui.BenchmarkPageSize, afterOffset)
	}
}

// TestBenchmarkPgUpDecreasesPageOffset verifies PgUp moves pageOffset backward without underflow.
func TestBenchmarkPgUpDecreasesPageOffset(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(20)})

	// Simulate two PgDn presses to get pageOffset = 2*pageSize.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkPageOffset(m) != 2*tui.BenchmarkPageSize {
		t.Fatalf("setup: expected pageOffset = %d, got %d", 2*tui.BenchmarkPageSize, tui.GetBenchmarkPageOffset(m))
	}

	// One PgUp should subtract one page.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkPageOffset(m) != tui.BenchmarkPageSize {
		t.Errorf("after PgUp: expected pageOffset = %d, got %d", tui.BenchmarkPageSize, tui.GetBenchmarkPageOffset(m))
	}

	// Another PgUp should go to 0, not negative.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkPageOffset(m) != 0 {
		t.Errorf("after second PgUp: expected pageOffset = 0, got %d", tui.GetBenchmarkPageOffset(m))
	}

	// A third PgUp should stay at 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkPageOffset(m) != 0 {
		t.Errorf("after third PgUp from 0: expected pageOffset = 0, got %d", tui.GetBenchmarkPageOffset(m))
	}
}

// TestBenchmarkCursorMovesWithinPage verifies up/down move within the current page.
func TestBenchmarkCursorMovesWithinPage(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(5)})

	// Cursor starts at 0.
	if tui.GetBenchmarkCursor(m) != 0 {
		t.Fatalf("expected initial cursor = 0, got %d", tui.GetBenchmarkCursor(m))
	}

	// Down moves cursor to 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetBenchmarkCursor(m) != 1 {
		t.Errorf("after down: expected cursor = 1, got %d", tui.GetBenchmarkCursor(m))
	}

	// Up moves cursor back to 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetBenchmarkCursor(m) != 0 {
		t.Errorf("after up: expected cursor = 0, got %d", tui.GetBenchmarkCursor(m))
	}

	// Up at 0 should not go negative.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetBenchmarkCursor(m) != 0 {
		t.Errorf("after up at 0: expected cursor = 0, got %d", tui.GetBenchmarkCursor(m))
	}
}

// TestBenchmarkDetailFreezeOnEnter verifies Enter freezes the detail panel.
func TestBenchmarkDetailFreezeOnEnter(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := makeRuns(3)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: runs})

	// Initially the detail is not frozen.
	if tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to not be frozen initially")
	}

	// Move cursor down then press Enter to freeze.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to be frozen after Enter")
	}

	// The frozen run should match runs[1] (cursor was at 1).
	frozen := tui.GetBenchmarkFrozenRun(m)
	frozenRun, ok := frozen.(store.BenchmarkRun)
	if !ok {
		t.Fatalf("expected BenchmarkRun, got %T", frozen)
	}
	if frozenRun.AgentID != runs[1].AgentID {
		t.Errorf("frozen run AgentID = %q, want %q", frozenRun.AgentID, runs[1].AgentID)
	}

	// Background refresh should NOT change the detail panel content when frozen.
	// Inject new data simulating a refresh.
	newRuns := makeRuns(3)
	newRuns[1].Accuracy = 0.5 // change a field to ensure it would show differently
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: newRuns})

	// Detail must still be frozen.
	if !tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to remain frozen after background refresh")
	}
	// Frozen run must still be the original one.
	stillFrozen := tui.GetBenchmarkFrozenRun(m)
	stillFrozenRun := stillFrozen.(store.BenchmarkRun)
	if stillFrozenRun.Accuracy != runs[1].Accuracy {
		t.Errorf("frozen run accuracy changed after refresh: got %f, want %f",
			stillFrozenRun.Accuracy, runs[1].Accuracy)
	}
}

// TestBenchmarkDetailUnfreezeOnEsc verifies Esc unfreezes the detail panel.
func TestBenchmarkDetailUnfreezeOnEsc(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(3)})

	// Freeze.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to be frozen after Enter")
	}

	// Unfreeze with Esc.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to be unfrozen after Esc")
	}
}

// TestBenchmarkDetailUnfreezeOnNavigation verifies cursor movement unfreezes the detail.
func TestBenchmarkDetailUnfreezeOnNavigation(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(3)})

	// Freeze with Enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to be frozen")
	}

	// Moving the cursor should unfreeze.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to be unfrozen after cursor movement")
	}
}

// TestBenchmarkViewShowsDateAndTime verifies the Time column shows date+time not just date.
func TestBenchmarkViewShowsDateAndTime(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	// Use a fixed timestamp in the local timezone to avoid UTC conversion differences.
	ts := time.Date(2026, 3, 15, 14, 30, 0, 0, time.Local)
	m, _ = m.Update(tui.BenchmarkDataMsg{
		Runs: []store.BenchmarkRun{
			{
				AgentID:  "time-agent",
				Model:    "gpt-4",
				RunAt:    ts,
				Accuracy: 0.9,
				Verdict:  store.VerdictKeep,
			},
		},
	})

	view := m.View()
	// The Time column should show the date portion (YYYY-MM-DD).
	if !strings.Contains(view, "2026-03-15") {
		t.Errorf("expected date '2026-03-15' in view, got: %q", view)
	}
	// The Time column should show the hour portion (HH:MM) — time.Local is preserved.
	expectedTime := ts.Format("15:04")
	if !strings.Contains(view, expectedTime) {
		t.Errorf("expected time %q in view, got: %q", expectedTime, view)
	}
}

// TestBenchmarkPgDnResetsCursor verifies PgDn resets cursor to 0.
func TestBenchmarkPgDnResetsCursor(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(5)})

	// Move cursor to row 3.
	for i := 0; i < 3; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	if tui.GetBenchmarkCursor(m) != 3 {
		t.Fatalf("expected cursor = 3, got %d", tui.GetBenchmarkCursor(m))
	}

	// PgDn should reset cursor to 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkCursor(m) != 0 {
		t.Errorf("after PgDn: expected cursor = 0, got %d", tui.GetBenchmarkCursor(m))
	}
}

// TestBenchmarkViewFrozenDetailNotAffectedByPageChange verifies that the frozen detail
// does not change when navigating to a different page.
func TestBenchmarkViewFrozenDetailNotAffectedByPageChange(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := makeRuns(5)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: runs})

	// Move to row 2 and freeze.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	frozenID := tui.GetBenchmarkFrozenRun(m).(store.BenchmarkRun).AgentID

	// Simulate a page change message arriving (PgDn sends fetchRuns but we inject BenchmarkDataMsg).
	newRuns := makeRuns(5)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: newRuns})

	// Detail must still be frozen with the original agent.
	if !tui.GetBenchmarkDetailFrozen(m) {
		t.Fatal("expected detail to remain frozen after page data changed")
	}
	gotID := tui.GetBenchmarkFrozenRun(m).(store.BenchmarkRun).AgentID
	if gotID != frozenID {
		t.Errorf("frozen agent ID changed: got %q, want %q", gotID, frozenID)
	}
}

// TestTrendDirection verifies trendDirection handles all edge cases correctly.
func TestTrendDirection(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []string
		want     string
	}{
		{"switch to keep is improving", []string{"SWITCH", "KEEP"}, "↑ improving"},
		{"keep to switch is degrading", []string{"KEEP", "SWITCH"}, "↓ degrading"},
		{"keep to keep is stable", []string{"KEEP", "KEEP"}, "→ stable"},
		{"switch to insufficient_data is neutral", []string{"SWITCH", "INSUFFICIENT_DATA"}, "→ stable"},
		{"insufficient_data to keep is neutral", []string{"INSUFFICIENT_DATA", "KEEP"}, "→ stable"},
		{"empty slice is stable", []string{}, "→ stable"},
		{"single verdict is stable", []string{"KEEP"}, "→ stable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.TrendDirection(tc.verdicts)
			if got != tc.want {
				t.Errorf("TrendDirection(%v) = %q, want %q", tc.verdicts, got, tc.want)
			}
		})
	}
}

// ----- Tracking session grouping tests (Step 1) --------------------------------

// makeSessionSummaries builds N SessionSummary entries with distinct timestamps and session IDs.
func makeSessionSummaries(n int) []store.SessionSummary {
	sessions := make([]store.SessionSummary, n)
	base := time.Now()
	tokens := 100
	cost := 0.001
	for i := 0; i < n; i++ {
		sessions[i] = store.SessionSummary{
			SessionID:        fmt.Sprintf("sess-%02d", i),
			AgentID:          fmt.Sprintf("agent-%02d", i),
			Model:            "gpt-4",
			Timestamp:        base.Add(time.Duration(-i) * time.Hour),
			PromptTokens:     &tokens,
			CompletionTokens: &tokens,
			CostUSD:          &cost,
		}
	}
	return sessions
}

// TestTrackingPageSizeIs20 verifies the page-size constant is 20.
func TestTrackingPageSizeIs20(t *testing.T) {
	if tui.TrackingPageSize != 20 {
		t.Errorf("expected TrackingPageSize == 20, got %d", tui.TrackingPageSize)
	}
}

// TestTrackingViewRendersCollapsedSessions verifies that injecting sessions renders
// one collapsed row per session (Type column shows "complete").
func TestTrackingViewRendersCollapsedSessions(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(5)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	view := m.View()
	for _, s := range sessions {
		if !strings.Contains(view, s.AgentID) {
			t.Errorf("expected session agent %q in collapsed view", s.AgentID)
		}
	}
	// All collapsed rows should show "complete" as the type column.
	if !strings.Contains(view, "complete") {
		t.Errorf("expected 'complete' type in collapsed rows")
	}
}

// TestTrackingCollapsedRowShowsPlusPrefix verifies the "+" prefix on collapsed rows.
func TestTrackingCollapsedRowShowsPlusPrefix(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: makeSessionSummaries(3)})

	view := m.View()
	if !strings.Contains(view, "+") {
		t.Errorf("expected '+' prefix on collapsed session rows, got: %q", view)
	}
}

// TestTrackingSessionExpandToggle verifies Space expands a collapsed session and
// a second Space collapses it again.
func TestTrackingSessionExpandToggle(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Initially collapsed.
	if tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session to be collapsed initially")
	}

	// Press Space → should expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session to be expanded after Space")
	}

	// Press Space again → should collapse.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session to be collapsed after second Space")
	}
}

// TestTrackingEnterAlsoTogglesExpand verifies Enter also expands/collapses.
func TestTrackingEnterAlsoTogglesExpand(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(2)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Press Enter → should expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session to be expanded after Enter")
	}
}

// TestTrackingExpandedRowsAppearInView verifies that after expanding, the session's
// events appear in the view.
func TestTrackingExpandedRowsAppearInView(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Expand (no es store → events arrive via msg injection).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})

	// Inject events for the session (simulating fetchSessionEvents result).
	evts := []store.Event{
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "start", Model: "gpt-4", Timestamp: time.Now().Add(-2 * time.Minute)},
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "tool_call", Model: "gpt-4", Timestamp: time.Now().Add(-1 * time.Minute)},
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "complete", Model: "gpt-4", Timestamp: time.Now()},
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	view := m.View()
	if !strings.Contains(view, "start") {
		t.Errorf("expected 'start' event in expanded view, got: %q", view)
	}
	if !strings.Contains(view, "tool_call") {
		t.Errorf("expected 'tool_call' event in expanded view, got: %q", view)
	}
}

// TestTrackingExpandedShowsMinusPrefixOnHeader verifies the "-" prefix when expanded.
func TestTrackingExpandedShowsMinusPrefixOnHeader(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	// Inject empty events so it won't be in loading state.
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: []store.Event{}})

	view := m.View()
	if !strings.Contains(view, "-") {
		t.Errorf("expected '-' prefix on expanded session header, got: %q", view)
	}
}

// TestTrackingPgDnIncreasesPageOffset verifies PgDn moves pageOffset forward.
func TestTrackingPgDnIncreasesPageOffset(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: makeSessionSummaries(20)})

	if tui.GetTrackingPageOffset(m) != 0 {
		t.Fatalf("expected initial pageOffset = 0, got %d", tui.GetTrackingPageOffset(m))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetTrackingPageOffset(m) != tui.TrackingPageSize {
		t.Errorf("after PgDn: expected pageOffset = %d, got %d", tui.TrackingPageSize, tui.GetTrackingPageOffset(m))
	}
}

// TestTrackingPgUpDecreasesPageOffset verifies PgUp moves pageOffset backward without underflow.
func TestTrackingPgUpDecreasesPageOffset(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: makeSessionSummaries(20)})

	// Two PgDn to get offset = 2 * pageSize.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetTrackingPageOffset(m) != 2*tui.TrackingPageSize {
		t.Fatalf("setup: expected pageOffset = %d", 2*tui.TrackingPageSize)
	}

	// One PgUp → subtract one page.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetTrackingPageOffset(m) != tui.TrackingPageSize {
		t.Errorf("after PgUp: expected pageOffset = %d, got %d", tui.TrackingPageSize, tui.GetTrackingPageOffset(m))
	}

	// Another PgUp → 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetTrackingPageOffset(m) != 0 {
		t.Errorf("after second PgUp: expected pageOffset = 0, got %d", tui.GetTrackingPageOffset(m))
	}

	// Third PgUp → stays at 0, no underflow.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetTrackingPageOffset(m) != 0 {
		t.Errorf("after third PgUp from 0: expected pageOffset = 0, got %d", tui.GetTrackingPageOffset(m))
	}
}

// TestTrackingCursorMovesWithinSessions verifies up/down navigation across session rows.
func TestTrackingCursorMovesWithinSessions(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: makeSessionSummaries(5)})

	if tui.GetTrackingCursor(m) != 0 {
		t.Fatalf("expected initial cursor = 0, got %d", tui.GetTrackingCursor(m))
	}

	// Down → cursor 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetTrackingCursor(m) != 1 {
		t.Errorf("after down: expected cursor = 1, got %d", tui.GetTrackingCursor(m))
	}

	// Up → cursor 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetTrackingCursor(m) != 0 {
		t.Errorf("after up: expected cursor = 0, got %d", tui.GetTrackingCursor(m))
	}

	// Up at 0 → stays at 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetTrackingCursor(m) != 0 {
		t.Errorf("after up at 0: expected cursor = 0, got %d", tui.GetTrackingCursor(m))
	}
}

// TestTrackingRefreshPreservesExpandState verifies that a background refresh
// does not collapse an expanded session.
func TestTrackingRefreshPreservesExpandState(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Expand session 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session to be expanded")
	}

	// Simulate background refresh with same sessions.
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Session should remain expanded.
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("refresh collapsed the session unexpectedly")
	}
}

// TestTrackingAutoRefreshDoesNotRollbackExpandOnNewSessions verifies
// that a refresh with different sessions still retains expand state for
// sessions that remain in the new list.
func TestTrackingAutoRefreshDoesNotRollbackExpandOnNewSessions(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[1].SessionID

	// Move to session 1 and expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("expected session 1 to be expanded")
	}

	// Inject a refresh with 3 refreshed sessions (same IDs).
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Session 1 should still be expanded.
	if !tui.IsTrackingSessionExpanded(m, sid) {
		t.Fatal("refresh unexpectedly collapsed session 1")
	}
}
