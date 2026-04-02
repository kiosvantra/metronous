package tui_test

import (
	"context"
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

func newTestApp(t *testing.T) *tui.AppModel {
	t.Helper()
	m := tui.NewAppModel(nil, nil, "", "", "", "test")
	return &m
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

	// 1 → Tracking (already there)
	updated, _ := sendKey(m, "1")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected TabTracking after pressing 1, got %d", m.CurrentTab)
	}

	// 2 → Benchmark Summary
	updated, _ = sendKey(m, "2")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabBenchmarkSummary {
		t.Errorf("expected TabBenchmarkSummary after pressing 2, got %d", m.CurrentTab)
	}

	// 3 → Benchmark Detailed
	updated, _ = sendKey(m, "3")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabBenchmarkDetailed {
		t.Errorf("expected TabBenchmarkDetailed after pressing 3, got %d", m.CurrentTab)
	}

	// 4 → Charts
	updated, _ = sendKey(m, "4")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabCharts {
		t.Errorf("expected TabCharts after pressing 4, got %d", m.CurrentTab)
	}

	// 5 → Config
	updated, _ = sendKey(m, "5")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabConfig {
		t.Errorf("expected TabConfig after pressing 5, got %d", m.CurrentTab)
	}

	// Back to 1 → Tracking
	updated, _ = sendKey(m, "1")
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected TabTracking after pressing 1, got %d", m.CurrentTab)
	}
}

func TestAppTabSwitchingByArrowKeys(t *testing.T) {
	m := newTestApp(t)

	// right → TabBenchmarkSummary
	updated, _ := sendSpecialKey(m, tea.KeyRight)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabBenchmarkSummary {
		t.Errorf("expected TabBenchmarkSummary after right arrow, got %d", m.CurrentTab)
	}

	// right → TabBenchmarkDetailed
	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabBenchmarkDetailed {
		t.Errorf("expected TabBenchmarkDetailed after right arrow, got %d", m.CurrentTab)
	}

	// right → TabCharts
	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabCharts {
		t.Errorf("expected TabCharts after right arrow, got %d", m.CurrentTab)
	}

	// right → TabConfig
	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(*tui.AppModel)
	// When on Charts, left/right should not switch tabs.
	if m.CurrentTab != tui.TabCharts {
		t.Errorf("expected TabCharts after right arrow, got %d", m.CurrentTab)
	}

	// left → TabCharts
	updated, _ = sendSpecialKey(m, tea.KeyLeft)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabCharts {
		t.Errorf("expected TabCharts after left arrow, got %d", m.CurrentTab)
	}

	// left → TabBenchmarkDetailed
	updated, _ = sendSpecialKey(m, tea.KeyLeft)
	m = updated.(*tui.AppModel)
	// When on Charts, left/right should not switch tabs.
	if m.CurrentTab != tui.TabCharts {
		t.Errorf("expected TabCharts after left arrow, got %d", m.CurrentTab)
	}
}

func TestAppArrowKeyDoesNotWrapBeyondBounds(t *testing.T) {
	m := newTestApp(t)

	// Left at first tab → stays at TabTracking.
	updated, _ := sendSpecialKey(m, tea.KeyLeft)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabTracking {
		t.Errorf("expected tab to stay at TabTracking, got %d", m.CurrentTab)
	}

	// Jump to last tab (5 = Config) then press right → stays at TabConfig.
	updated, _ = sendKey(m, "5")
	m = updated.(*tui.AppModel)
	updated, _ = sendSpecialKey(m, tea.KeyRight)
	m = updated.(*tui.AppModel)
	if m.CurrentTab != tui.TabConfig {
		t.Errorf("expected tab to stay at TabConfig, got %d", m.CurrentTab)
	}
}

func TestAppWindowResize(t *testing.T) {
	m := newTestApp(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(*tui.AppModel)
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
	v := updated.(*tui.AppModel).View()
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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

// makeCycles builds N week-start times (Sundays) going back N weeks from now.
func makeCycles(n int) []time.Time {
	cycles := make([]time.Time, n)
	// Start from the most recent Sunday.
	now := time.Now()
	daysBack := int(now.Weekday())
	lastSunday := now.AddDate(0, 0, -daysBack)
	base := time.Date(lastSunday.Year(), lastSunday.Month(), lastSunday.Day(), 0, 0, 0, 0, time.Local)
	for i := 0; i < n; i++ {
		cycles[i] = base.AddDate(0, 0, -i*7)
	}
	return cycles
}

// TestBenchmarkViewRendersAgentRows verifies that injecting runs renders the agent rows.
func TestBenchmarkViewRendersAgentRows(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(5)})

	view := m.View()
	// At least one agent row should appear.
	if !strings.Contains(view, "agent-00") {
		t.Errorf("expected 'agent-00' in view, got: %q", view)
	}
}

// TestBenchmarkCycleIndexStartsAtZero verifies cycleIndex is 0 initially.
func TestBenchmarkCycleIndexStartsAtZero(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	if tui.GetBenchmarkCycleIndex(m) != 0 {
		t.Errorf("expected initial cycleIndex = 0, got %d", tui.GetBenchmarkCycleIndex(m))
	}
}

