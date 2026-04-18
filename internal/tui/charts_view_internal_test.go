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

// liveChartsEventStore is a test double that returns different daily cost
// snapshots on successive calls so we can verify that the charts view reacts
// to live data changes (cost chart, legend, and tooltip) instead of only the
// aggregated monthly total.
type liveChartsEventStore struct {
	rowsByCall [][]store.DailyCostByModelRow
	callIdx    int
}

func (m *liveChartsEventStore) InsertEvent(context.Context, store.Event) (string, error) {
	return "", nil
}

func (m *liveChartsEventStore) QueryEvents(context.Context, store.EventQuery) ([]store.Event, error) {
	return nil, nil
}

func (m *liveChartsEventStore) CountEvents(context.Context, store.EventQuery) (int, error) {
	return 0, nil
}

func (m *liveChartsEventStore) QuerySessions(context.Context, store.SessionQuery) ([]store.SessionSummary, error) {
	return nil, nil
}

func (m *liveChartsEventStore) GetSessionEvents(context.Context, string) ([]store.Event, error) {
	return nil, nil
}

func (m *liveChartsEventStore) GetAgentEvents(context.Context, string, time.Time) ([]store.Event, error) {
	return nil, nil
}

func (m *liveChartsEventStore) GetAgentSummary(context.Context, string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}

func (m *liveChartsEventStore) QueryDailyCostByModel(context.Context, time.Time, time.Time) ([]store.DailyCostByModelRow, error) {
	if len(m.rowsByCall) == 0 {
		return nil, nil
	}
	if m.callIdx >= len(m.rowsByCall) {
		return m.rowsByCall[len(m.rowsByCall)-1], nil
	}
	rows := m.rowsByCall[m.callIdx]
	m.callIdx++
	return rows, nil
}

func (m *liveChartsEventStore) Close() error { return nil }

func TestChartsFetchRanksMonthlyCards(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	es := mockChartsEventStore{rows: []store.DailyCostByModelRow{
		{Day: monthStart, Model: "alpha", TotalCostUSD: 10},
		{Day: monthStart, Model: "beta", TotalCostUSD: 8},
		{Day: monthStart, Model: "gamma", TotalCostUSD: 6},
		{Day: monthStart, Model: "delta", TotalCostUSD: 4},
	}}
	bs := mockChartsBenchmarkStore{runs: []store.BenchmarkRun{
		{AgentID: "build", Model: "alpha", RunAt: monthStart.AddDate(0, 0, 1), SampleSize: 100, Accuracy: 0.94, AvgTurnMs: 100, Verdict: store.VerdictKeep, RunKind: store.RunKindWeekly, Status: store.RunStatusActive},
		{AgentID: "sdd-orchestrator", Model: "beta", RunAt: monthStart.AddDate(0, 0, 2), SampleSize: 100, Accuracy: 0.92, AvgTurnMs: 200, Verdict: store.VerdictKeep, RunKind: store.RunKindWeekly, Status: store.RunStatusActive},
		{AgentID: "sdd-explore", Model: "gamma", RunAt: monthStart.AddDate(0, 0, 3), SampleSize: 100, Accuracy: 0.88, AvgTurnMs: 500, Verdict: store.VerdictSwitch, RunKind: store.RunKindWeekly, Status: store.RunStatusActive},
		{AgentID: "sdd-init", Model: "delta", RunAt: monthStart.AddDate(0, 0, 4), SampleSize: 100, Accuracy: 0.80, AvgTurnMs: 1000, Verdict: store.VerdictUrgentSwitch, RunKind: store.RunKindWeekly, Status: store.RunStatusActive},
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

// TestChartsLiveRefreshKeepsChartLegendAndTooltipInSync verifies that when the
// underlying EventStore returns updated daily cost data (simulating new live
// events), the cost chart, legend, and tooltip all refresh consistently every
// time fetchChartData is called – matching the updated monthly total spent.
func TestChartsLiveRefreshKeepsChartLegendAndTooltipInSync(t *testing.T) {
	monthStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	day := monthStart

	es := &liveChartsEventStore{
		rowsByCall: [][]store.DailyCostByModelRow{
			// First snapshot: $1 total for alpha.
			{{Day: day, Model: "alpha", TotalCostUSD: 1}},
			// Second snapshot: cost doubles to $2 for alpha.
			{{Day: day, Model: "alpha", TotalCostUSD: 2}},
		},
	}

	m := NewChartsModel(es, nil)
	m.monthStart = monthStart
	m.width = 120
	m.height = 40

	// Initial fetch.
	msg1 := m.fetchChartData()()
	data1, ok := msg1.(ChartsDataMsg)
	if !ok {
		t.Fatalf("expected ChartsDataMsg, got %T", msg1)
	}
	m, _ = m.Update(data1)
	view1 := m.View()

	if !strings.Contains(view1, "Total Spent of the Month: $1.00") {
		t.Fatalf("expected initial total spent $1.00, got view: %q", view1)
	}
	if !strings.Contains(view1, "Tooltip: "+day.Format("Jan 02")+" ($1.00)") {
		t.Fatalf("expected tooltip with $1.00 for selected day, got view: %q", view1)
	}
	if !strings.Contains(view1, "Legend:") || !strings.Contains(view1, "alpha") || !strings.Contains(view1, "($1.00)") {
		t.Fatalf("expected legend to show alpha with $1.00, got view: %q", view1)
	}

	// Second fetch simulating the 2s refresh with updated costs.
	msg2 := m.fetchChartData()()
	data2, ok := msg2.(ChartsDataMsg)
	if !ok {
		t.Fatalf("expected ChartsDataMsg, got %T", msg2)
	}
	m, _ = m.Update(data2)
	view2 := m.View()

	if !strings.Contains(view2, "Total Spent of the Month: $2.00") {
		t.Fatalf("expected refreshed total spent $2.00, got view: %q", view2)
	}
	if !strings.Contains(view2, "Tooltip: "+day.Format("Jan 02")+" ($2.00)") {
		t.Fatalf("expected tooltip to refresh to $2.00, got view: %q", view2)
	}
	if !strings.Contains(view2, "Legend:") || !strings.Contains(view2, "alpha") || !strings.Contains(view2, "($2.00)") {
		t.Fatalf("expected legend to refresh alpha total to $2.00, got view: %q", view2)
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
