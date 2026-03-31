// This file exports internal symbols for use in external _test packages.
// It is only compiled during testing.
package tui

import "github.com/kiosvantra/metronous/internal/config"

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

// BenchmarkPageSize exposes maxBenchmarkRows for pagination tests.
const BenchmarkPageSize = maxBenchmarkRows

// GetBenchmarkPageOffset returns the current pageOffset for tests.
func GetBenchmarkPageOffset(m BenchmarkModel) int {
	return m.pageOffset
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

// --- Tracking session helpers (for tests) ---

// TrackingPageSize exposes maxTrackingRows for pagination tests.
const TrackingPageSize = maxTrackingRows

// GetTrackingPageOffset returns the current session pageOffset for tests.
func GetTrackingPageOffset(m TrackingModel) int {
	return m.pageOffset
}

// GetTrackingCursor returns the current flat-row cursor for tests.
func GetTrackingCursor(m TrackingModel) int {
	return m.cursor
}

// GetTrackingSessionCount returns the number of session summaries loaded.
func GetTrackingSessionCount(m TrackingModel) int {
	return len(m.sessions)
}

// IsTrackingSessionExpanded returns whether the session at the given sessions index is expanded.
func IsTrackingSessionExpanded(m TrackingModel, sessionID string) bool {
	st := m.sessionStates[sessionID]
	return st != nil && st.expanded
}

// GetTrackingSessionEvents returns the cached events for a given session (may be nil).
func GetTrackingSessionEvents(m TrackingModel, sessionID string) interface{} {
	st := m.sessionStates[sessionID]
	if st == nil {
		return nil
	}
	return st.events
}
