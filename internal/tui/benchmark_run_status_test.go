package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	storepkg "github.com/kiosvantra/metronous/internal/store"
)

type benchmarkRunStatusStore struct {
	runs []storepkg.BenchmarkRun
}

func (s *benchmarkRunStatusStore) SaveRun(context.Context, storepkg.BenchmarkRun) error { return nil }
func (s *benchmarkRunStatusStore) GetRuns(context.Context, string, int) ([]storepkg.BenchmarkRun, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) QueryRuns(_ context.Context, q storepkg.BenchmarkQuery) ([]storepkg.BenchmarkRun, error) {
	if q.Offset >= len(s.runs) {
		return nil, nil
	}
	end := len(s.runs)
	if q.Limit > 0 && q.Offset+q.Limit < end {
		end = q.Offset + q.Limit
	}
	return s.runs[q.Offset:end], nil
}
func (s *benchmarkRunStatusStore) GetBenchmarkAttemptStates(context.Context) ([]storepkg.BenchmarkAttemptState, error) {
	return []storepkg.BenchmarkAttemptState{
		{RunKind: storepkg.RunKindWeekly, LastAttemptAt: time.Date(2026, 4, 7, 3, 0, 0, 0, time.UTC), LastAttemptStatus: storepkg.BenchmarkAttemptCompleted},
		{RunKind: storepkg.RunKindIntraweek, LastAttemptAt: time.Date(2026, 4, 8, 15, 0, 0, 0, time.UTC), LastAttemptStatus: storepkg.BenchmarkAttemptFailed},
	}, nil
}
func (s *benchmarkRunStatusStore) CountRuns(context.Context, storepkg.BenchmarkQuery) (int, error) {
	return len(s.runs), nil
}
func (s *benchmarkRunStatusStore) GetLatestRun(context.Context, string) (*storepkg.BenchmarkRun, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) ListAgents(context.Context) ([]string, error) { return nil, nil }
func (s *benchmarkRunStatusStore) GetVerdictTrend(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) ListRunCycles(context.Context, *time.Location, int, int) ([]time.Time, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) QueryModelSummaries(context.Context) ([]storepkg.BenchmarkModelSummary, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) QueryRunsInWindow(context.Context, time.Time, time.Time) ([]storepkg.BenchmarkRun, error) {
	return nil, nil
}
func (s *benchmarkRunStatusStore) MarkSupersededRuns(context.Context, string, time.Time, string, time.Time, time.Time) error {
	return nil
}
func (s *benchmarkRunStatusStore) Close() error { return nil }

func TestLoadBenchmarkRunStatus(t *testing.T) {
	now := time.Date(2026, 4, 7, 10, 30, 0, 0, time.UTC)
	weekly := now.Add(-24 * time.Hour)
	intraweek := now.Add(-2 * time.Hour)

	bs := &benchmarkRunStatusStore{runs: []storepkg.BenchmarkRun{
		{RunAt: intraweek, RunKind: storepkg.RunKindIntraweek},
		{RunAt: weekly, RunKind: storepkg.RunKindWeekly},
	}}

	status, err := loadBenchmarkRunStatus(context.Background(), bs)
	if err != nil {
		t.Fatalf("loadBenchmarkRunStatus: %v", err)
	}
	if !status.lastWeeklyRunAt.Equal(weekly) {
		t.Fatalf("lastWeeklyRunAt = %v, want %v", status.lastWeeklyRunAt, weekly)
	}
	if !status.lastIntraweekRunAt.Equal(intraweek) {
		t.Fatalf("lastIntraweekRunAt = %v, want %v", status.lastIntraweekRunAt, intraweek)
	}
	if got := status.lastWeeklyAttemptAt; got.IsZero() {
		t.Fatal("expected weekly attempt state")
	}
	if got := status.lastIntraweekAttemptAt; got.IsZero() {
		t.Fatal("expected intraweek attempt state")
	}
}

func TestBenchmarkSummaryViewShowsLastRunStatus(t *testing.T) {
	m := BenchmarkSummaryModel{
		lastWeeklyRunAt:    time.Date(2026, 4, 7, 2, 0, 0, 0, time.UTC),
		lastIntraweekRunAt: time.Date(2026, 4, 8, 14, 15, 0, 0, time.UTC),
	}

	view := m.View()
	if !strings.Contains(view, "Last weekly saved run:") {
		t.Fatalf("expected weekly status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last weekly attempt:") {
		t.Fatalf("expected weekly attempt status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last intraweek saved run:") {
		t.Fatalf("expected intraweek status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last intraweek attempt:") {
		t.Fatalf("expected intraweek attempt status in view, got: %q", view)
	}
}

func TestBenchmarkDetailedViewShowsLastRunStatus(t *testing.T) {
	m := BenchmarkModel{
		lastWeeklyRunAt:    time.Date(2026, 4, 7, 2, 0, 0, 0, time.UTC),
		lastIntraweekRunAt: time.Date(2026, 4, 8, 14, 15, 0, 0, time.UTC),
	}

	view := m.View()
	if !strings.Contains(view, "Last weekly saved run:") {
		t.Fatalf("expected weekly status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last weekly attempt:") {
		t.Fatalf("expected weekly attempt status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last intraweek saved run:") {
		t.Fatalf("expected intraweek status in view, got: %q", view)
	}
	if !strings.Contains(view, "Last intraweek attempt:") {
		t.Fatalf("expected intraweek attempt status in view, got: %q", view)
	}
}