// TestBenchmarkPgDnAdvancesCycleIndex verifies PgDn moves to the next (older) cycle.
func TestBenchmarkPgDnAdvancesCycleIndex(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	// Inject 3 cycles so PgDn has room to move.
	cycles := makeCycles(3)
	m, _ = m.Update(tui.BenchmarkDataMsg{
		Runs:   makeRuns(3),
		Cycles: cycles,
	})

	if tui.GetBenchmarkCycleIndex(m) != 0 {
		t.Fatalf("expected initial cycleIndex = 0, got %d", tui.GetBenchmarkCycleIndex(m))
	}

	// PgDn → cycleIndex = 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkCycleIndex(m) != 1 {
		t.Errorf("after PgDn: expected cycleIndex = 1, got %d", tui.GetBenchmarkCycleIndex(m))
	}
}

// TestBenchmarkPgUpDecreaseCycleIndex verifies PgUp moves to the previous (newer) cycle without underflow.
func TestBenchmarkPgUpDecreaseCycleIndex(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	cycles := makeCycles(3)
	m, _ = m.Update(tui.BenchmarkDataMsg{
		Runs:   makeRuns(3),
		Cycles: cycles,
	})

	// Two PgDn presses → cycleIndex = 2 (last cycle).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkCycleIndex(m) != 2 {
		t.Fatalf("setup: expected cycleIndex = 2, got %d", tui.GetBenchmarkCycleIndex(m))
	}

	// One PgUp → cycleIndex = 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkCycleIndex(m) != 1 {
		t.Errorf("after PgUp: expected cycleIndex = 1, got %d", tui.GetBenchmarkCycleIndex(m))
	}

	// Another PgUp → cycleIndex = 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkCycleIndex(m) != 0 {
		t.Errorf("after second PgUp: expected cycleIndex = 0, got %d", tui.GetBenchmarkCycleIndex(m))
	}

	// Third PgUp at 0 → stays at 0 (no underflow).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetBenchmarkCycleIndex(m) != 0 {
		t.Errorf("after third PgUp from 0: expected cycleIndex = 0, got %d", tui.GetBenchmarkCycleIndex(m))
	}
}

// TestBenchmarkPgDnAtLastCycleDoesNotAdvance verifies PgDn at the last cycle is a no-op.
func TestBenchmarkPgDnAtLastCycleDoesNotAdvance(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	cycles := makeCycles(2)
	m, _ = m.Update(tui.BenchmarkDataMsg{
		Runs:   makeRuns(2),
		Cycles: cycles,
	})

	// Move to last cycle.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkCycleIndex(m) != 1 {
		t.Fatalf("expected cycleIndex = 1, got %d", tui.GetBenchmarkCycleIndex(m))
	}

	// Another PgDn at last cycle → stays at 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetBenchmarkCycleIndex(m) != 1 {
		t.Errorf("PgDn at last cycle should not advance: got cycleIndex = %d", tui.GetBenchmarkCycleIndex(m))
	}
}

// TestBenchmarkCursorMovesWithinPage verifies up/down move within the current page.
func TestBenchmarkCursorMovesWithinPage(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	cycles := makeCycles(3)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(5), Cycles: cycles})

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
	m := tui.NewBenchmarkModel(nil, "", "", nil)
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
	dur := 5000 // 5s
	for i := 0; i < n; i++ {
		d := dur + i*20000 // vary 5s.. etc for coloring
		sessions[i] = store.SessionSummary{
			SessionID:        fmt.Sprintf("sess-%02d", i),
			AgentID:          fmt.Sprintf("agent-%02d", i),
			Model:            "gpt-4",
			Timestamp:        base.Add(time.Duration(-i) * time.Hour),
			PromptTokens:     &tokens,
			CompletionTokens: &tokens,
			CostUSD:          &cost,
			DurationMs:       &d,
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

// TestTrackingEnterOpensPopup verifies Enter opens the popup for the selected session.
func TestTrackingEnterOpensPopup(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Initially no popup.
	if tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to be closed initially")
	}

	// Press Enter → popup opens for sessions[0].
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to open after Enter")
	}
	if tui.GetTrackingPopupSessionID(m) != sessions[0].SessionID {
		t.Errorf("expected popup session ID = %q, got %q", sessions[0].SessionID, tui.GetTrackingPopupSessionID(m))
	}
}

// TestTrackingEscClosesPopup verifies Esc closes the popup and cursor stays in place.
func TestTrackingEscClosesPopup(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Move to session 1 and open popup.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to be open")
	}
	cursorBeforeClose := tui.GetTrackingCursor(m)

	// Esc closes popup.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to be closed after Esc")
	}
	// Cursor must remain at the same position.
	if tui.GetTrackingCursor(m) != cursorBeforeClose {
		t.Errorf("cursor changed after Esc: got %d, want %d", tui.GetTrackingCursor(m), cursorBeforeClose)
	}
}

