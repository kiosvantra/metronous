// Package benchmark provides metrics calculation and event aggregation for weekly
// benchmark runs. It fetches events from the EventStore, computes percentile
// latencies, accuracy, tool success rate, and ROI score, then hands off to the
// decision engine.
package benchmark

import (
	"sort"
)

// MinSampleSize is the minimum number of events required to compute meaningful
// metrics. Runs with fewer events receive the INSUFFICIENT_DATA verdict.
const MinSampleSize = 50

// CalculateAccuracy returns the fraction of non-error events over total events.
// A "complete" event is any event whose EventType is not "error".
// If total is zero, returns 0.
func CalculateAccuracy(completed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(completed) / float64(total)
}

// CalculateLatencyPercentiles computes the p50, p95, and p99 latency percentiles
// from a slice of duration values in milliseconds.
// The input slice is not modified (a copy is sorted internally).
// Returns (0, 0, 0) if the slice is empty.
func CalculateLatencyPercentiles(durations []int) (p50, p95, p99 float64) {
	if len(durations) == 0 {
		return 0, 0, 0
	}

	sorted := make([]int, len(durations))
	copy(sorted, durations)
	sort.Ints(sorted)

	p50 = percentile(sorted, 50)
	p95 = percentile(sorted, 95)
	p99 = percentile(sorted, 99)
	return
}

// percentile returns the value at the given percentile rank (0–100)
// using the nearest-rank method: index = floor(rank/100 * n), 0-indexed.
func percentile(sorted []int, rank int) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if rank <= 0 {
		return float64(sorted[0])
	}
	if rank >= 100 {
		return float64(sorted[n-1])
	}

	// Floor-based index: p50 of 100 elements → index 49 → value 50 (for 1..100 dataset).
	idx := rank * n / 100
	// Clamp to valid range.
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	// Walk back one if we overshot: rank*n must be exactly divisible or we keep floor.
	// For p50 of 100: 50*100/100 = 50, but index 50 gives value 51. Use idx-1 when
	// the multiplication is exact to stay at the boundary value.
	if idx > 0 && rank*n%100 == 0 {
		idx--
	}
	return float64(sorted[idx])
}

// CalculateToolSuccessRate returns the fraction of successful tool calls
// out of all tool call events.
// If total is zero, returns 1.0 (no failures observed).
func CalculateToolSuccessRate(successes, total int) float64 {
	if total == 0 {
		return 1.0
	}
	return float64(successes) / float64(total)
}

// CalculateROIScore computes the efficiency-per-dollar ROI score:
//
//	ROI = tool_success_rate / cost_per_session
//
// This measures how much useful work is done per dollar spent.
// toolSuccessRate is the fraction of successful tool calls (0.0–1.0).
// costPerSession is the average cost per session in USD (total_cost / session_count).
//
// Returns 0 when costPerSession is zero (no cost data available).
func CalculateROIScore(toolSuccessRate, costPerSession float64) float64 {
	if costPerSession <= 0 {
		return 0
	}
	return toolSuccessRate / costPerSession
}

// CalculateAvgLatency returns the arithmetic mean of the provided durations.
// Returns 0 if durations is empty.
func CalculateAvgLatency(durations []int) float64 {
	if len(durations) == 0 {
		return 0
	}
	var sum int64
	for _, d := range durations {
		sum += int64(d)
	}
	return float64(sum) / float64(len(durations))
}

// CalculateErrorRate returns the fraction of error events over total events.
// Returns 0 if total is zero.
func CalculateErrorRate(errors, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}
