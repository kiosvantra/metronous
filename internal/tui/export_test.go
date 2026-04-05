// This file exports internal symbols for use in external _test packages.
// It is only compiled during testing.
package tui

import (
	"time"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/store"
)

// GetBenchmarkSummaryCursor returns the current cursor for summary tests.
func GetBenchmarkSummaryCursor(m BenchmarkSummaryModel) int {
	return m.cursor
}

// GetBenchmarkSummaryRows returns the rows loaded into the summary model.
func GetBenchmarkSummaryRows(m BenchmarkSummaryModel) []summaryRow {
	return m.rows
}

// ComputeHealthScoreForTest exposes computeHealthScore for unit testing.
func ComputeHealthScoreForTest(accuracy, p95Ms float64, verdict store.VerdictType) float64 {
	return computeHealthScore(accuracy, p95Ms, verdict, 0, defaultChartMinROI)
}

// AggregateSummaryRowsForTest runs the same aggregation logic as fetchSummary
// over a provided slice of BenchmarkRuns and returns the computed summaryRows.
// This allows unit tests to verify INSUFFICIENT_DATA filtering, weekly-only
// aggregation, active model marking, and RawModel display without a live store.
func AggregateSummaryRowsForTest(runs []store.BenchmarkRun) []summaryRow {
	type key struct{ agent, model string }
	type agg struct {
		rawModel     string
		runs         int
		totalSamples int
		sumAccuracy  float64
		sumP95       float64
		lastCostUSD  float64
		lastVerdict  store.VerdictType
		lastRunAt    time.Time // most recent non-insufficient run (for verdict/cost display)
		mostRecentAt time.Time // actual most recent run (for active-model tiebreaking)
		lastStatus   store.RunStatus
		lastAccuracy float64
		lastP95      float64
	}
	aggMap := make(map[key]*agg)

	for _, r := range runs {
		if r.RunAt.IsZero() {
			continue
		}
		k := key{r.AgentID, store.NormalizeModelName(r.Model)}
		a := aggMap[k]
		if a == nil {
			a = &agg{}
			aggMap[k] = a
		}

		isWeekly := r.RunKind == store.RunKindWeekly || r.RunKind == ""
		isInsufficient := r.Verdict == store.VerdictInsufficientData || r.SampleSize < 50
		if isWeekly && !isInsufficient {
			samples := r.SampleSize
			if samples <= 0 {
				samples = 1
			}
			a.totalSamples += samples
			a.sumAccuracy += r.Accuracy * float64(samples)
			turnMs := r.AvgTurnMs
			if turnMs <= 0 {
				turnMs = r.AvgLatencyMs
			}
			a.sumP95 += turnMs * float64(samples)
		}
		if isWeekly {
			a.runs++
		}

		if r.RunAt.After(a.mostRecentAt) || a.mostRecentAt.IsZero() {
			a.mostRecentAt = r.RunAt
			a.lastStatus = r.Status
			if r.RawModel != "" {
				a.rawModel = r.RawModel
			}
			a.lastAccuracy = r.Accuracy
			turnMs := r.AvgTurnMs
			if turnMs <= 0 {
				turnMs = r.AvgLatencyMs
			}
			a.lastP95 = turnMs
		}

		if !isInsufficient {
			if r.RunAt.After(a.lastRunAt) || a.lastRunAt.IsZero() {
				a.lastRunAt = r.RunAt
				a.lastVerdict = r.Verdict
				a.lastCostUSD = r.TotalCostUSD
			}
		} else if a.lastVerdict == "" || a.lastVerdict == store.VerdictInsufficientData {
			if r.RunAt.After(a.lastRunAt) || a.lastRunAt.IsZero() {
				a.lastRunAt = r.RunAt
				a.lastVerdict = r.Verdict
				a.lastCostUSD = r.TotalCostUSD
			}
		}
	}

	// Determine active model per agent using mostRecentAt for accurate tiebreaking.
	activeModelByAgent := make(map[string]string)
	activeRunAtByAgent := make(map[string]time.Time)
	for k, a := range aggMap {
		if a.lastStatus == store.RunStatusActive {
			if prev, ok := activeRunAtByAgent[k.agent]; !ok || a.mostRecentAt.After(prev) {
				activeModelByAgent[k.agent] = k.model
				activeRunAtByAgent[k.agent] = a.mostRecentAt
			}
		}
	}

	var rows []summaryRow
	for k, a := range aggMap {
		avgAcc := 0.0
		avgP95 := 0.0
		if a.totalSamples > 0 {
			avgAcc = a.sumAccuracy / float64(a.totalSamples)
			avgP95 = a.sumP95 / float64(a.totalSamples)
		} else {
			avgAcc = a.lastAccuracy
			avgP95 = a.lastP95
		}
		health := computeHealthScore(avgAcc, avgP95, a.lastVerdict, 0, defaultChartMinROI)
		displayModel := a.rawModel
		if displayModel == "" {
			displayModel = k.model
		}
		rows = append(rows, summaryRow{
			AgentID:      k.agent,
			Model:        displayModel,
			RawModel:     a.rawModel,
			IsActive:     activeModelByAgent[k.agent] == k.model,
			Runs:         a.runs,
			AvgAccuracy:  avgAcc,
			AvgTurnMs:    avgP95,
			TotalCostUSD: a.lastCostUSD,
			HealthScore:  health,
			LastVerdict:  a.lastVerdict,
			LastRunAt:    a.lastRunAt,
		})
	}
	return rows
}

