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
		{AgentID: "agent-2", RunAt: time.Time{}},
	}
	m := BenchmarkModel{runs: runs, cursor: 0, pricing: map[string]float64{"model-a": 0.01}}
	first := m.View()
	m.cursor = 1
	second := m.View()
	if strings.Count(first, "\n") != strings.Count(second, "\n") {
		t.Fatalf("benchmark detailed view height changed across cursor moves: first=%d second=%d", strings.Count(first, "\n"), strings.Count(second, "\n"))
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