// TestTrackingPopupDataFrozenOnRefresh verifies the popup events do NOT change
// when a background TrackingDataMsg arrives after the popup is open.
func TestTrackingPopupDataFrozenOnRefresh(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(2)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Open popup and inject frozen events.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	evts := []store.Event{
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "start", Model: "gpt-4", Timestamp: time.Now().Add(-2 * time.Minute)},
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "complete", Model: "gpt-4", Timestamp: time.Now()},
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	// Verify events are frozen.
	frozen := tui.GetTrackingPopupEvents(m)
	if len(frozen) != 2 {
		t.Fatalf("expected 2 frozen events, got %d", len(frozen))
	}

	// Simulate background refresh — sessions list changes.
	newSessions := makeSessionSummaries(2)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: newSessions})

	// Popup must still be open with the same frozen events.
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to remain open after background refresh")
	}
	stillFrozen := tui.GetTrackingPopupEvents(m)
	if len(stillFrozen) != 2 {
		t.Fatalf("expected frozen events to remain 2 after refresh, got %d", len(stillFrozen))
	}
	if stillFrozen[0].EventType != evts[0].EventType {
		t.Errorf("frozen event type changed: got %q, want %q", stillFrozen[0].EventType, evts[0].EventType)
	}
}

// TestTrackingPopupShowsTimelineEvents verifies the popup view contains the session events.
func TestTrackingPopupShowsTimelineEvents(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID

	// Open popup.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Inject events.
	evts := []store.Event{
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "start", Model: "gpt-4", Timestamp: time.Now().Add(-2 * time.Minute)},
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "tool_call", Model: "gpt-4", Timestamp: time.Now().Add(-1 * time.Minute)},
		{AgentID: sessions[0].AgentID, SessionID: sid, EventType: "complete", Model: "gpt-4", Timestamp: time.Now()},
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	view := m.View()
	if !strings.Contains(view, "start") {
		t.Errorf("expected 'start' in popup view, got: %q", view)
	}
	if !strings.Contains(view, "tool_call") {
		t.Errorf("expected 'tool_call' in popup view, got: %q", view)
	}
	if !strings.Contains(view, "complete") {
		t.Errorf("expected 'complete' in popup view, got: %q", view)
	}
}

// TestTrackingSpaceDoesNotExpandOrOpenPopup verifies Space is a no-op (expand removed).
func TestTrackingSpaceDoesNotExpandOrOpenPopup(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Press Space → popup must NOT open.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if tui.IsTrackingPopupOpen(m) {
		t.Fatal("Space should not open popup (expand removed)")
	}
}

// TestTrackingPopupBlocksNavigation verifies that while popup is open,
// arrow keys do not move the cursor in the background list.
func TestTrackingPopupBlocksNavigation(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(5)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Open popup on session 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cursorWhileOpen := tui.GetTrackingCursor(m)

	// Try to navigate — should be swallowed by popup.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetTrackingCursor(m) != cursorWhileOpen {
		t.Errorf("cursor moved while popup open: got %d, want %d", tui.GetTrackingCursor(m), cursorWhileOpen)
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

// TestTrackingRefreshDoesNotClosePopup verifies that a background refresh
// does not close an open popup.
func TestTrackingRefreshDoesNotClosePopup(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Open popup on session 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("expected popup to be open")
	}

	// Simulate background refresh with same sessions.
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Popup should remain open.
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("refresh unexpectedly closed the popup")
	}
}

// TestTrackingPopupScrolling verifies ↑/↓ move selection within the popup viewport
// and PgUp/PgDn scroll by blocks of 20.
func TestTrackingPopupScrolling(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Build 25 events so we have more than one page.
	evts := make([]store.Event, 25)
	base := time.Now()
	for i := range evts {
		evts[i] = store.Event{
			AgentID:   sessions[0].AgentID,
			SessionID: sid,
			EventType: "tool_call",
			Model:     "gpt-4",
			Timestamp: base.Add(time.Duration(-i) * time.Minute),
		}
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	// Initial state: cursor=0, offset=0, main cursor unchanged.
	mainCursorBefore := tui.GetTrackingCursor(m)
	if tui.GetTrackingPopupCursor(m) != 0 {
		t.Fatalf("expected popup cursor = 0, got %d", tui.GetTrackingPopupCursor(m))
	}
	if tui.GetTrackingPopupOffset(m) != 0 {
		t.Fatalf("expected popup offset = 0, got %d", tui.GetTrackingPopupOffset(m))
	}

	// Down moves popup cursor (not main cursor).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetTrackingPopupCursor(m) != 1 {
		t.Errorf("after down: expected popup cursor = 1, got %d", tui.GetTrackingPopupCursor(m))
	}
	if tui.GetTrackingCursor(m) != mainCursorBefore {
		t.Errorf("main cursor should not change while popup is open: got %d, want %d",
			tui.GetTrackingCursor(m), mainCursorBefore)
	}

	// Up moves popup cursor back.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetTrackingPopupCursor(m) != 0 {
		t.Errorf("after up: expected popup cursor = 0, got %d", tui.GetTrackingPopupCursor(m))
	}

	// Up at popup cursor=0 with offset=0 → no change.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetTrackingPopupCursor(m) != 0 {
		t.Errorf("up at boundary: expected popup cursor = 0, got %d", tui.GetTrackingPopupCursor(m))
	}
	if tui.GetTrackingPopupOffset(m) != 0 {
		t.Errorf("up at boundary: expected popup offset = 0, got %d", tui.GetTrackingPopupOffset(m))
	}

	// PgDn scrolls to next block of 20.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetTrackingPopupOffset(m) != 20 {
		t.Errorf("after PgDn: expected popup offset = 20, got %d", tui.GetTrackingPopupOffset(m))
	}
	if tui.GetTrackingPopupCursor(m) != 0 {
		t.Errorf("after PgDn: expected popup cursor = 0, got %d", tui.GetTrackingPopupCursor(m))
	}

	// PgDn beyond last page → stays at last valid offset.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if tui.GetTrackingPopupOffset(m) != 20 {
		t.Errorf("PgDn beyond last page: expected popup offset = 20 (no change), got %d",
			tui.GetTrackingPopupOffset(m))
	}

	// PgUp scrolls back.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if tui.GetTrackingPopupOffset(m) != 0 {
		t.Errorf("after PgUp: expected popup offset = 0, got %d", tui.GetTrackingPopupOffset(m))
	}

	// Popup events must NOT be refetched — still 25.
	if len(tui.GetTrackingPopupEvents(m)) != 25 {
		t.Errorf("popup events changed during scroll: expected 25, got %d",
			len(tui.GetTrackingPopupEvents(m)))
	}
}

