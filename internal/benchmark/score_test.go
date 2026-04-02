package benchmark_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
)

// defaultWeights is a convenience helper for tests.
func defaultWeights() config.ScoreWeights {
	return config.DefaultScoreWeights()
}

// defaultThresholds returns a ScoreThresholds with maxLatencyP95Ms=30000.
func defaultThresholds() benchmark.ScoreThresholds {
	return benchmark.ScoreThresholds{MaxLatencyP95Ms: 30000}
}

// TestComputeCompositeScore_AllMetricsHealthy verifies Scenario 1 from the spec.
// accuracy=0.95, lat=5000, tool=0.98, roi=0.80, maxLat=30000, default weights → ≈0.9027
func TestComputeCompositeScore_AllMetricsHealthy(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        0.95,
		P95LatencyMs:    5000,
		ToolSuccessRate: 0.98,
		ROIScore:        0.80,
	}
	weights := defaultWeights()
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)

	// accuracy_norm = 0.95
	// latency_norm  = 1.0 - (5000/30000) = 0.8333...
	// tool_norm     = 0.98
	// roi_norm      = 0.80
	// score = 0.40*0.95 + 0.20*0.8333 + 0.20*0.98 + 0.20*0.80 ≈ 0.9027
	const want = 0.40*0.95 + 0.20*(1.0-5000.0/30000.0) + 0.20*0.98 + 0.20*0.80
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("ComputeCompositeScore = %v, want %v (diff %v)", got, want, got-want)
	}
}

// TestComputeCompositeScore_LatencyAtThreshold verifies Scenario 2: latency at threshold.
// lat=30000 at maxLat=30000 → latency_norm=0.0, score=0.80
func TestComputeCompositeScore_LatencyAtThreshold(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        1.0,
		P95LatencyMs:    30000,
		ToolSuccessRate: 1.0,
		ROIScore:        1.0,
	}
	weights := defaultWeights()
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)

	// latency_norm = 1.0 - min(30000/30000, 1.0) = 0.0
	// score = 0.40*1 + 0.20*0 + 0.20*1 + 0.20*1 = 0.80
	const want = 0.80
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("ComputeCompositeScore = %v, want %v", got, want)
	}
}

// TestComputeCompositeScore_NegativeROI verifies Scenario 3: roi clamped to 0.
// roi=-0.50, all others=1.0 → score=0.80
func TestComputeCompositeScore_NegativeROI(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        1.0,
		P95LatencyMs:    0,
		ToolSuccessRate: 1.0,
		ROIScore:        -0.50,
	}
	weights := defaultWeights()
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)

	// roi_norm = max(-0.50, 0.0) = 0.0
	// latency_norm = 1.0 - 0/30000 = 1.0
	// score = 0.40*1 + 0.20*1 + 0.20*1 + 0.20*0 = 0.80
	const want = 0.80
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("ComputeCompositeScore = %v, want %v", got, want)
	}
}

// TestComputeCompositeScore_AllZero verifies EC-05: all four normalized inputs 0.0 → score=0.0.
// latency_norm is 0 when P95LatencyMs >= MaxLatencyP95Ms (at or beyond threshold).
func TestComputeCompositeScore_AllZero(t *testing.T) {
	// accuracy=0, tool=0, roi=0, latency at threshold → latency_norm=0
	input := benchmark.ScoreInput{
		Accuracy:        0.0,
		P95LatencyMs:    30000, // at threshold → latency_norm = 0.0
		ToolSuccessRate: 0.0,
		ROIScore:        0.0,
	}
	weights := defaultWeights()
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	if got != 0.0 {
		t.Errorf("ComputeCompositeScore(all normalized=0) = %v, want 0.0", got)
	}
}

// TestComputeCompositeScore_AllOne verifies EC-06: all inputs optimal → score=1.0
func TestComputeCompositeScore_AllOne(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        1.0,
		P95LatencyMs:    0,
		ToolSuccessRate: 1.0,
		ROIScore:        1.0,
	}
	weights := defaultWeights()
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("ComputeCompositeScore(all optimal) = %v, want 1.0", got)
	}
}

