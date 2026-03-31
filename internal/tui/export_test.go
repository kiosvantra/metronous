// This file exports internal symbols for use in external _test packages.
// It is only compiled during testing.
package tui

import (
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/store"
)

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