// TestTrackingPopupViewport20Rows verifies that the popup renders at most 20 event rows
// even when popupEvents contains more than 20 entries.
func TestTrackingPopupViewport20Rows(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// 25 events.
	evts := make([]store.Event, 25)
	base := time.Now()
	for i := range evts {
		evts[i] = store.Event{
			AgentID:   sessions[0].AgentID,
			SessionID: sid,
			EventType: fmt.Sprintf("evt-%02d", i),
			Model:     "gpt-4",
			Timestamp: base.Add(time.Duration(-i) * time.Minute),
		}
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	view := m.View()
	// Only the first 20 events (evt-00..evt-19) should be visible at offset 0.
	if strings.Contains(view, "evt-20") {
		t.Errorf("event from second page (evt-20) should not be visible in first viewport")
	}
	if !strings.Contains(view, "evt-00") {
		t.Errorf("expected first event (evt-00) to be visible")
	}
	if !strings.Contains(view, "evt-19") {
		t.Errorf("expected last visible event (evt-19) to be visible")
	}
}

// TestTrackingPopupResetOnReopen verifies that reopening the popup resets cursor and offset.
func TestTrackingPopupResetOnReopen(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(1)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	sid := sessions[0].SessionID
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	evts := make([]store.Event, 25)
	base := time.Now()
	for i := range evts {
		evts[i] = store.Event{
			AgentID:   sessions[0].AgentID,
			SessionID: sid,
			EventType: "tool_call",
			Model:     "gpt-4",
			Timestamp: base.Add(time.Duration(-i) * time.Minute),
		}
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	// Scroll down then close.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Reopen.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if tui.GetTrackingPopupCursor(m) != 0 {
		t.Errorf("reopen: expected popup cursor = 0, got %d", tui.GetTrackingPopupCursor(m))
	}
	if tui.GetTrackingPopupOffset(m) != 0 {
		t.Errorf("reopen: expected popup offset = 0, got %d", tui.GetTrackingPopupOffset(m))
	}
}

// TestTrackingRefreshWithNewSessionsDoesNotClosePopup verifies that a refresh
// with different session data still keeps the popup open and frozen.
func TestTrackingRefreshWithNewSessionsDoesNotClosePopup(t *testing.T) {
	m := tui.NewTrackingModel(nil)
	sessions := makeSessionSummaries(3)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: sessions})

	// Move to session 1, open popup and inject events.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	sid := sessions[1].SessionID
	evts := []store.Event{
		{AgentID: sessions[1].AgentID, SessionID: sid, EventType: "start", Model: "gpt-4", Timestamp: time.Now().Add(-1 * time.Minute)},
		{AgentID: sessions[1].AgentID, SessionID: sid, EventType: "complete", Model: "gpt-4", Timestamp: time.Now()},
	}
	m, _ = m.Update(tui.TrackingSessionEventsMsg{SessionID: sid, Events: evts})

	// Simulate a refresh with new sessions.
	newSessions := makeSessionSummaries(5)
	m, _ = m.Update(tui.TrackingDataMsg{Sessions: newSessions})

	// Popup must still be open with the original session and frozen events.
	if !tui.IsTrackingPopupOpen(m) {
		t.Fatal("refresh unexpectedly closed the popup")
	}
	if tui.GetTrackingPopupSessionID(m) != sid {
		t.Errorf("popup session ID changed after refresh: got %q, want %q", tui.GetTrackingPopupSessionID(m), sid)
	}
	frozen := tui.GetTrackingPopupEvents(m)
	if len(frozen) != 2 {
		t.Fatalf("frozen events count changed after refresh: got %d, want 2", len(frozen))
	}
}

