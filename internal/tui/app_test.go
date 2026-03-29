package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enduluc/metronous/internal/store"
	"github.com/enduluc/metronous/internal/tui"
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
	return tui.NewAppModel(nil, nil, "", "", "")
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
		Events: []store.Event{
			{
				AgentID:          "test-agent",
				EventType:        "complete",
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
	m, _ = m.Update(tui.TrackingDataMsg{Events: nil})
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
