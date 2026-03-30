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
