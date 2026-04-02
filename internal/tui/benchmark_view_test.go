package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/tui"
)

// sampleBenchRun creates a BenchmarkRun with all fields set for testing.
func sampleBenchRun(agentID, model string, score float64, verdict store.VerdictType) store.BenchmarkRun {
	return store.BenchmarkRun{
		AgentID:         agentID,
		Model:           model,
		CompositeScore:  score,
		Accuracy:        0.92,
		P95LatencyMs:    2800,
		ToolSuccessRate: 0.95,
		TotalCostUSD:    0.30,
		ROIScore:        0.80,
		SampleSize:      100,
		Verdict:         verdict,
		RunAt:           time.Now().UTC(),
		WindowDays:      7,
	}
}

func buildKeyMsg(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func buildEscMsg() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEsc}
}

// updateBenchmark sends a message to BenchmarkModel and returns the updated model.
func updateBenchmark(m tui.BenchmarkModel, msg tea.Msg) tui.BenchmarkModel {
	updated, _ := m.Update(msg)
	return updated
}

// --- T08: Score column ---

// TestBenchmarkView_ScoreColumn_RendersValue verifies that a run with CompositeScore=0.87
// renders "0.87" in the table row.
func TestBenchmarkView_ScoreColumn_RendersValue(t *testing.T) {
	run := sampleBenchRun("test-agent", "claude-sonnet", 0.87, store.VerdictKeep)
	row := tui.FormatBenchmarkRowForTest(run, "primary", map[string]float64{})

	if len(row) <= tui.ScoreColIdx {
		t.Fatalf("expected at least %d columns, got %d", tui.ScoreColIdx+1, len(row))
	}
	scoreCell := row[tui.ScoreColIdx]
	if !strings.Contains(scoreCell, "0.87") {
		t.Errorf("Score column: got %q, want to contain '0.87'", scoreCell)
	}
}

// TestBenchmarkView_ScoreColumn_ZeroShowsDash verifies that CompositeScore=0 renders "—".
func TestBenchmarkView_ScoreColumn_ZeroShowsDash(t *testing.T) {
	run := sampleBenchRun("test-agent", "claude-sonnet", 0.0, store.VerdictKeep)
	row := tui.FormatBenchmarkRowForTest(run, "primary", map[string]float64{})

	scoreCell := row[tui.ScoreColIdx]
	if !strings.Contains(scoreCell, "—") {
		t.Errorf("Score column with zero: got %q, want '—'", scoreCell)
	}
}

// TestBenchmarkView_ScoreColumnLayout verifies Score is at index 3 and verdictColIdx is 6.
func TestBenchmarkView_ScoreColumnLayout(t *testing.T) {
	colNames := tui.BenchColNames()

	if len(colNames) < 7 {
		t.Fatalf("expected at least 7 columns, got %d: %v", len(colNames), colNames)
	}
	if colNames[tui.ScoreColIdx] != "Score" {
		t.Errorf("column[%d] = %q, want 'Score'", tui.ScoreColIdx, colNames[tui.ScoreColIdx])
	}
	if tui.VerdictColIdxForTest != 6 {
		t.Errorf("verdictColIdx = %d, want 6 (shifted by Score column insertion)", tui.VerdictColIdxForTest)
	}
}

// TestBenchmarkView_NoDataRow_ScoreShowsDash verifies NO_DATA placeholder rows show "-" for score.
func TestBenchmarkView_NoDataRow_ScoreShowsDash(t *testing.T) {
	run := store.BenchmarkRun{AgentID: "no-data-agent"}
	row := tui.FormatBenchmarkRowForTest(run, "primary", map[string]float64{})

	scoreCell := row[tui.ScoreColIdx]
	if scoreCell != "-" {
		t.Errorf("NO_DATA score cell: got %q, want '-'", scoreCell)
	}
}

// TestBenchmarkView_ExistingKeybinds_Unaffected verifies j/k navigation still works.
func TestBenchmarkView_ExistingKeybinds_Unaffected(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-a", "model-1", 0.85, store.VerdictKeep),
		sampleBenchRun("agent-b", "model-2", 0.72, store.VerdictSwitch),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})

	initialCursor := tui.GetBenchmarkCursor(m)

	m2 := updateBenchmark(m, buildKeyMsg("j"))
	if tui.GetBenchmarkCursor(m2) != initialCursor+1 {
		t.Errorf("after j: cursor = %d, want %d", tui.GetBenchmarkCursor(m2), initialCursor+1)
	}

	m3 := updateBenchmark(m2, buildKeyMsg("k"))
	if tui.GetBenchmarkCursor(m3) != initialCursor {
		t.Errorf("after k: cursor = %d, want %d", tui.GetBenchmarkCursor(m3), initialCursor)
	}
}

// --- T09: Comparison panel ---

// TestBenchmarkView_CompareKey_TwoModels_OpensPanel verifies that pressing 'c' with
// 2 models for the same agent opens the comparison panel.
func TestBenchmarkView_CompareKey_TwoModels_OpensPanel(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-x", "model-a", 0.91, store.VerdictKeep),
		sampleBenchRun("agent-x", "model-b", 0.75, store.VerdictKeep),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))

	if !tui.GetBenchmarkComparing(m) {
		t.Error("expected comparing=true after pressing 'c' with 2 models")
	}

	view := m.View()
	if !strings.Contains(view, "model-a") || !strings.Contains(view, "model-b") {
		t.Errorf("comparison panel should show both models, view=%q", view)
	}
}

