package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestBenchmarkDetailedKeepsTableHeightStableAcrossCursorMoves(t *testing.T) {
	runs := []store.BenchmarkRun{
		{AgentID: "agent-1", Model: "model-a", RunAt: time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
		{AgentID: "agent-2"},
	}
	m := BenchmarkModel{runs: runs, cursor: 0, pricing: map[string]float64{"model-a": 0.01}}
	first := m.View()
	m.cursor = 1
	second := m.View()
	if strings.Count(first, "\n") != strings.Count(second, "\n") {
		t.Fatalf("benchmark detailed view height changed across cursor moves: first=%d second=%d", strings.Count(first, "\n"), strings.Count(second, "\n"))
	}
}

func TestBenchmarkDetailedNoDataCursorDoesNotChangeHeight(t *testing.T) {
	runs := []store.BenchmarkRun{
		{AgentID: "agent-1", Model: "model-a", RunAt: time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
		{AgentID: "agent-2"},
	}
	m := &BenchmarkModel{runs: runs, cursor: 0, pricing: map[string]float64{"model-a": 0.01}}
	before := m.View()
	m.cursor = 1
	after := m.View()
	if strings.Count(before, "\n") != strings.Count(after, "\n") {
		t.Fatalf("NO DATA cursor move changed height: before=%d after=%d", strings.Count(before, "\n"), strings.Count(after, "\n"))
	}
	if !strings.Contains(after, "NO DATA") {
		t.Fatalf("expected NO DATA row in rendered output")
	}
}

func TestBenchmarkDetailedOmitsExtraScrollIndicatorLines(t *testing.T) {
	runs := make([]store.BenchmarkRun, 0, maxBenchmarkRows+2)
	for i := 0; i < maxBenchmarkRows+2; i++ {
		runs = append(runs, store.BenchmarkRun{
			AgentID:      "agent",
			Model:        "model-a",
			RunAt:        time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute),
			SampleSize:   120,
			Accuracy:     0.93,
			AvgTurnMs:    1250,
			TotalCostUSD: 1.23,
			Verdict:      store.VerdictKeep,
			Status:       store.RunStatusActive,
		})
	}
	m := BenchmarkModel{runs: runs, cursor: maxBenchmarkRows, offset: 1, pricing: map[string]float64{"model-a": 0.01}}
	view := m.View()
	if strings.Contains(view, "more above") || strings.Contains(view, "more below") {
		t.Fatalf("expected Detailed to keep scroll state in footer only, got %q", view)
	}
	if !strings.Contains(view, "showing 2-") {
		t.Fatalf("expected footer to report visible window, got %q", view)
	}
}

func TestBenchmarkDetailedNoDataDetailPanelClearsPreviousContent(t *testing.T) {
	active := store.BenchmarkRun{
		AgentID:      "agent-1",
		Model:        "model-a",
		RunAt:        time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC),
		SampleSize:   120,
		Accuracy:     0.93,
		AvgTurnMs:    1250,
		TotalCostUSD: 1.23,
		Verdict:      store.VerdictKeep,
		Status:       store.RunStatusActive,
	}
	noData := store.BenchmarkRun{AgentID: "agent-2"}
	m := &BenchmarkModel{runs: []store.BenchmarkRun{active, noData}, cursor: 0, pricing: map[string]float64{"model-a": 0.01}}
	_ = m.View()
	m.cursor = 1
	view := m.View()
	if !strings.Contains(view, "No benchmark runs recorded yet for this agent.") {
		t.Fatalf("expected NO DATA detail text, got %q", view)
	}
	if strings.Contains(view, "Cost:") {
		t.Fatalf("expected previous detail content to be cleared for NO DATA row, got %q", view)
	}
}

func TestBenchmarkDetailedUsesCompactDefaultVisibleWindow(t *testing.T) {
	m := BenchmarkModel{}
	if got := m.visibleBenchmarkRows(); got != benchmarkDefaultVisibleRows {
		t.Fatalf("expected compact default visible rows %d, got %d", benchmarkDefaultVisibleRows, got)
	}
}

func TestBenchmarkDetailedShrinksVisibleWindowToFitHeight(t *testing.T) {
	m := BenchmarkModel{height: 42}
	if got := m.visibleBenchmarkRows(); got >= benchmarkDefaultVisibleRows {
		t.Fatalf("expected reduced visible rows for constrained height, got %d", got)
	}
	if got := m.visibleBenchmarkRows(); got < 3 {
		t.Fatalf("expected minimum visible rows floor, got %d", got)
	}
}

func TestBenchmarkDetailedCurrentMarkerUsesSameRowLayout(t *testing.T) {
	runs := []store.BenchmarkRun{
		{AgentID: "agent-1", Model: "model-a", RunAt: time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
		{AgentID: "agent-2", Model: "model-b", RunAt: time.Date(2026, 4, 6, 11, 0, 0, 0, time.UTC), SampleSize: 120, Accuracy: 0.93, AvgTurnMs: 1250, TotalCostUSD: 1.23, Verdict: store.VerdictKeep, Status: store.RunStatusActive},
	}
	m := BenchmarkModel{runs: runs, cursor: 0, pricing: map[string]float64{"model-a": 0.01, "model-b": 0.01}}
	view := m.View()
	if !strings.Contains(view, "● agent-1") {
		t.Fatalf("expected current marker inline with agent cell, got: %q", view)
	}
}