// TestComputeCompositeScore_ZeroLatency verifies EC-01: lat=0 → latency_norm=1.0
func TestComputeCompositeScore_ZeroLatency(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        0.0,
		P95LatencyMs:    0,
		ToolSuccessRate: 0.0,
		ROIScore:        0.0,
	}
	weights := config.ScoreWeights{Accuracy: 0, Latency: 1.0, ToolSuccessRate: 0, ROIScore: 0}
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	// Only latency matters; lat=0 → latency_norm=1.0; score = 1.0*1.0 = 1.0
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("ComputeCompositeScore(zero latency, weight=1) = %v, want 1.0", got)
	}
}

// TestComputeCompositeScore_ZeroMaxLatencyThreshold verifies EC-02: maxLat=0 → latency_norm=0.0 (safe fallback)
func TestComputeCompositeScore_ZeroMaxLatencyThreshold(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        0.0,
		P95LatencyMs:    5000,
		ToolSuccessRate: 0.0,
		ROIScore:        0.0,
	}
	weights := config.ScoreWeights{Accuracy: 0, Latency: 1.0, ToolSuccessRate: 0, ROIScore: 0}
	thresholds := benchmark.ScoreThresholds{MaxLatencyP95Ms: 0}

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	// maxLat=0 → latency_norm=0.0 (safe fallback)
	if got != 0.0 {
		t.Errorf("ComputeCompositeScore(maxLat=0) = %v, want 0.0", got)
	}
}

// TestComputeCompositeScore_ROIAboveOne verifies EC-04: roi=1.5 → clamped to 1.0
func TestComputeCompositeScore_ROIAboveOne(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        0.0,
		P95LatencyMs:    0,
		ToolSuccessRate: 0.0,
		ROIScore:        1.5,
	}
	weights := config.ScoreWeights{Accuracy: 0, Latency: 0, ToolSuccessRate: 0, ROIScore: 1.0}
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	// roi=1.5 → clamped to 1.0; score = 1.0 * 1.0 = 1.0
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("ComputeCompositeScore(roi=1.5) = %v, want 1.0", got)
	}
}

// TestComputeCompositeScore_CustomWeights verifies Scenario 7: custom weights.
func TestComputeCompositeScore_CustomWeights(t *testing.T) {
	input := benchmark.ScoreInput{
		Accuracy:        1.0,
		P95LatencyMs:    0, // latency_norm=1.0 but weight=0.10
		ToolSuccessRate: 1.0,
		ROIScore:        1.0,
	}
	weights := config.ScoreWeights{Accuracy: 0.60, Latency: 0.10, ToolSuccessRate: 0.20, ROIScore: 0.10}
	thresholds := defaultThresholds()

	got := benchmark.ComputeCompositeScore(input, weights, thresholds)
	// score = 0.60*1.0 + 0.10*1.0 + 0.20*1.0 + 0.10*1.0 = 1.0
	// But test uses lat_norm=1.0 (lat=0), not 0.0 as in the spec scenario.
	// Spec Scenario 7: accuracy=1.0, latency_norm=0.0, tool=1.0, roi=1.0
	// score = 0.60*1 + 0.10*0 + 0.20*1 + 0.10*1 = 0.90
	input2 := benchmark.ScoreInput{
		Accuracy:        1.0,
		P95LatencyMs:    30000, // latency_norm=0.0 (at threshold)
		ToolSuccessRate: 1.0,
		ROIScore:        1.0,
	}
	got2 := benchmark.ComputeCompositeScore(input2, weights, thresholds)
	const want2 = 0.90
	if math.Abs(got2-want2) > 1e-9 {
		t.Errorf("ComputeCompositeScore(Scenario 7) = %v, want %v", got2, want2)
	}
	_ = got
}

// TestComputeCompositeScore_ResultAlwaysInRange verifies that 50 random valid inputs
// always produce a result in [0.0, 1.0].
func TestComputeCompositeScore_ResultAlwaysInRange(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	weights := defaultWeights()
	thresholds := defaultThresholds()

	for i := 0; i < 50; i++ {
		input := benchmark.ScoreInput{
			Accuracy:        rng.Float64(),
			P95LatencyMs:    rng.Float64() * 60000,
			ToolSuccessRate: rng.Float64(),
			ROIScore:        rng.Float64()*2 - 0.5, // allow negatives and > 1
		}
		got := benchmark.ComputeCompositeScore(input, weights, thresholds)
		if got < 0.0 || got > 1.0 {
			t.Errorf("iteration %d: ComputeCompositeScore = %v, want in [0.0, 1.0] for input %+v", i, got, input)
		}
	}
}
