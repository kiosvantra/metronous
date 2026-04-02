package tui

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

type mockChartsEventStore struct {
	rows []store.DailyCostByModelRow
}

func (m mockChartsEventStore) InsertEvent(context.Context, store.Event) (string, error) {
	return "", nil
}
func (m mockChartsEventStore) QueryEvents(context.Context, store.EventQuery) ([]store.Event, error) {
	return nil, nil
}
func (m mockChartsEventStore) CountEvents(context.Context, store.EventQuery) (int, error) {
	return 0, nil
}
func (m mockChartsEventStore) QuerySessions(context.Context, store.SessionQuery) ([]store.SessionSummary, error) {
	return nil, nil
}
func (m mockChartsEventStore) GetSessionEvents(context.Context, string) ([]store.Event, error) {
	return nil, nil
}
func (m mockChartsEventStore) GetAgentEvents(context.Context, string, time.Time) ([]store.Event, error) {
	return nil, nil
}
func (m mockChartsEventStore) GetAgentSummary(context.Context, string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}
func (m mockChartsEventStore) QueryDailyCostByModel(context.Context, time.Time, time.Time) ([]store.DailyCostByModelRow, error) {
	return m.rows, nil
}
func (m mockChartsEventStore) Close() error { return nil }

type mockChartsBenchmarkStore struct {
	summaries []store.BenchmarkModelSummary
}

func (m mockChartsBenchmarkStore) SaveRun(context.Context, store.BenchmarkRun) error { return nil }
func (m mockChartsBenchmarkStore) GetRuns(context.Context, string, int) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) QueryRuns(context.Context, store.BenchmarkQuery) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) CountRuns(context.Context, store.BenchmarkQuery) (int, error) {
	return 0, nil
}
func (m mockChartsBenchmarkStore) GetLatestRun(context.Context, string) (*store.BenchmarkRun, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) ListAgents(context.Context) ([]string, error) { return nil, nil }
func (m mockChartsBenchmarkStore) GetVerdictTrend(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) ListRunCycles(context.Context, *time.Location, int, int) ([]time.Time, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) QueryModelSummaries(context.Context) ([]store.BenchmarkModelSummary, error) {
	return m.summaries, nil
}
func (m mockChartsBenchmarkStore) QueryRunsInWindow(context.Context, time.Time, time.Time) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m mockChartsBenchmarkStore) Close() error { return nil }

func TestChartsFetchRanksPerformanceByHealthScore(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	es := mockChartsEventStore{rows: []store.DailyCostByModelRow{
		{Day: monthStart, Model: "alpha", TotalCostUSD: 10},
		{Day: monthStart, Model: "beta", TotalCostUSD: 5},
		{Day: monthStart, Model: "gamma", TotalCostUSD: 3},
	}}
	bs := mockChartsBenchmarkStore{summaries: []store.BenchmarkModelSummary{
		{Model: "alpha", AvgAccuracy: 0.20, AvgP95Ms: 9000, LastVerdict: store.VerdictUrgentSwitch, TotalCostUSD: 10},
		{Model: "beta", AvgAccuracy: 0.95, AvgP95Ms: 150, LastVerdict: store.VerdictKeep, TotalCostUSD: 5},
		{Model: "gamma", AvgAccuracy: 0.80, AvgP95Ms: 1000, LastVerdict: store.VerdictSwitch, TotalCostUSD: 3},
	}}

	m := NewChartsModel(es, bs)
	m.monthStart = monthStart
	msg := m.fetchChartData()()
	data, ok := msg.(ChartsDataMsg)
	if !ok {
		t.Fatalf("expected ChartsDataMsg, got %T", msg)
	}

	if got, want := data.PerformanceSelectedModels, []string{"beta", "gamma", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected performance ranking: got %v want %v", got, want)
	}
	if got, want := data.CostSelectedModels, []string{"alpha", "beta", "gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected cost ranking: got %v want %v", got, want)
	}
	if got, want := data.ResponsibilitySelectedModels, []string{"alpha", "beta", "gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected responsibility ranking: got %v want %v", got, want)
	}
}