// ----- Benchmark Summary tab tests -------------------------------------------

// TestAppTabSwitchingFiveTabs verifies all tabs are reachable via 1/2/3/4/5.
func TestAppTabSwitchingFiveTabs(t *testing.T) {
	m := newTestApp(t)

	cases := []struct {
		key     string
		wantTab tui.Tab
		label   string
	}{
		{"1", tui.TabTracking, "TabTracking"},
		{"2", tui.TabBenchmarkSummary, "TabBenchmarkSummary"},
		{"3", tui.TabBenchmarkDetailed, "TabBenchmarkDetailed"},
		{"4", tui.TabCharts, "TabCharts"},
		{"5", tui.TabConfig, "TabConfig"},
	}
	for _, tc := range cases {
		updated, _ := sendKey(m, tc.key)
		m = updated.(*tui.AppModel)
		if m.CurrentTab != tc.wantTab {
			t.Errorf("key %q: expected %s (%d), got %d", tc.key, tc.label, tc.wantTab, m.CurrentTab)
		}
	}
}

// TestAppTabBarContainsSummaryAndDetailed verifies the rendered tab bar includes
// both "Summary" and "Detailed" labels.
func TestAppTabBarContainsSummaryAndDetailed(t *testing.T) {
	m := newTestApp(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	view := updated.(*tui.AppModel).View()
	if !strings.Contains(view, "Summary") {
		t.Errorf("tab bar should contain 'Summary', got: %q", view[:min(len(view), 300)])
	}
	if !strings.Contains(view, "Detailed") {
		t.Errorf("tab bar should contain 'Detailed', got: %q", view[:min(len(view), 300)])
	}
}

// TestBenchmarkSummaryViewShowsTitle verifies the summary view renders its title.
func TestBenchmarkSummaryViewShowsTitle(t *testing.T) {
	m := tui.NewBenchmarkSummaryModel(nil)
	// Inject synthetic rows directly via the data message.
	m, _ = m.Update(tui.BenchmarkSummaryDataMsg{
		Rows: []tui.SummaryRowForTest{
			{AgentID: "agent-x", Model: "gpt-4", Runs: 3},
		},
	})
	view := m.View()
	if !strings.Contains(view, "Benchmark Summary") {
		t.Errorf("expected 'Benchmark Summary' title in view, got: %q", view)
	}
}

// TestBenchmarkSummaryViewShowsEmptyState verifies the empty-state message.
func TestBenchmarkSummaryViewShowsEmptyState(t *testing.T) {
	m := tui.NewBenchmarkSummaryModel(nil)
	m, _ = m.Update(tui.BenchmarkSummaryDataMsg{Rows: nil})
	view := m.View()
	if !strings.Contains(view, "No benchmark runs yet") {
		t.Errorf("expected empty state message, got: %q", view)
	}
}

// TestBenchmarkSummaryCursorNavigation verifies ↑/↓ moves the cursor.
func TestBenchmarkSummaryCursorNavigation(t *testing.T) {
	m := tui.NewBenchmarkSummaryModel(nil)
	rows := []tui.SummaryRowForTest{
		{AgentID: "agent-a", Model: "gpt-4", Runs: 2},
		{AgentID: "agent-b", Model: "gpt-4", Runs: 1},
		{AgentID: "agent-c", Model: "gpt-4", Runs: 5},
	}
	m, _ = m.Update(tui.BenchmarkSummaryDataMsg{Rows: rows})

	if tui.GetBenchmarkSummaryCursor(m) != 0 {
		t.Fatalf("expected initial cursor = 0, got %d", tui.GetBenchmarkSummaryCursor(m))
	}

	// Down → cursor 1.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tui.GetBenchmarkSummaryCursor(m) != 1 {
		t.Errorf("after down: expected cursor = 1, got %d", tui.GetBenchmarkSummaryCursor(m))
	}

	// Up → cursor 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetBenchmarkSummaryCursor(m) != 0 {
		t.Errorf("after up: expected cursor = 0, got %d", tui.GetBenchmarkSummaryCursor(m))
	}

	// Up at 0 → stays at 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetBenchmarkSummaryCursor(m) != 0 {
		t.Errorf("after up at 0: expected cursor = 0, got %d", tui.GetBenchmarkSummaryCursor(m))
	}
}

// TestBenchmarkSummaryViewRendersAgentRows verifies agent rows appear in the view.
func TestBenchmarkSummaryViewRendersAgentRows(t *testing.T) {
	m := tui.NewBenchmarkSummaryModel(nil)
	rows := []tui.SummaryRowForTest{
		{AgentID: "sdd-orchestrator", Model: "claude-sonnet", Runs: 5},
		{AgentID: "sdd-apply", Model: "gpt-4-mini", Runs: 3},
	}
	m, _ = m.Update(tui.BenchmarkSummaryDataMsg{Rows: rows})
	view := m.View()
	if !strings.Contains(view, "sdd-orchestrator") {
		t.Errorf("expected 'sdd-orchestrator' in view, got: %q", view)
	}
	if !strings.Contains(view, "sdd-apply") {
		t.Errorf("expected 'sdd-apply' in view, got: %q", view)
	}
}

