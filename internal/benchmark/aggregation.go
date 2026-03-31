// Package benchmark provides metrics calculation and aggregation for weekly
// benchmark runs.
package benchmark

import (
	"sort"

	"github.com/kiosvantra/metronous/internal/store"
)

// healthScoreWeights define the Opcion B weights for HealthScore computation.
// Higher HealthScore = healthier model assignment across agents.
const (
	weightKeep   = 0.50
	weightSwitch = 0.20
	weightUrgent = 0.30
)

// AggregateStat holds cross-agent aggregated metrics for a single LLM model,
// computed from a set of BenchmarkRun records sharing the same Model field.
type AggregateStat struct {
	// Model is the LLM model identifier shared by all contributing runs.
	Model string

	// AgentCount is the number of distinct agent IDs that contributed runs.
	AgentCount int

	// TotalSampleSize is the sum of SampleSize across all contributing runs.
	// Used as the denominator for all weighted averages.
	TotalSampleSize int

	// WeightedAccuracy is the SampleSize-weighted mean accuracy (0.0–1.0).
	WeightedAccuracy float64

	// WeightedP95LatencyMs is the SampleSize-weighted mean P95 latency in ms.
	WeightedP95LatencyMs float64

	// WeightedToolSuccessRate is the SampleSize-weighted mean tool success rate (0.0–1.0).
	WeightedToolSuccessRate float64

	// TotalCostUSD is the sum of TotalCostUSD across all contributing runs.
	TotalCostUSD float64

	// WeightedROIScore is the SampleSize-weighted mean ROI score.
	WeightedROIScore float64

	// HealthScore is a composite score computed from verdict fractions using
	// Opcion B weights: 0.50*keep + 0.20*switch + 0.30*urgent.
	// INSUFFICIENT_DATA verdicts are excluded from the fraction computation.
	// Range: 0.0–0.50 (all KEEP = 0.50, all URGENT = 0.30).
	HealthScore float64
}

// AggregateWeeklyStats computes per-model AggregateStat values from a slice of
// BenchmarkRun records. Each run's SampleSize is used as its weight for metric
// averages. Runs with SampleSize == 0 still contribute to verdict fractions and
// AgentCount but have zero weight on metric averages.
//
// The returned slice is sorted by Model name (ascending) for stable rendering.
// Returns an empty slice when runs is nil or empty.
func AggregateWeeklyStats(runs []store.BenchmarkRun) []AggregateStat {
	if len(runs) == 0 {
		return nil
	}

	type modelAccum struct {
		agents map[string]struct{}
		// weighted metric accumulators
		sumWeight    float64
		sumAccuracy  float64
		sumP95       float64
		sumToolSR    float64
		sumROI       float64
		totalCost    float64
		totalSamples int
		// verdict counters (INSUFFICIENT_DATA excluded)
		countKeep   int
		countSwitch int
		countUrgent int
		countValid  int // runs with a "real" verdict (non INSUFFICIENT_DATA)
	}

	accums := make(map[string]*modelAccum)

	for _, run := range runs {
		a, ok := accums[run.Model]
		if !ok {
			a = &modelAccum{agents: make(map[string]struct{})}
			accums[run.Model] = a
		}

		// Track distinct agents.
		if run.AgentID != "" {
			a.agents[run.AgentID] = struct{}{}
		}

		w := float64(run.SampleSize)
		a.sumWeight += w
		a.sumAccuracy += run.Accuracy * w
		a.sumP95 += run.P95LatencyMs * w
		a.sumToolSR += run.ToolSuccessRate * w
		a.sumROI += run.ROIScore * w
		a.totalCost += run.TotalCostUSD
		a.totalSamples += run.SampleSize

		// Verdict fractions: exclude INSUFFICIENT_DATA.
		switch run.Verdict {
		case store.VerdictKeep:
			a.countKeep++
			a.countValid++
		case store.VerdictSwitch:
			a.countSwitch++
			a.countValid++
		case store.VerdictUrgentSwitch:
			a.countUrgent++
			a.countValid++
			// VerdictInsufficientData and "" are intentionally excluded.
		}
	}

	stats := make([]AggregateStat, 0, len(accums))
	for model, a := range accums {
		stat := AggregateStat{
			Model:           model,
			AgentCount:      len(a.agents),
			TotalSampleSize: a.totalSamples,
			TotalCostUSD:    a.totalCost,
		}

		// Weighted averages — guard against zero-weight denominator.
		if a.sumWeight > 0 {
			stat.WeightedAccuracy = a.sumAccuracy / a.sumWeight
			stat.WeightedP95LatencyMs = a.sumP95 / a.sumWeight
			stat.WeightedToolSuccessRate = a.sumToolSR / a.sumWeight
			stat.WeightedROIScore = a.sumROI / a.sumWeight
		}

		// HealthScore (Opcion B) — guard against zero-valid-verdict denominator.
		if a.countValid > 0 {
			keepFrac := float64(a.countKeep) / float64(a.countValid)
			switchFrac := float64(a.countSwitch) / float64(a.countValid)
			urgentFrac := float64(a.countUrgent) / float64(a.countValid)
			stat.HealthScore = weightKeep*keepFrac + weightSwitch*switchFrac + weightUrgent*urgentFrac
		}

		stats = append(stats, stat)
	}

	// Sort by model name for stable output.
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Model < stats[j].Model
	})

	return stats
}
