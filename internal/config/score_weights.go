package config

import "fmt"

// ScoreWeights holds configurable weights for the composite score formula.
// All weights MUST sum to 1.0; validated at config load time.
type ScoreWeights struct {
	// Accuracy is the weight for the accuracy metric (default 0.40).
	Accuracy float64 `json:"accuracy"`

	// Latency is the weight for the normalized latency metric (default 0.20).
	Latency float64 `json:"latency"`

	// ToolSuccessRate is the weight for the tool success rate metric (default 0.20).
	ToolSuccessRate float64 `json:"tool_success_rate"`

	// ROIScore is the weight for the ROI score metric (default 0.20).
	ROIScore float64 `json:"roi_score"`
}

// DefaultScoreWeights returns the recommended default weights.
// The weights sum to exactly 1.0.
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{
		Accuracy:        0.40,
		Latency:         0.20,
		ToolSuccessRate: 0.20,
		ROIScore:        0.20,
	}
}

// Sum returns the sum of all weights.
func (w ScoreWeights) Sum() float64 {
	return w.Accuracy + w.Latency + w.ToolSuccessRate + w.ROIScore
}

// ValidateScoreWeights returns an error if the weights do not sum to 1.0
// within an epsilon tolerance of 0.001 (to handle floating-point rounding).
func ValidateScoreWeights(w ScoreWeights) error {
	const epsilon = 0.001
	sum := w.Sum()
	if sum < 1.0-epsilon || sum > 1.0+epsilon {
		return fmt.Errorf("score_weights must sum to 1.0 (got %.4f)", sum)
	}
	return nil
}
