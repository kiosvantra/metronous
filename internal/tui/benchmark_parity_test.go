package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	storepkg "github.com/kiosvantra/metronous/internal/store"
)

func TestBenchmarkDetailedMatchesSummaryCursorWindowBehavior(t *testing.T) {
	runs := make([]storepkg.BenchmarkRun, 0, 18)
	rows := make([]summaryRow, 0, 18)
	for i := 0; i < 18; i++ {
		agent := "agent-a"
		if i >= 9 {
			agent = "agent-b"
		}
		run := storepkg.BenchmarkRun{
			AgentID:    agent,
			Model:      "openai/gpt-4.1",
			RawModel:   "openai/gpt-4.1",
			RunAt:      time.Date(2026, 4, 8, 12, i, 0, 0, time.UTC),
			SampleSize: 80 + i,
			Accuracy:   0.91,
			AvgTurnMs:  1200,
			Verdict:    storepkg.VerdictKeep,
			Status:     storepkg.RunStatusActive,
		}
		if i%3 == 0 {
			run.Status = storepkg.RunStatusSuperseded
		}
		runs = append(runs, run)
		rows = append(rows, summaryRow{
			AgentID:     agent,
			Model:       "openai/gpt-4.1",
			RawModel:    "openai/gpt-4.1",
			IsActive:    run.Status == storepkg.RunStatusActive,
			Runs:        1,
			AvgAccuracy: 0.91,
			AvgTurnMs:   1200,
			LastVerdict: storepkg.VerdictKeep,
			LastRunAt:   run.RunAt,
		})
	}

	bm := BenchmarkModel{runs: runs, cursor: 11, offset: 3, cycles: []time.Time{time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)}}
	bs := BenchmarkSummaryModel{rows: rows, cursor: 11, offset: 3}

	beforeDetailed := bm.View()
	beforeSummary := bs.View()
	if !strings.Contains(beforeDetailed, "Decision Rationale") || !strings.Contains(beforeSummary, "Agent History Summary") {
		t.Fatalf("expected both views to render detail panels")
	}

	bm, _ = bm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("up")})
	bs, _ = bs.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("up")})
	afterDetailed := bm.View()
	afterSummary := bs.View()
	if strings.Count(afterDetailed, "Decision Rationale") == 0 {
		t.Fatalf("detailed view lost its detail panel after cursor move")
	}
	if strings.Count(afterSummary, "Agent History Summary") == 0 {
		t.Fatalf("summary view lost its detail panel after cursor move")
	}
}
