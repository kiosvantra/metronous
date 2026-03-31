package benchmark_test

import (
	"math"
	"testing"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/store"
)

// makeRun is a helper that builds a minimal BenchmarkRun for aggregation tests.
func makeRun(agentID, model string, sampleSize int, accuracy, p95, toolSR, roi, cost float64, verdict store.VerdictType) store.BenchmarkRun {
	return store.BenchmarkRun{
		AgentID:         agentID,
		Model:           model,
		SampleSize:      sampleSize,
		Accuracy:        accuracy,
		P95LatencyMs:    p95,
		ToolSuccessRate: toolSR,
		ROIScore:        roi,
		TotalCostUSD:    cost,
		Verdict:         verdict,
	}
}

// almostEqual returns true when a and b differ by less than eps.
func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// TestAggregateWeeklyStatsEmpty verifies an empty input returns nil.
func TestAggregateWeeklyStatsEmpty(t *testing.T) {
	got := benchmark.AggregateWeeklyStats(nil)
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d elements", len(got))
	}
}

// TestAggregateWeeklyStatsWeightedAverage verifies REQ-02 scenario 1:
// agent-A has 100 events, Accuracy=0.90 and agent-B has 50 events, Accuracy=0.60
// → WeightedAccuracy = (0.90*100 + 0.60*50) / 150 = 0.80
func TestAggregateWeeklyStatsWeightedAverage(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("agent-A", "model-x", 100, 0.90, 200.0, 0.95, 0.10, 1.0, store.VerdictKeep),
		makeRun("agent-B", "model-x", 50, 0.60, 400.0, 0.70, 0.05, 2.0, store.VerdictSwitch),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 AggregateStat (one model), got %d", len(stats))
	}
	s := stats[0]

	if s.Model != "model-x" {
		t.Errorf("Model: got %q, want model-x", s.Model)
	}
	if s.AgentCount != 2 {
		t.Errorf("AgentCount: got %d, want 2", s.AgentCount)
	}
	if s.TotalSampleSize != 150 {
		t.Errorf("TotalSampleSize: got %d, want 150", s.TotalSampleSize)
	}

	// WeightedAccuracy = (0.90*100 + 0.60*50) / 150 = (90+30)/150 = 0.80
	wantAccuracy := (0.90*100 + 0.60*50) / 150.0
	if !almostEqual(s.WeightedAccuracy, wantAccuracy, 1e-9) {
		t.Errorf("WeightedAccuracy: got %f, want %f", s.WeightedAccuracy, wantAccuracy)
	}

	// WeightedP95 = (200*100 + 400*50) / 150
	wantP95 := (200.0*100 + 400.0*50) / 150.0
	if !almostEqual(s.WeightedP95LatencyMs, wantP95, 1e-9) {
		t.Errorf("WeightedP95LatencyMs: got %f, want %f", s.WeightedP95LatencyMs, wantP95)
	}

	// TotalCostUSD = 1.0 + 2.0 = 3.0
	if !almostEqual(s.TotalCostUSD, 3.0, 1e-9) {
		t.Errorf("TotalCostUSD: got %f, want 3.0", s.TotalCostUSD)
	}
}

// TestAggregateWeeklyStatsZeroSampleSizeGuard verifies REQ-02 scenario 2:
// all runs have SampleSize == 0 → weighted metrics all 0, no panic.
func TestAggregateWeeklyStatsZeroSampleSizeGuard(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("agent-A", "model-z", 0, 0.90, 200.0, 0.95, 0.10, 1.0, store.VerdictKeep),
		makeRun("agent-B", "model-z", 0, 0.60, 400.0, 0.70, 0.05, 2.0, store.VerdictSwitch),
	}

	// Must not panic.
	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	s := stats[0]

	if s.WeightedAccuracy != 0 {
		t.Errorf("WeightedAccuracy: got %f, want 0 (zero-weight guard)", s.WeightedAccuracy)
	}
	if s.WeightedP95LatencyMs != 0 {
		t.Errorf("WeightedP95LatencyMs: got %f, want 0", s.WeightedP95LatencyMs)
	}
	if s.WeightedToolSuccessRate != 0 {
		t.Errorf("WeightedToolSuccessRate: got %f, want 0", s.WeightedToolSuccessRate)
	}
	if s.WeightedROIScore != 0 {
		t.Errorf("WeightedROIScore: got %f, want 0", s.WeightedROIScore)
	}
	// TotalSampleSize should still be 0.
	if s.TotalSampleSize != 0 {
		t.Errorf("TotalSampleSize: got %d, want 0", s.TotalSampleSize)
	}
}

// TestAggregateWeeklyStatsSingleAgentDegenerate verifies REQ-02 scenario 3:
// single run → weighted metric equals original metric.
func TestAggregateWeeklyStatsSingleAgentDegenerate(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("agent-solo", "model-solo", 77, 0.88, 350.0, 0.92, 0.15, 5.5, store.VerdictKeep),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	s := stats[0]

	if !almostEqual(s.WeightedAccuracy, 0.88, 1e-9) {
		t.Errorf("WeightedAccuracy: got %f, want 0.88", s.WeightedAccuracy)
	}
	if !almostEqual(s.WeightedP95LatencyMs, 350.0, 1e-9) {
		t.Errorf("WeightedP95LatencyMs: got %f, want 350.0", s.WeightedP95LatencyMs)
	}
	if s.TotalSampleSize != 77 {
		t.Errorf("TotalSampleSize: got %d, want 77", s.TotalSampleSize)
	}
	if s.AgentCount != 1 {
		t.Errorf("AgentCount: got %d, want 1", s.AgentCount)
	}
}

