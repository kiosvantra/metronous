package tui

import (
	"strings"
	"testing"
	"time"

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
