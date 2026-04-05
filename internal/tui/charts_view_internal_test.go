package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	runs      []store.BenchmarkRun
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
	return m.runs, nil
}
func (m mockChartsBenchmarkStore) MarkSupersededRuns(context.Context, string, time.Time, string, time.Time, time.Time) error {
	return nil
}
func (m mockChartsBenchmarkStore) Close() error { return nil }

func TestChartsFetchRanksMonthlyCards(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	es := mockChartsEventStore{rows: []store.DailyCostByModelRow{
		{Day: monthStart, Model: "alpha", TotalCostUSD: 10},
		{Day: monthStart, Model: "beta", TotalCostUSD: 8},
		{Day: monthStart, Model: "gamma", TotalCostUSD: 6},
		{Day: monthStart, Model: "delta", TotalCostUSD: 4},
	}}
	bs := mockChartsBenchmarkStore{runs: []store.BenchmarkRun{
		{AgentID: "build", Model: "alpha", RunAt: monthStart.AddDate(0, 0, 1), SampleSize: 100, Accuracy: 0.94, P95LatencyMs: 100, Verdict: store.VerdictKeep},
		{AgentID: "sdd-orchestrator", Model: "beta", RunAt: monthStart.AddDate(0, 0, 2), SampleSize: 100, Accuracy: 0.92, P95LatencyMs: 200, Verdict: store.VerdictKeep},
		{AgentID: "sdd-explore", Model: "gamma", RunAt: monthStart.AddDate(0, 0, 3), SampleSize: 100, Accuracy: 0.88, P95LatencyMs: 500, Verdict: store.VerdictSwitch},
		{AgentID: "sdd-init", Model: "delta", RunAt: monthStart.AddDate(0, 0, 4), SampleSize: 100, Accuracy: 0.80, P95LatencyMs: 1000, Verdict: store.VerdictUrgentSwitch},
	}}

	m := NewChartsModel(es, bs)
	m.monthStart = monthStart
	msg := m.fetchChartData()()
	data, ok := msg.(ChartsDataMsg)
	if !ok {
		t.Fatalf("expected ChartsDataMsg, got %T", msg)
	}

	// Cost chart now shows top 5; with 4 models all 4 should appear.
	if got, want := data.CostSelectedModels, []string{"alpha", "beta", "gamma", "delta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected cost ranking: got %v want %v", got, want)
	}
	if got, want := data.PerformanceSelectedModels, []string{"alpha", "beta", "gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected performance ranking: got %v want %v", got, want)
	}
	if got, want := data.ResponsibilitySelectedModels, []string{"beta", "alpha", "gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected responsibility ranking: got %v want %v", got, want)
	}

	m, _ = m.Update(data)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	view := m.View()
	for _, want := range []string{"Cost chart", "Performance Top 3 of the Month", "Responsibility Top 3 of the Month"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in view, got %q", want, view)
		}
	}
}

func TestChartsMonthCursorDoesNotChangeWithModeToggleKey(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	m := NewChartsModel(nil, nil)
	m.monthStart = monthStart
	originalCursor := m.cursorDayIndex
	originalMonth := m.monthStart

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = updated

	if m.cursorDayIndex != originalCursor {
		t.Fatalf("expected cursor to stay at %d, got %d", originalCursor, m.cursorDayIndex)
	}
	if m.monthStart.Year() != originalMonth.Year() || m.monthStart.Month() != originalMonth.Month() {
		t.Fatalf("expected month to stay unchanged after pressing m")
	}
}

func TestChartsMouseCursorSelectsExpectedDay(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC) // 30 days
	m := NewChartsModel(nil, nil)
	m.monthStart = monthStart
	m.width = 160
	// Height is not used by handleMouse.

	const leftGutter = 10
	const cellWidth = 4
	chartStartX := leftGutter + 2
	wantDay := 5

	msg := tea.MouseMsg{X: chartStartX + cellWidth*wantDay + 1, Y: 0, Type: tea.MouseMotion}
	m.handleMouse(msg)
	if m.cursorDayIndex != wantDay {
		t.Fatalf("expected cursorDayIndex %d, got %d", wantDay, m.cursorDayIndex)
	}
}

func TestChartCostToUnitsShowsGrowthAboveMinimumHalfBlock(t *testing.T) {
	const barUnits = 12
	minPositive := 0.001
	maxTotal := 1.0
	day1Units := chartCostToUnits(0.001, minPositive, maxTotal, barUnits, 1)
	day2Units := chartCostToUnits(0.002, minPositive, maxTotal, barUnits, 1)
	day3Units := chartCostToUnits(1.0, minPositive, maxTotal, barUnits, 1)

	if day1Units <= 0 {
		t.Fatalf("expected minimum visible units for day1, got %d", day1Units)
	}
	if day2Units <= day1Units {
		t.Fatalf("expected day2 (%d) to map above day1 (%d)", day2Units, day1Units)
	}
	if day3Units <= day2Units {
		t.Fatalf("expected day3 (%d) to map above day2 (%d)", day3Units, day2Units)
	}
}