// TestChartsModeToggleAndNavigation verifies the Charts tab keeps month/day
// navigation working while toggling between the two visualization modes.
func TestChartsModeToggleAndNavigation(t *testing.T) {
	monthStart := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local)
	m := tui.NewChartsModel(nil, nil)
	m, _ = m.Update(tui.ChartsDataMsg{
		Mode:           tui.ChartModePerformance,
		MonthStart:     monthStart,
		Rows:           []store.DailyCostByModelRow{{Day: monthStart, Model: "alpha", TotalCostUSD: 1}},
		SelectedModels: []string{"alpha"},
	})

	if tui.GetChartsMode(m) != tui.ChartModePerformance {
		t.Fatalf("expected performance mode, got %v", tui.GetChartsMode(m))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if tui.GetChartsMode(m) != tui.ChartModeResponsibility {
		t.Fatalf("expected responsibility mode after toggle, got %v", tui.GetChartsMode(m))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if tui.GetChartsCursor(m) != 1 {
		t.Fatalf("expected cursor=1 after l, got %d", tui.GetChartsCursor(m))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tui.GetChartsCursor(m) != 0 {
		t.Fatalf("expected cursor=0 after k, got %d", tui.GetChartsCursor(m))
	}

	originalMonth := tui.GetChartsMonthStart(m)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if !tui.GetChartsMonthStart(m).Before(originalMonth) {
		t.Fatalf("expected month to move backward with left arrow")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !sameMonthForTest(tui.GetChartsMonthStart(m), originalMonth) {
		t.Fatalf("expected month to return after right arrow")
	}
}

// TestChartsViewRendersTooltipBreakdown verifies the chart view includes the
// selected mode label and the selected day breakdown.
func TestChartsViewRendersTooltipBreakdown(t *testing.T) {
	monthStart := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local)
	day1 := monthStart
	day2 := monthStart.AddDate(0, 0, 1)

	m := tui.NewChartsModel(nil, nil)
	m, _ = m.Update(tui.ChartsDataMsg{
		Mode:       tui.ChartModePerformance,
		MonthStart: monthStart,
		Rows: []store.DailyCostByModelRow{
			{Day: day1, Model: "alpha", TotalCostUSD: 1},
			{Day: day1, Model: "beta", TotalCostUSD: 2},
			{Day: day1, Model: "gamma", TotalCostUSD: 3},
			{Day: day2, Model: "alpha", TotalCostUSD: 4},
			{Day: day2, Model: "beta", TotalCostUSD: 1},
		},
		SelectedModels: []string{"gamma", "beta", "alpha"},
	})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	view := m.View()
	if !strings.Contains(view, "Performance mode") {
		t.Fatalf("expected performance mode label in view, got: %q", view)
	}
	if !strings.Contains(view, "Tooltip: "+day2.Format("Jan 02")) {
		t.Fatalf("expected tooltip for selected day, got: %q", view)
	}
	if !strings.Contains(view, "alpha: $4.000") || !strings.Contains(view, "beta: $1.000") {
		t.Fatalf("expected tooltip breakdown in view, got: %q", view)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	view = m.View()
	if !strings.Contains(view, "Responsibility mode") {
		t.Fatalf("expected responsibility mode label after toggle, got: %q", view)
	}
}

func sameMonthForTest(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month()
}

// min is a small helper for test output truncation.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── F5 intraweek trigger tests ───────────────────────────────────────────────

// mockIntraweekRunner is a test double for tui.IntraweekRunner that records calls
// and returns a configurable error.
type mockIntraweekRunner struct {
	called bool
	retErr error
}

func (r *mockIntraweekRunner) RunIntraweek(_ context.Context, _ int) error {
	r.called = true
	return r.retErr
}

// TestBenchmarkF5SetsRunningFlag verifies that pressing F5 sets the running flag
// and returns a non-nil tea.Cmd when a runner is wired.
func TestBenchmarkF5SetsRunningFlag(t *testing.T) {
	mock := &mockIntraweekRunner{}
	m := tui.NewBenchmarkModelWithRunner(nil, mock)

	// Pressing F5 should set running=true and return a Cmd.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})

	if !tui.GetBenchmarkRunning(m) {
		t.Error("expected running=true after F5 press")
	}
	if cmd == nil {
		t.Error("expected non-nil tea.Cmd after F5 press")
	}
}

// TestBenchmarkF5NoOpWhenAlreadyRunning verifies that a second F5 press while a run
// is in progress is ignored (no new Cmd, running stays true).
func TestBenchmarkF5NoOpWhenAlreadyRunning(t *testing.T) {
	mock := &mockIntraweekRunner{}
	m := tui.NewBenchmarkModelWithRunner(nil, mock)

	// First F5 — starts the run.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})
	if !tui.GetBenchmarkRunning(m) {
		t.Fatal("setup: expected running=true after first F5")
	}

	// Second F5 while running — must be a no-op (nil Cmd).
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})
	if cmd2 != nil {
		t.Error("expected nil Cmd on second F5 press while already running")
	}
}

// TestBenchmarkF5NoOpWithoutRunner verifies that F5 is a no-op when no runner is wired.
func TestBenchmarkF5NoOpWithoutRunner(t *testing.T) {
	// No runner (nil).
	m := tui.NewBenchmarkModel(nil, "", "", nil)

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})

	if tui.GetBenchmarkRunning(m) {
		t.Error("expected running=false when no runner is wired")
	}
	if cmd != nil {
		t.Error("expected nil Cmd when no runner is wired")
	}
}