// GetSummaryRowIsActive returns the IsActive field of a summaryRow for tests.
func GetSummaryRowIsActive(r summaryRow) bool {
	return r.IsActive
}

// GetSummaryRowRawModel returns the RawModel field of a summaryRow for tests.
func GetSummaryRowRawModel(r summaryRow) string {
	return r.RawModel
}

// SummaryRowForTest is the exported name for the internal summaryRow struct.
// It allows tests to build synthetic BenchmarkSummaryDataMsg payloads.
type SummaryRowForTest = summaryRow

// TrackingSessionEventsMsg exports the internal trackingSessionEventsMsg for tests.
type TrackingSessionEventsMsg = trackingSessionEventsMsg

// DefaultThresholdValuesForTest returns the default threshold values.
// Exposed so external tests can inject realistic data.
func DefaultThresholdValuesForTest() config.Thresholds {
	return config.DefaultThresholdValues()
}

// TrendDirection exposes the internal trendDirection function for testing.
func TrendDirection(verdicts []string) string {
	return trendDirection(verdicts)
}

// GetBenchmarkCycleIndex returns the current cycleIndex for tests.
func GetBenchmarkCycleIndex(m BenchmarkModel) int {
	return m.cycleIndex
}

// GetBenchmarkCycles returns the list of cycle week-starts for tests.
func GetBenchmarkCycles(m BenchmarkModel) []time.Time {
	return m.cycles
}

// GetBenchmarkCursor returns the current cursor for tests.
func GetBenchmarkCursor(m BenchmarkModel) int {
	return m.cursor
}

// GetBenchmarkDetailFrozen returns whether the detail is frozen for tests.
func GetBenchmarkDetailFrozen(m BenchmarkModel) bool {
	return m.detailFrozen
}

// GetBenchmarkFrozenRun returns the frozen run for tests.
func GetBenchmarkFrozenRun(m BenchmarkModel) interface{} {
	return m.frozenRun
}

// GetBenchmarkRunning returns whether an intraweek run is in progress.
func GetBenchmarkRunning(m BenchmarkModel) bool {
	return m.running
}

// GetBenchmarkRunErr returns the error from the last intraweek run (nil if no error).
func GetBenchmarkRunErr(m BenchmarkModel) error {
	return m.runErr
}

// GetChartsMonthStart returns the active month for tests.
func GetChartsMonthStart(m ChartsModel) time.Time {
	return m.monthStart
}

// GetChartsCursor returns the current day cursor for tests.
func GetChartsCursor(m ChartsModel) int {
	return m.cursorDayIndex
}

// GetChartsSelectedModels returns the currently selected models for tests.
func GetChartsSelectedModels(m ChartsModel) []string {
	return m.selectedModels
}

// GetChartsCostSelectedModels returns the cost panel selection for tests.
func GetChartsCostSelectedModels(m ChartsModel) []string {
	return m.selectedModels
}

// GetChartsPerformanceSelectedModels returns the performance panel selection for tests.
func GetChartsPerformanceSelectedModels(m ChartsModel) []string {
	return m.performanceSelectedModels
}

// GetChartsResponsibilitySelectedModels returns the responsibility panel selection for tests.
func GetChartsResponsibilitySelectedModels(m ChartsModel) []string {
	return m.responsibilitySelectedModels
}

// NewBenchmarkModelWithRunner creates a BenchmarkModel with an explicit runner for tests.
func NewBenchmarkModelWithRunner(bs store.BenchmarkStore, r IntraweekRunner) BenchmarkModel {
	return NewBenchmarkModel(bs, "", "", r)
}

// IntraweekRunDoneMsg exposes the internal intraweekRunDoneMsg for tests.
type IntraweekRunDoneMsg = intraweekRunDoneMsg

// --- Tracking session helpers (for tests) ---

// TrackingPageSize exposes maxTrackingRows for pagination tests.
const TrackingPageSize = maxTrackingRows

// GetTrackingPageOffset returns the current session pageOffset for tests.
func GetTrackingPageOffset(m TrackingModel) int {
	return m.pageOffset
}

// GetTrackingCursor returns the current session cursor for tests.
func GetTrackingCursor(m TrackingModel) int {
	return m.cursor
}

// GetTrackingSessionCount returns the number of session summaries loaded.
func GetTrackingSessionCount(m TrackingModel) int {
	return len(m.sessions)
}

// IsTrackingPopupOpen returns whether the popup is currently open.
func IsTrackingPopupOpen(m TrackingModel) bool {
	return m.popupOpen
}

// GetTrackingPopupSessionID returns the session ID of the currently open popup.
func GetTrackingPopupSessionID(m TrackingModel) string {
	return m.popupSessionID
}

// GetTrackingPopupEvents returns the frozen events in the popup (may be nil).
func GetTrackingPopupEvents(m TrackingModel) []store.Event {
	return m.popupEvents
}

// GetTrackingPopupCursor returns the current cursor row within the popup viewport.
func GetTrackingPopupCursor(m TrackingModel) int {
	return m.popupCursor
}

// GetTrackingPopupOffset returns the current scroll offset within popupEvents.
func GetTrackingPopupOffset(m TrackingModel) int {
	return m.popupOffset
}
