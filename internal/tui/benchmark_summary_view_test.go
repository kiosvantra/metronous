package tui

import (
	"context"
	"testing"
	"time"

	storepkg "github.com/kiosvantra/metronous/internal/store"
)

// summaryTestStore is a minimal BenchmarkStore implementation used to drive
// BenchmarkSummaryModel.fetchSummary in tests. Only ListAgents and GetRuns are
// exercised; all other methods are no-ops.
type summaryTestStore struct {
	agents      []string
	runsByAgent map[string][]storepkg.BenchmarkRun
}

func (s *summaryTestStore) SaveRun(ctx context.Context, run storepkg.BenchmarkRun) error {
	return nil
}

func (s *summaryTestStore) GetRuns(ctx context.Context, agentID string, limit int) ([]storepkg.BenchmarkRun, error) {
	return s.runsByAgent[agentID], nil
}

func (s *summaryTestStore) QueryRuns(ctx context.Context, _ storepkg.BenchmarkQuery) ([]storepkg.BenchmarkRun, error) {
	return nil, nil
}

func (s *summaryTestStore) CountRuns(ctx context.Context, _ storepkg.BenchmarkQuery) (int, error) {
	return 0, nil
}

func (s *summaryTestStore) GetLatestRun(ctx context.Context, agentID string) (*storepkg.BenchmarkRun, error) {
	return nil, nil
}

func (s *summaryTestStore) ListAgents(ctx context.Context) ([]string, error) {
	return s.agents, nil
}

func (s *summaryTestStore) GetVerdictTrend(ctx context.Context, agentID string, weeks int) ([]string, error) {
	return nil, nil
}

func (s *summaryTestStore) ListRunCycles(ctx context.Context, loc *time.Location, limit, offset int) ([]time.Time, error) {
	return nil, nil
}

func (s *summaryTestStore) QueryModelSummaries(ctx context.Context) ([]storepkg.BenchmarkModelSummary, error) {
	return nil, nil
}

func (s *summaryTestStore) QueryRunsInWindow(ctx context.Context, since, until time.Time) ([]storepkg.BenchmarkRun, error) {
	return nil, nil
}

func (s *summaryTestStore) MarkSupersededRuns(ctx context.Context, agentID string, newRunAt time.Time, newModel string, cycleStart, cycleEnd time.Time) error {
	return nil
}

func (s *summaryTestStore) Close() error { return nil }

// TestBenchmarkSummaryKeepsActiveIntraweekModel verifies that the summary view
// keeps the agent's currently active model even when it only has intraweek
// runs (no weekly runs in the last 4 cycles) and would otherwise be filtered
// out by the weekly-cycle display filter.
func TestBenchmarkSummaryKeepsActiveIntraweekModel(t *testing.T) {
	agentID := "agent-1"
	oldModel := "old-model"
	newModel := "new-model"
	now := time.Now()

	// Weekly run for the old model in a recent weekly cycle.
	weeklyRun := storepkg.BenchmarkRun{
		AgentID:    agentID,
		Model:      oldModel,
		RunKind:    storepkg.RunKindWeekly,
		RunAt:      now.Add(-7 * 24 * time.Hour),
		SampleSize: 100,
		Accuracy:   0.9,
		AvgTurnMs:  1000,
		Status:     storepkg.RunStatusSuperseded,
		Verdict:    storepkg.VerdictKeep,
	}

	// Intraweek run for the new model, marked as the current active run.
	activeRun := storepkg.BenchmarkRun{
		AgentID:    agentID,
		Model:      newModel,
		RawModel:   "provider/" + newModel,
		RunKind:    storepkg.RunKindIntraweek,
		RunAt:      now,
		SampleSize: 50,
		Accuracy:   0.95,
		AvgTurnMs:  800,
		Status:     storepkg.RunStatusActive,
		Verdict:    storepkg.VerdictKeep,
	}

	bs := &summaryTestStore{
		agents: []string{agentID},
		runsByAgent: map[string][]storepkg.BenchmarkRun{
			agentID: {activeRun, weeklyRun},
		},
	}

	model := NewBenchmarkSummaryModel(bs, nil)
	cmd := model.fetchSummary()
	if cmd == nil {
		t.Fatalf("expected fetchSummary command, got nil")
	}

	msg := cmd()
	data, ok := msg.(BenchmarkSummaryDataMsg)
	if !ok {
		t.Fatalf("expected BenchmarkSummaryDataMsg, got %T", msg)
	}
	if data.Err != nil {
		t.Fatalf("fetchSummary returned error: %v", data.Err)
	}

	// Expect a row for the new active model and it must be marked IsActive.
	var found bool
	for _, row := range data.Rows {
		// Debug helper to understand which rows are present in the summary.
		t.Logf("row: agent=%s model=%s isActive=%v", row.AgentID, row.Model, row.IsActive)
		if row.AgentID != agentID {
			continue
		}
		// Match either by RawModel (preferred) or by normalized model name.
		if row.RawModel == activeRun.RawModel || storepkg.NormalizeModelName(row.Model) == storepkg.NormalizeModelName(newModel) {
			found = true
			if !row.IsActive {
				t.Fatalf("expected active model row to have IsActive=true, got false")
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected summary to include active intraweek model row, but it was filtered out")
	}
}