// TestBenchmarkView_CompareKey_SingleModel_NoOp verifies that pressing 'c' with
// only 1 model for the agent sets comparing=false and shows status message.
func TestBenchmarkView_CompareKey_SingleModel_NoOp(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("solo-agent", "only-model", 0.85, store.VerdictKeep),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))

	if tui.GetBenchmarkComparing(m) {
		t.Error("expected comparing=false for single-model agent")
	}
	view := m.View()
	if !strings.Contains(view, "2+ models") {
		t.Errorf("expected status message about 2+ models, view=%q", view)
	}
}

// TestBenchmarkView_ComparePanel_Esc_Returns verifies that pressing Esc closes the comparison panel.
func TestBenchmarkView_ComparePanel_Esc_Returns(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-x", "model-a", 0.91, store.VerdictKeep),
		sampleBenchRun("agent-x", "model-b", 0.75, store.VerdictKeep),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))
	if !tui.GetBenchmarkComparing(m) {
		t.Fatal("comparison panel should be open")
	}

	m = updateBenchmark(m, buildEscMsg())
	if tui.GetBenchmarkComparing(m) {
		t.Error("expected comparing=false after Esc")
	}
}

// TestBenchmarkView_ComparePanel_RendersScores verifies the ranked panel renders score values.
func TestBenchmarkView_ComparePanel_RendersScores(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-x", "model-a", 0.91, store.VerdictKeep),
		sampleBenchRun("agent-x", "model-b", 0.75, store.VerdictKeep),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))

	view := m.View()
	if !strings.Contains(view, "0.91") {
		t.Errorf("panel should show top score 0.91, view=%q", view)
	}
	if !strings.Contains(view, "0.75") {
		t.Errorf("panel should show second score 0.75, view=%q", view)
	}
	// Visual bars should be present.
	if !strings.Contains(view, "█") {
		t.Error("panel should contain bar characters (█)")
	}
}

// TestBenchmarkView_ComparePanel_InsufficientDataWarning verifies that a warning
// appears when one of the compared runs has INSUFFICIENT_DATA verdict.
func TestBenchmarkView_ComparePanel_InsufficientDataWarning(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-x", "model-alpha", 0.0, store.VerdictInsufficientData),
		sampleBenchRun("agent-x", "model-beta", 0.78, store.VerdictKeep),
	}
	runs[0].SampleSize = 3
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))

	view := m.View()
	if !strings.Contains(strings.ToLower(view), "insufficient") {
		t.Errorf("comparison panel should warn about insufficient data, view=%q", view)
	}
}

// TestBenchmarkView_ComparePanel_ThreeModels_ShowsAll verifies that
// with 3 models, pressing 'c' shows ALL models ranked by composite score.
func TestBenchmarkView_ComparePanel_ThreeModels_ShowsAll(t *testing.T) {
	m := tui.NewBenchmarkModel(nil, "", "")
	runs := []store.BenchmarkRun{
		sampleBenchRun("agent-x", "model-low", 0.50, store.VerdictSwitch),
		sampleBenchRun("agent-x", "model-mid", 0.75, store.VerdictKeep),
		sampleBenchRun("agent-x", "model-high", 0.91, store.VerdictKeep),
	}
	m = updateBenchmark(m, tui.BenchmarkDataMsg{Runs: runs})
	m = updateBenchmark(m, buildKeyMsg("c"))

	if !tui.GetBenchmarkComparing(m) {
		t.Fatal("expected comparing=true with 3 models")
	}

	view := m.View()
	// The ranked panel should show the heading "Model Ranking:".
	panelIdx := strings.Index(view, "Model Ranking:")
	if panelIdx < 0 {
		t.Fatalf("ranked comparison panel not found in view")
	}
	panelView := view[panelIdx:]

	// All 3 models should appear in the ranked panel.
	if !strings.Contains(panelView, "model-high") {
		t.Errorf("panel should contain model-high (top score), panel=%q", panelView)
	}
	if !strings.Contains(panelView, "model-mid") {
		t.Errorf("panel should contain model-mid (second score), panel=%q", panelView)
	}
	if !strings.Contains(panelView, "model-low") {
		t.Errorf("panel should contain model-low (third score), panel=%q", panelView)
	}

	// Verify ranking order: #1 should appear before #2 before #3.
	idx1 := strings.Index(panelView, "#1")
	idx2 := strings.Index(panelView, "#2")
	idx3 := strings.Index(panelView, "#3")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("expected rank markers #1, #2, #3 in panel")
	}
	if idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("ranks should appear in order: #1(@%d) < #2(@%d) < #3(@%d)", idx1, idx2, idx3)
	}

	// The BEST label should appear for #1.
	if !strings.Contains(panelView, "BEST") {
		t.Error("panel should show BEST label for the top-ranked model")
	}
}
