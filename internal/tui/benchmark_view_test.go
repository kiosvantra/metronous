package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kiosvantra/metronous/internal/store"
)

func TestBenchmarkViewStabilizesHeightAcrossCursorMoves(t *testing.T) {
	runs := []store.BenchmarkRun{
		{
			AgentID:             "agent-1",
			Model:               "model-a",
			RunAt:               time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC),
			SampleSize:          120,
			Accuracy:            0.93,
			AvgTurnMs:           1250,
			TotalCostUSD:        1.23,
			Verdict:             store.VerdictKeep,
			Status:              store.RunStatusActive,
			DecisionReason:      "keep",
			RecommendedModel:    "",
			ToolSuccessRate:     0.99,
			ROIScore:            0.5,
			AvgCompletionTokens: 100,
			AvgPromptTokens:     200,
		},
		{AgentID: "agent-2"},
	}

	m := &BenchmarkModel{
		runs:      runs,
		trendByID: map[string][]string{"agent-1": {"KEEP", "KEEP"}},
		pricing:   map[string]float64{"model-a": 0.01},
	}

	first := m.View()
	firstLines := strings.Count(first, "\n")
	if !strings.Contains(first, "Agent") {
		t.Fatalf("expected header in first render")
	}

	m.cursor = 1
	second := m.View()
	secondLines := strings.Count(second, "\n")
	if !strings.Contains(second, "Agent") {
		t.Fatalf("expected header in second render")
	}
	if secondLines != firstLines {
		t.Fatalf("line count changed after cursor move: first=%d second=%d", firstLines, secondLines)
	}
}

func TestBenchmarkViewKeepsHeaderVisibleWhenMovingUpFromBottom(t *testing.T) {
	runs := make([]store.BenchmarkRun, 0, maxBenchmarkRows+1)
	for i := 0; i < maxBenchmarkRows; i++ {
		runs = append(runs, store.BenchmarkRun{
			AgentID:             "agent-a",
			Model:               "model-a",
			RunAt:               time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute),
			SampleSize:          120,
			Accuracy:            0.93,
			AvgTurnMs:           1250,
			TotalCostUSD:        1.23,
			Verdict:             store.VerdictKeep,
			Status:              store.RunStatusActive,
			ToolSuccessRate:     0.99,
			ROIScore:            0.5,
			AvgCompletionTokens: 100,
			AvgPromptTokens:     200,
		})
	}
	// Bottom row is a placeholder with a much shorter detail panel. Moving up from
	// here used to grow the render height and push the table header off-screen.
	runs = append(runs, store.BenchmarkRun{AgentID: "agent-z"})

	m := BenchmarkModel{
		runs:      runs,
		cursor:    len(runs) - 1,
		offset:    len(runs) - maxBenchmarkRows,
		trendByID: map[string][]string{"agent-a": {"KEEP", "KEEP", "KEEP"}},
		pricing:   map[string]float64{"model-a": 0.01},
	}

	bottom := m.View()
	bottomLines := strings.Count(bottom, "\n")
	if !strings.Contains(bottom, "Agent") {
		t.Fatalf("expected header at bottom render")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	up := updated.View()
	upLines := strings.Count(up, "\n")
	if !strings.Contains(up, "Agent") {
		t.Fatalf("expected header after moving up from bottom")
	}
	if upLines != bottomLines {
		t.Fatalf("line count changed after moving up from bottom: bottom=%d up=%d", bottomLines, upLines)
	}
}

func TestBenchmarkViewCurrentRowDoesNotInsertExtraSeparator(t *testing.T) {
	runs := []store.BenchmarkRun{
		{AgentID: "agent-1", Model: "model-a", RunAt: time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
		{AgentID: "agent-2", Model: "model-b", RunAt: time.Date(2026, 4, 6, 11, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
	}
	m := &BenchmarkModel{runs: runs, cursor: 0, pricing: map[string]float64{"model-a": 0.01, "model-b": 0.01}}
	view := m.View()
	if strings.Contains(view, "● Agent") {
		t.Fatalf("unexpected separator between marker and agent cell: %q", view)
	}
	if !strings.Contains(view, "● agent-1") {
		t.Fatalf("expected active marker on current row, got: %q", view)
	}
}