// TestAggregateWeeklyStatsHealthScoreAllKeep verifies REQ-03 scenario 1:
// 4 runs all KEEP → HealthScore = 0.50.
func TestAggregateWeeklyStatsHealthScoreAllKeep(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("a1", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a2", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a3", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a4", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	// HealthScore = 0.50*1.0 + 0.20*0.0 + 0.30*0.0 = 0.50
	want := 0.50
	if !almostEqual(stats[0].HealthScore, want, 1e-9) {
		t.Errorf("HealthScore: got %f, want %f (all KEEP)", stats[0].HealthScore, want)
	}
}

// TestAggregateWeeklyStatsHealthScoreMixed verifies REQ-03 scenario 2:
// 2 KEEP, 1 SWITCH, 1 URGENT_SWITCH → HealthScore = 0.50*(2/4) + 0.20*(1/4) + 0.30*(1/4) = 0.375.
func TestAggregateWeeklyStatsHealthScoreMixed(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("a1", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a2", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a3", "mx", 10, 0.7, 200, 0.7, 0.05, 1.0, store.VerdictSwitch),
		makeRun("a4", "mx", 10, 0.5, 500, 0.5, 0.02, 1.0, store.VerdictUrgentSwitch),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	// HealthScore = 0.50*(2/4) + 0.20*(1/4) + 0.30*(1/4)
	want := 0.50*(2.0/4.0) + 0.20*(1.0/4.0) + 0.30*(1.0/4.0)
	if !almostEqual(stats[0].HealthScore, want, 1e-9) {
		t.Errorf("HealthScore: got %f, want %f (mixed verdicts)", stats[0].HealthScore, want)
	}
}

// TestAggregateWeeklyStatsHealthScoreInsufficientDataExcluded verifies REQ-03 scenario 3:
// 2 KEEP + 2 INSUFFICIENT_DATA → only 2 KEEP count → HealthScore = 0.50.
func TestAggregateWeeklyStatsHealthScoreInsufficientDataExcluded(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("a1", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a2", "mx", 10, 0.9, 100, 0.9, 0.1, 1.0, store.VerdictKeep),
		makeRun("a3", "mx", 5, 0.0, 0.0, 0.0, 0.0, 0.0, store.VerdictInsufficientData),
		makeRun("a4", "mx", 5, 0.0, 0.0, 0.0, 0.0, 0.0, store.VerdictInsufficientData),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	// Only 2 valid runs (both KEEP) → HealthScore = 0.50*(2/2) = 0.50
	want := 0.50
	if !almostEqual(stats[0].HealthScore, want, 1e-9) {
		t.Errorf("HealthScore: got %f, want %f (INSUFFICIENT_DATA excluded)", stats[0].HealthScore, want)
	}
}

// TestAggregateWeeklyStatsMultipleModels verifies that runs for different models
// produce separate AggregateStat entries, sorted by model name.
func TestAggregateWeeklyStatsMultipleModels(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("a1", "model-b", 10, 0.8, 150, 0.8, 0.1, 1.0, store.VerdictKeep),
		makeRun("a2", "model-a", 20, 0.9, 100, 0.9, 0.2, 2.0, store.VerdictKeep),
		makeRun("a3", "model-a", 30, 0.7, 200, 0.7, 0.1, 1.5, store.VerdictSwitch),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 2 {
		t.Fatalf("expected 2 stats (2 models), got %d", len(stats))
	}
	// Sorted alphabetically: model-a first, then model-b.
	if stats[0].Model != "model-a" {
		t.Errorf("stats[0].Model: got %q, want model-a (sorted)", stats[0].Model)
	}
	if stats[1].Model != "model-b" {
		t.Errorf("stats[1].Model: got %q, want model-b (sorted)", stats[1].Model)
	}

	// model-a: 2 agents, 50 total samples.
	if stats[0].AgentCount != 2 {
		t.Errorf("model-a AgentCount: got %d, want 2", stats[0].AgentCount)
	}
	if stats[0].TotalSampleSize != 50 {
		t.Errorf("model-a TotalSampleSize: got %d, want 50", stats[0].TotalSampleSize)
	}
}

// TestAggregateWeeklyStatsAllInsufficientData verifies that when all runs are
// INSUFFICIENT_DATA, HealthScore is 0 (no valid verdicts).
func TestAggregateWeeklyStatsAllInsufficientData(t *testing.T) {
	runs := []store.BenchmarkRun{
		makeRun("a1", "mx", 5, 0, 0, 0, 0, 0, store.VerdictInsufficientData),
		makeRun("a2", "mx", 3, 0, 0, 0, 0, 0, store.VerdictInsufficientData),
	}

	stats := benchmark.AggregateWeeklyStats(runs)

	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	if stats[0].HealthScore != 0 {
		t.Errorf("HealthScore: got %f, want 0 (all INSUFFICIENT_DATA)", stats[0].HealthScore)
	}
}
