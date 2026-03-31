package benchmark

import (
	"math"

	"github.com/kiosvantra/metronous/internal/config"
)

// ScoreInput holds the raw metric values used to compute the composite score.
type ScoreInput struct {
	// Accuracy is the fraction of non-error events (0.0–1.0).
	Accuracy float64

	// P95LatencyMs is the 95th-percentile latency in milliseconds.
	P95LatencyMs float64

	// ToolSuccessRate is the fraction of successful tool calls (0.0–1.0).
	ToolSuccessRate float64

	// ROIScore is a quality/cost ratio (can be negative; clamped to [0, 1] for scoring).
	ROIScore float64
}

// ScoreThresholds holds the threshold parameters used for score normalization.
type ScoreThresholds struct {
	// MaxLatencyP95Ms is the latency ceiling for normalization.
	// A P95 latency of 0 maps to 1.0 (perfect); at/above this ceiling maps to 0.0.
	// If this is 0, latency_norm falls back to 0.0 (safe fallback for unconfigured thresholds).
	MaxLatencyP95Ms float64
}

// ComputeCompositeScore calculates a normalized 0–1 composite score from raw metrics.
//
// Normalization rules:
//   - accuracy_norm    = accuracy (already 0–1)
//   - latency_norm     = 1.0 - min(p95 / maxLatP95, 1.0); 0.0 when maxLatP95 == 0
//   - tool_norm        = tool_success_rate (already 0–1)
//   - roi_norm         = min(max(roi, 0.0), 1.0)  — clamp negative to 0, cap at 1
//
// Final score = weighted sum, safety-clamped to [0.0, 1.0].
//
// This is a PURE FUNCTION: deterministic, no I/O, no side effects.
func ComputeCompositeScore(input ScoreInput, weights config.ScoreWeights, thresholds ScoreThresholds) float64 {
	// Normalize accuracy: already in [0, 1].
	accNorm := input.Accuracy

	// Normalize latency: lower is better.
	// 0ms → 1.0 (perfect); at/above threshold → 0.0.
	var latNorm float64
	if thresholds.MaxLatencyP95Ms > 0 {
		latNorm = 1.0 - math.Min(input.P95LatencyMs/thresholds.MaxLatencyP95Ms, 1.0)
	}
	// If MaxLatencyP95Ms == 0: latNorm stays 0.0 (safe fallback — EC-02).

	// Normalize tool success rate: already in [0, 1].
	toolNorm := input.ToolSuccessRate

	// Normalize ROI: clamp negative to 0 (EC-03), cap above 1.0 (EC-04).
	roiNorm := math.Min(math.Max(input.ROIScore, 0.0), 1.0)

	// Weighted sum.
	score := weights.Accuracy*accNorm +
		weights.Latency*latNorm +
		weights.ToolSuccessRate*toolNorm +
		weights.ROIScore*roiNorm

	// Safety clamp — should never be needed with correct normalization.
	return math.Max(0.0, math.Min(score, 1.0))
}
