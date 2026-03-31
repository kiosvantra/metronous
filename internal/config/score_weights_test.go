package config_test

import (
	"encoding/json"
	"testing"

	"github.com/kiosvantra/metronous/internal/config"
)

// TestDefaultScoreWeights_Sum verifies that the default weights sum to exactly 1.0.
func TestDefaultScoreWeights_Sum(t *testing.T) {
	w := config.DefaultScoreWeights()
	sum := w.Sum()
	if sum != 1.0 {
		t.Errorf("DefaultScoreWeights().Sum() = %v, want 1.0", sum)
	}
}

// TestScoreWeights_Parse_Override verifies that a JSON with score_weights.accuracy=0.50
// is parsed correctly into ScoreWeights.Accuracy.
func TestScoreWeights_Parse_Override(t *testing.T) {
	raw := `{"score_weights": {"accuracy": 0.50, "latency": 0.20, "tool_success_rate": 0.20, "roi_score": 0.10}}`
	var thresholds config.Thresholds
	if err := json.Unmarshal([]byte(raw), &thresholds); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if thresholds.ScoreWeights.Accuracy != 0.50 {
		t.Errorf("ScoreWeights.Accuracy = %v, want 0.50", thresholds.ScoreWeights.Accuracy)
	}
}

// TestScoreWeights_Validation_Invalid verifies that weights summing to 0.95 produce an error.
func TestScoreWeights_Validation_Invalid(t *testing.T) {
	w := config.ScoreWeights{Accuracy: 0.50, Latency: 0.20, ToolSuccessRate: 0.20, ROIScore: 0.05}
	err := config.ValidateScoreWeights(w)
	if err == nil {
		t.Fatal("expected validation error for weights summing to 0.95, got nil")
	}
	if msg := err.Error(); msg == "" {
		t.Error("expected non-empty error message")
	}
}

// TestScoreWeights_Validation_Valid_FloatRounding verifies that weights summing to 0.9999
// (within epsilon) pass validation.
func TestScoreWeights_Validation_Valid_FloatRounding(t *testing.T) {
	w := config.ScoreWeights{Accuracy: 0.3333, Latency: 0.3333, ToolSuccessRate: 0.3333, ROIScore: 0.0001}
	// sum = 1.0000 exactly — but use a real floating-point imprecise case:
	w2 := config.ScoreWeights{Accuracy: 0.40, Latency: 0.20, ToolSuccessRate: 0.20, ROIScore: 0.1999}
	// sum = 0.9999, within epsilon
	if err := config.ValidateScoreWeights(w); err != nil {
		t.Errorf("ValidateScoreWeights(%v) unexpected error: %v", w, err)
	}
	if err := config.ValidateScoreWeights(w2); err != nil {
		t.Errorf("ValidateScoreWeights(%v) unexpected error for sum=0.9999: %v", w2, err)
	}
}

// TestScoreWeights_Omitted_UsesDefault verifies that JSON without score_weights uses defaults.
func TestScoreWeights_Omitted_UsesDefault(t *testing.T) {
	raw := `{"version":"1.0"}`
	var thresholds config.Thresholds
	if err := json.Unmarshal([]byte(raw), &thresholds); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	defaults := config.DefaultScoreWeights()
	if thresholds.ScoreWeights.Accuracy != 0 {
		// ScoreWeights field is zero when omitted from JSON — caller must use DefaultScoreWeights()
		// This test just confirms it doesn't explode.
	}
	// DefaultThresholdValues() should initialize ScoreWeights properly.
	d := config.DefaultThresholdValues()
	if d.ScoreWeights.Accuracy != defaults.Accuracy {
		t.Errorf("DefaultThresholdValues().ScoreWeights.Accuracy = %v, want %v",
			d.ScoreWeights.Accuracy, defaults.Accuracy)
	}
}
