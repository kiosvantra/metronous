// This file exports internal symbols for use in external _test packages.
// It is only compiled during testing.
package tui

import "github.com/kiosvantra/metronous/internal/config"

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