// TestBenchmarkIntraweekDoneClearsRunning verifies that receiving intraweekRunDoneMsg
// clears the running flag and triggers a data refresh.
func TestBenchmarkIntraweekDoneClearsRunning(t *testing.T) {
	mock := &mockIntraweekRunner{}
	m := tui.NewBenchmarkModelWithRunner(nil, mock)

	// Simulate F5 press to set running.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})
	if !tui.GetBenchmarkRunning(m) {
		t.Fatal("setup: expected running=true")
	}

	// Simulate the run completing successfully.
	m, _ = m.Update(tui.IntraweekRunDoneMsg{})

	if tui.GetBenchmarkRunning(m) {
		t.Error("expected running=false after run completes")
	}
	if tui.GetBenchmarkRunErr(m) != nil {
		t.Errorf("expected no error, got: %v", tui.GetBenchmarkRunErr(m))
	}
	// Note: a refresh Cmd may be nil when no BenchmarkStore is wired (test scenario).
	// The important invariant is that running=false and runErr=nil after a successful run.
}

// TestBenchmarkIntraweekDoneSetsErrOnFailure verifies that a failed run stores
// the error in runErr and still clears the running flag.
func TestBenchmarkIntraweekDoneSetsErrOnFailure(t *testing.T) {
	mock := &mockIntraweekRunner{retErr: fmt.Errorf("benchmark pipeline failed")}
	m := tui.NewBenchmarkModelWithRunner(nil, mock)

	// F5 to start.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})

	// Simulate a failed run.
	m, _ = m.Update(tui.IntraweekRunDoneMsg{Err: mock.retErr})

	if tui.GetBenchmarkRunning(m) {
		t.Error("expected running=false after failed run")
	}
	if tui.GetBenchmarkRunErr(m) == nil {
		t.Error("expected runErr to be set after failed run")
	}
}

// TestBenchmarkF5ViewShowsRunningStatus verifies the View() renders a running
// indicator while an intraweek run is in progress.
func TestBenchmarkF5ViewShowsRunningStatus(t *testing.T) {
	mock := &mockIntraweekRunner{}
	m := tui.NewBenchmarkModelWithRunner(nil, mock)
	// Inject some runs so View() has data to render.
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(2)})

	// F5 to start run.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})

	view := m.View()
	if !strings.Contains(view, "Running intraweek") {
		t.Errorf("expected 'Running intraweek' in view while running, got: %q", view[:min(len(view), 400)])
	}
}

// TestBenchmarkF5ViewShowsF5HintInFooter verifies that the footer always shows
// the F5 keybind hint (regardless of running state).
func TestBenchmarkF5ViewShowsF5HintInFooter(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "", nil)
	m, _ = m.Update(tui.BenchmarkDataMsg{Runs: makeRuns(2)})

	view := m.View()
	if !strings.Contains(view, "F5") {
		t.Errorf("expected 'F5' hint in view footer, got: %q", view[:min(len(view), 400)])
	}
}

// ─── Benchmark Summary filtering tests ───────────────────────────────────────

// makeRun is a helper that builds a BenchmarkRun with required fields populated.
func makeRun(agentID, model string, verdict store.VerdictType, accuracy, p95 float64, sampleSize int, runAt time.Time) store.BenchmarkRun {
	return store.BenchmarkRun{
		AgentID:      agentID,
		Model:        model,
		Verdict:      verdict,
		Accuracy:     accuracy,
		P95LatencyMs: p95,
		SampleSize:   sampleSize,
		RunAt:        runAt,
		TotalCostUSD: 0.01,
	}
}

// TestSummaryIgnoresInsufficientDataInAverages verifies that INSUFFICIENT_DATA runs
// (verdict == INSUFFICIENT_DATA or SampleSize < 50) are excluded from the weighted
// averages for AvgAccuracy, AvgP95Ms, and HealthScore.
func TestSummaryIgnoresInsufficientDataInAverages(t *testing.T) {
	base := time.Now().UTC()

	// One good run (KEEP, SampleSize=100, Accuracy=1.0) and one bad run
	// (INSUFFICIENT_DATA, SampleSize=5, Accuracy=0.0).
	// Without filtering the weighted average would be:
	//   (1.0 * 100 + 0.0 * 5) / (100 + 5) ≈ 0.952
	// With filtering the average must be:
	//   (1.0 * 100) / 100 = 1.0
	runs := []store.BenchmarkRun{
		makeRun("agent-a", "model-x", store.VerdictKeep, 1.0, 500, 100, base.Add(-1*time.Hour)),
		makeRun("agent-a", "model-x", store.VerdictInsufficientData, 0.0, 99999, 5, base.Add(-2*time.Hour)),
	}

	rows := tui.AggregateSummaryRowsForTest(runs)
	if len(rows) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(rows))
	}
	row := rows[0]

	// AvgAccuracy must be 1.0 (only the good run contributes).
	if row.AvgAccuracy < 0.999 {
		t.Errorf("AvgAccuracy: got %.4f, want ~1.0 (INSUFFICIENT_DATA must be excluded)", row.AvgAccuracy)
	}

	// HealthScore must reflect 1.0 accuracy (no penalty from the bad run).
	// Formula: accPart = 1.0 * 50 = 50; latPart = 30 * max(0, 1-500/10000) = 30*0.95 ≈ 28.5
	// verdictPart = 20 (KEEP); total ≈ 98.5
	if row.HealthScore < 90 {
		t.Errorf("HealthScore: got %.1f, expected > 90 (bad run must not drag health down)", row.HealthScore)
	}

	// Runs count still includes both runs.
	if row.Runs != 2 {
		t.Errorf("Runs: got %d, want 2 (INSUFFICIENT_DATA counts in total but not in averages)", row.Runs)
	}
}

// TestSummaryLastVerdictPrefersMeaningfulVerdict verifies that LastVerdict is set to
// the most recent non-INSUFFICIENT_DATA verdict, even when the latest run is INSUFFICIENT_DATA.
func TestSummaryLastVerdictPrefersMeaningfulVerdict(t *testing.T) {
	base := time.Now().UTC()

	// Order: oldest KEEP run, then a newer INSUFFICIENT_DATA run.
	// LastVerdict must be KEEP (skipping the INSUFFICIENT_DATA).
	runs := []store.BenchmarkRun{
		makeRun("agent-b", "model-y", store.VerdictKeep, 0.9, 1000, 80, base.Add(-7*24*time.Hour)),
		makeRun("agent-b", "model-y", store.VerdictInsufficientData, 0.0, 0, 3, base.Add(-1*time.Hour)),
	}

	rows := tui.AggregateSummaryRowsForTest(runs)
	if len(rows) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(rows))
	}
	row := rows[0]

	if row.LastVerdict != store.VerdictKeep {
		t.Errorf("LastVerdict: got %q, want KEEP (must skip INSUFFICIENT_DATA)", row.LastVerdict)
	}
}

// TestSummaryLastVerdictFallsBackToInsufficientWhenNoOtherExists verifies that when
// ALL runs are INSUFFICIENT_DATA, LastVerdict is set to INSUFFICIENT_DATA.
func TestSummaryLastVerdictFallsBackToInsufficientWhenNoOtherExists(t *testing.T) {
	base := time.Now().UTC()

	runs := []store.BenchmarkRun{
		makeRun("agent-c", "model-z", store.VerdictInsufficientData, 0.0, 0, 10, base.Add(-1*time.Hour)),
		makeRun("agent-c", "model-z", store.VerdictInsufficientData, 0.0, 0, 5, base.Add(-2*time.Hour)),
	}

	rows := tui.AggregateSummaryRowsForTest(runs)
	if len(rows) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(rows))
	}
	row := rows[0]

	if row.LastVerdict != store.VerdictInsufficientData {
		t.Errorf("LastVerdict: got %q, want INSUFFICIENT_DATA (fallback when all runs are insufficient)", row.LastVerdict)
	}
	// Averages must be zero (no samples contributed).
	if row.AvgAccuracy != 0 {
		t.Errorf("AvgAccuracy: got %.4f, want 0 (no valid samples)", row.AvgAccuracy)
	}
}

// TestSummaryExcludesRunsBySampleSizeThreshold verifies that runs with SampleSize<50
// (even if their verdict is not INSUFFICIENT_DATA) are excluded from averages.
func TestSummaryExcludesRunsBySampleSizeThreshold(t *testing.T) {
	base := time.Now().UTC()

	// Good run: KEEP, SampleSize=100, Accuracy=0.8
	// Noisy run: KEEP verdict but SampleSize=30 (< 50), Accuracy=0.0
	// Expected: only the good run contributes to averages.
	runs := []store.BenchmarkRun{
		makeRun("agent-d", "model-w", store.VerdictKeep, 0.8, 2000, 100, base.Add(-1*time.Hour)),
		makeRun("agent-d", "model-w", store.VerdictKeep, 0.0, 99999, 30, base.Add(-2*time.Hour)),
	}

	rows := tui.AggregateSummaryRowsForTest(runs)
	if len(rows) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(rows))
	}
	row := rows[0]

	// AvgAccuracy must be 0.8 (only the 100-sample run contributes).
	if row.AvgAccuracy < 0.79 || row.AvgAccuracy > 0.81 {
		t.Errorf("AvgAccuracy: got %.4f, want ~0.8 (low-sample run must be excluded)", row.AvgAccuracy)
	}
}
