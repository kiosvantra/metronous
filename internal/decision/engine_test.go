package decision_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/store"
)

// defaultEngine creates a DecisionEngine with default thresholds.
func defaultEngine() *decision.DecisionEngine {
	t := config.DefaultThresholdValues()
	return decision.NewDecisionEngine(&t)
}

// goodMetrics returns metrics that should yield KEEP with default thresholds.
func goodMetrics(agentID string) benchmark.WindowMetrics {
	return benchmark.WindowMetrics{
		AgentID:         agentID,
		Model:           "claude-sonnet",
		SampleSize:      100,
		Accuracy:        0.92,
		ErrorRate:       0.08,
		AvgLatencyMs:    1200,
		P50LatencyMs:    1000,
		P95LatencyMs:    15000,
		P99LatencyMs:    20000,
		ToolSuccessRate: 0.95,
		ROIScore:        0.148, // sdd-apply like: 0.961 / 6.47 ≈ 0.148
		TotalCostUSD:    2.0,
		AvgQuality:      0.9,
	}
}

// --- Task 15: Verdict rule tests ---

// TestEvaluateRulesKeepAllGood verifies KEEP when all metrics exceed thresholds.
func TestEvaluateRulesKeepAllGood(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictKeep {
		t.Errorf("expected VerdictKeep, got %s", vt)
	}
}

// TestVerdictSwitchBelowAccuracy verifies SWITCH when accuracy < 0.85.
func TestVerdictSwitchBelowAccuracy(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.Accuracy = 0.82 // Below MinAccuracy=0.85

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictSwitch {
		t.Errorf("expected VerdictSwitch, got %s", vt)
	}
}

// TestVerdictSwitchHighLatency verifies SWITCH when P95 > 30000ms.
func TestVerdictSwitchHighLatency(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.P95LatencyMs = 35000 // Exceeds MaxLatencyP95Ms=30000

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictSwitch {
		t.Errorf("expected VerdictSwitch, got %s", vt)
	}
}

// TestVerdictSwitchLowToolRate verifies SWITCH when tool_success_rate < 0.90.
func TestVerdictSwitchLowToolRate(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.ToolSuccessRate = 0.88 // Below MinToolSuccessRate=0.90

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictSwitch {
		t.Errorf("expected VerdictSwitch, got %s", vt)
	}
}

// TestVerdictSwitchLowROI verifies SWITCH when ROI < 0.05.
func TestVerdictSwitchLowROI(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.ROIScore = 0.02 // Below MinROIScore=0.05

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictSwitch {
		t.Errorf("expected VerdictSwitch, got %s", vt)
	}
}

// TestVerdictUrgentOnLowAccuracy verifies URGENT_SWITCH when accuracy < 0.60.
func TestVerdictUrgentOnLowAccuracy(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.Accuracy = 0.55 // Below MinAccuracy=0.60 (urgent)
	m.ErrorRate = 0.45

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictUrgentSwitch {
		t.Errorf("expected VerdictUrgentSwitch, got %s", vt)
	}
}

// TestVerdictUrgentOnCriticalErrorRate verifies URGENT_SWITCH when error_rate > 0.30.
func TestVerdictUrgentOnCriticalErrorRate(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.ErrorRate = 0.35 // Above MaxErrorRate=0.30
	m.Accuracy = 0.65  // Above urgent threshold but error rate triggers urgent

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictUrgentSwitch {
		t.Errorf("expected VerdictUrgentSwitch, got %s", vt)
	}
}

// TestVerdictInsufficientData verifies INSUFFICIENT_DATA when sample < 50.
func TestVerdictInsufficientData(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-a")
	m.SampleSize = 49 // Below MinSampleSize=50

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictInsufficientData {
		t.Errorf("expected VerdictInsufficientData, got %s", vt)
	}
}

// TestVerdictTableDriven runs a table-driven test for all verdict types.
func TestVerdictTableDriven(t *testing.T) {
	defaults := config.DefaultThresholdValues()

	tests := []struct {
		name   string
		modify func(*benchmark.WindowMetrics)
		want   store.VerdictType
	}{
		{"keep all good", func(m *benchmark.WindowMetrics) {}, store.VerdictKeep},
		{"insufficient data", func(m *benchmark.WindowMetrics) { m.SampleSize = 10 }, store.VerdictInsufficientData},
		{"urgent low accuracy", func(m *benchmark.WindowMetrics) { m.Accuracy = 0.55; m.ErrorRate = 0.45 }, store.VerdictUrgentSwitch},
		{"urgent high error rate", func(m *benchmark.WindowMetrics) { m.ErrorRate = 0.40; m.Accuracy = 0.65 }, store.VerdictUrgentSwitch},
		{"switch low accuracy", func(m *benchmark.WindowMetrics) { m.Accuracy = 0.80 }, store.VerdictSwitch},
		{"switch high latency", func(m *benchmark.WindowMetrics) { m.P95LatencyMs = 40000 }, store.VerdictSwitch},
		{"switch low tool rate", func(m *benchmark.WindowMetrics) { m.ToolSuccessRate = 0.85 }, store.VerdictSwitch},
		{"switch low roi", func(m *benchmark.WindowMetrics) { m.ROIScore = 0.01 }, store.VerdictSwitch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := goodMetrics("agent-x")
			tc.modify(&m)
			got := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
			if got != tc.want {
				t.Errorf("EvaluateRules: got %s, want %s", got, tc.want)
			}
		})
	}
}

// --- Task 14: DecisionEngine tests ---

// TestEvaluateKeepWithDefaultThresholds verifies Evaluate returns KEEP for good metrics.
func TestEvaluateKeepWithDefaultThresholds(t *testing.T) {
	engine := defaultEngine()
	ctx := context.Background()
	m := goodMetrics("agent-keep")

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictKeep {
		t.Errorf("expected VerdictKeep, got %s", v.Type)
	}
	if v.AgentID != "agent-keep" {
		t.Errorf("AgentID: got %q, want agent-keep", v.AgentID)
	}
	if v.Reason == "" {
		t.Error("Reason should not be empty")
	}
}

// TestEvaluateUsesPerAgentOverride verifies per-agent threshold overrides are applied.
func TestEvaluateUsesPerAgentOverride(t *testing.T) {
	thresholds := config.DefaultThresholdValues()

	// Override: agent "strict-agent" has MinAccuracy=0.95 (stricter than 0.85).
	strictAccuracy := 0.95
	thresholds.PerAgent["strict-agent"] = config.AgentThresholds{
		MinAccuracy: &strictAccuracy,
	}

	engine := decision.NewDecisionEngine(&thresholds)
	ctx := context.Background()

	// Metrics that pass default thresholds (accuracy=0.90 > 0.85) but fail strict override.
	m := goodMetrics("strict-agent")
	m.Accuracy = 0.90 // Passes default 0.85 but fails strict 0.95

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictSwitch {
		t.Errorf("expected VerdictSwitch with per-agent override, got %s", v.Type)
	}
}

// TestEvaluateAllReturnsOneVerdictPerMetric verifies EvaluateAll output count.
func TestEvaluateAllReturnsOneVerdictPerMetric(t *testing.T) {
	engine := defaultEngine()
	ctx := context.Background()

	metrics := []benchmark.WindowMetrics{
		goodMetrics("agent-1"),
		goodMetrics("agent-2"),
		goodMetrics("agent-3"),
	}

	verdicts := engine.EvaluateAll(ctx, metrics)
	if len(verdicts) != 3 {
		t.Errorf("expected 3 verdicts, got %d", len(verdicts))
	}
}

// TestLoadThresholds verifies that a valid JSON file is loaded correctly.
func TestLoadThresholds(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	data, err := json.Marshal(defaults)
	if err != nil {
		t.Fatalf("marshal thresholds: %v", err)
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "thresholds.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write thresholds: %v", err)
	}

	loaded, err := decision.LoadThresholds(path)
	if err != nil {
		t.Fatalf("LoadThresholds: %v", err)
	}

	if loaded.Defaults.MinAccuracy != defaults.Defaults.MinAccuracy {
		t.Errorf("MinAccuracy: got %f, want %f", loaded.Defaults.MinAccuracy, defaults.Defaults.MinAccuracy)
	}
	if loaded.UrgentTriggers.MaxErrorRate != defaults.UrgentTriggers.MaxErrorRate {
		t.Errorf("MaxErrorRate: got %f, want %f", loaded.UrgentTriggers.MaxErrorRate, defaults.UrgentTriggers.MaxErrorRate)
	}
}

// TestLoadThresholdsNotFound verifies error on missing file.
func TestLoadThresholdsNotFound(t *testing.T) {
	_, err := decision.LoadThresholds("/nonexistent/path/thresholds.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// TestIsPendingSwitch verifies the helper function.
func TestIsPendingSwitch(t *testing.T) {
	tests := []struct {
		vt   store.VerdictType
		want bool
	}{
		{store.VerdictSwitch, true},
		{store.VerdictUrgentSwitch, true},
		{store.VerdictKeep, false},
		{store.VerdictInsufficientData, false},
	}
	for _, tc := range tests {
		got := decision.IsPendingSwitch(tc.vt)
		if got != tc.want {
			t.Errorf("IsPendingSwitch(%s) = %v, want %v", tc.vt, got, tc.want)
		}
	}
}

// --- Free-model / ROI suppression tests ---

// thresholdsWithFreeModel builds a Thresholds config that marks "free-model-x" as free
// (price == 0) and "paid-model-y" as paid (price > 0).
func thresholdsWithFreeModel() *config.Thresholds {
	t := config.DefaultThresholdValues()
	t.ModelPricing = config.ModelPricing{
		Models: map[string]float64{
			"free-model-x": 0,
			"paid-model-y": 10.0,
		},
	}
	return &t
}

// freeModelMetrics returns metrics for a free model that are all good quality-wise
// but have a low ROI score that would normally trigger SWITCH.
func freeModelMetrics(agentID string) benchmark.WindowMetrics {
	return benchmark.WindowMetrics{
		AgentID:         agentID,
		Model:           "free-model-x",
		SampleSize:      100,
		Accuracy:        0.92,
		ErrorRate:       0.08,
		AvgLatencyMs:    1200,
		P50LatencyMs:    1000,
		P95LatencyMs:    15000,
		P99LatencyMs:    20000,
		ToolSuccessRate: 0.95,
		ROIScore:        0.001, // Very low — would trigger SWITCH for a paid model
		TotalCostUSD:    0.0,   // Free model: no cost
		AvgQuality:      0.9,
	}
}

// TestFreeModelROINotInfluencesVerdict verifies that a free model with low ROI stays KEEP
// because ROI/cost checks are skipped entirely for free models.
//
// Requirement: model_pricing.models[model] == 0 → ROI must NOT trigger SWITCH/URGENT.
func TestFreeModelROINotInfluencesVerdict(t *testing.T) {
	thresholds := thresholdsWithFreeModel()
	engine := decision.NewDecisionEngine(thresholds)
	ctx := context.Background()

	m := freeModelMetrics("agent-free")
	// Confirm that ROI score is below the threshold — this would normally trigger SWITCH.
	if m.ROIScore >= thresholds.Defaults.MinROIScore {
		t.Fatalf("test precondition: ROIScore %.4f must be below MinROIScore %.4f", m.ROIScore, thresholds.Defaults.MinROIScore)
	}

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictKeep {
		t.Errorf("free model: expected VerdictKeep (ROI ignored), got %s — reason: %s", v.Type, v.Reason)
	}
	if v.RecommendedModel != "" {
		t.Errorf("free model KEEP: RecommendedModel should be empty, got %q", v.RecommendedModel)
	}
}

// TestFreeModelQualityFailureStillSwitches verifies that a free model with poor accuracy
// still produces a SWITCH verdict — quality failures are never suppressed.
func TestFreeModelQualityFailureStillSwitches(t *testing.T) {
	thresholds := thresholdsWithFreeModel()
	engine := decision.NewDecisionEngine(thresholds)
	ctx := context.Background()

	m := freeModelMetrics("agent-free-bad")
	m.Accuracy = 0.70 // Below MinAccuracy=0.85 — quality failure

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictSwitch {
		t.Errorf("free model quality fail: expected VerdictSwitch, got %s", v.Type)
	}
	// Recommendation should be for accuracy (quality), not cost/performance.
	if v.RecommendedModel == "" {
		t.Error("free model quality fail: expected a RecommendedModel, got empty string")
	}
}

// TestFreeModelUrgentQualityFailure verifies that urgent quality failures (accuracy < 0.60)
// still trigger URGENT_SWITCH for free models.
func TestFreeModelUrgentQualityFailure(t *testing.T) {
	thresholds := thresholdsWithFreeModel()
	engine := decision.NewDecisionEngine(thresholds)
	ctx := context.Background()

	m := freeModelMetrics("agent-free-urgent")
	m.Accuracy = 0.50  // Below urgent threshold 0.60
	m.ErrorRate = 0.40 // Also above urgent error rate threshold

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictUrgentSwitch {
		t.Errorf("free model urgent: expected VerdictUrgentSwitch, got %s", v.Type)
	}
}

// TestPaidModelZeroCostROINotInfluences verifies that a paid model with TotalCostUSD==0
// (unreliable cost data) does NOT get a SWITCH triggered by low ROI.
//
// Requirement: paid model + TotalCostUSD==0 → ROI must NOT trigger SWITCH/URGENT.
func TestPaidModelZeroCostROINotInfluences(t *testing.T) {
	thresholds := thresholdsWithFreeModel()
	engine := decision.NewDecisionEngine(thresholds)
	ctx := context.Background()

	m := goodMetrics("agent-paid-zero-cost")
	m.Model = "paid-model-y"
	m.TotalCostUSD = 0     // Unreliable cost data
	m.ROIScore = 0.001     // Very low — would normally trigger SWITCH
	m.Accuracy = 0.92      // Good quality — ensure quality is not the issue
	m.P95LatencyMs = 15000 // Well within latency threshold
	m.ToolSuccessRate = 0.95

	// Confirm ROI is below threshold to validate the test is meaningful.
	if m.ROIScore >= thresholds.Defaults.MinROIScore {
		t.Fatalf("test precondition: ROIScore %.4f must be below MinROIScore %.4f", m.ROIScore, thresholds.Defaults.MinROIScore)
	}

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictKeep {
		t.Errorf("paid model zero cost: expected VerdictKeep (ROI suppressed), got %s — reason: %s", v.Type, v.Reason)
	}
}

// TestPaidModelReliableCostROICanSwitch verifies that a paid model with reliable cost data
// (TotalCostUSD > 0) can trigger a SWITCH when ROI score is below threshold.
//
// Requirement: paid model + TotalCostUSD > 0 → ROI CAN trigger SWITCH when score is low.
func TestPaidModelReliableCostROICanSwitch(t *testing.T) {
	thresholds := thresholdsWithFreeModel()
	engine := decision.NewDecisionEngine(thresholds)
	ctx := context.Background()

	m := goodMetrics("agent-paid-real-cost")
	m.Model = "paid-model-y"
	m.TotalCostUSD = 5.0 // Reliable cost data present
	m.ROIScore = 0.001   // Very low ROI — should trigger SWITCH
	m.Accuracy = 0.92    // Good quality
	m.P95LatencyMs = 15000
	m.ToolSuccessRate = 0.95

	// Confirm ROI is below threshold to validate the test is meaningful.
	if m.ROIScore >= thresholds.Defaults.MinROIScore {
		t.Fatalf("test precondition: ROIScore %.4f must be below MinROIScore %.4f", m.ROIScore, thresholds.Defaults.MinROIScore)
	}

	v := engine.Evaluate(ctx, m)

	if v.Type != store.VerdictSwitch {
		t.Errorf("paid model reliable cost: expected VerdictSwitch (ROI active), got %s — reason: %s", v.Type, v.Reason)
	}
}

// TestBuildReasonWithPricingFreeModelNote verifies that the reason string for KEEP
// includes a note about ROI being ignored for a free model.
func TestBuildReasonWithPricingFreeModelNote(t *testing.T) {
	thresholds := thresholdsWithFreeModel()

	m := freeModelMetrics("agent-reason-test")
	// Ensure all quality metrics pass so we get a KEEP.
	m.Accuracy = 0.92
	m.P95LatencyMs = 15000
	m.ToolSuccessRate = 0.95

	reason := decision.BuildReasonWithPricing(
		store.VerdictKeep,
		m,
		thresholds.Defaults,
		thresholds.UrgentTriggers,
		thresholds,
	)

	if !contains(reason, "free model") {
		t.Errorf("KEEP reason for free model should mention 'free model', got: %q", reason)
	}
}

// TestBuildReasonWithPricingZeroCostNote verifies that the reason string for KEEP
// mentions unreliable cost data when TotalCostUSD==0 and model is paid.
func TestBuildReasonWithPricingZeroCostNote(t *testing.T) {
	thresholds := thresholdsWithFreeModel()

	m := goodMetrics("agent-zero-cost-reason")
	m.Model = "paid-model-y"
	m.TotalCostUSD = 0

	reason := decision.BuildReasonWithPricing(
		store.VerdictKeep,
		m,
		thresholds.Defaults,
		thresholds.UrgentTriggers,
		thresholds,
	)

	if !contains(reason, "unreliable cost") {
		t.Errorf("KEEP reason for zero-cost paid model should mention 'unreliable cost', got: %q", reason)
	}
}

// TestEvaluateRulesBackwardsCompatibility ensures that the original EvaluateRules
// signature (without pricing) still works — ROI is treated as always active
// (old behaviour preserved for callers that don't have pricing data).
func TestEvaluateRulesBackwardsCompatibility(t *testing.T) {
	defaults := config.DefaultThresholdValues()
	m := goodMetrics("agent-compat")
	m.ROIScore = 0.001 // Low ROI — should still trigger SWITCH via old signature

	vt := decision.EvaluateRules(m, defaults.Defaults, defaults.UrgentTriggers)
	if vt != store.VerdictSwitch {
		t.Errorf("backwards compat: EvaluateRules without pricing should trigger SWITCH on low ROI, got %s", vt)
	}
}

// TestFreeModelTableDriven validates multiple free-model scenarios in one pass.
func TestFreeModelTableDriven(t *testing.T) {
	thresholds := thresholdsWithFreeModel()

	tests := []struct {
		name   string
		modify func(*benchmark.WindowMetrics)
		want   store.VerdictType
	}{
		// ROI/cost failures must NOT trigger a switch.
		{"free keep low roi", func(m *benchmark.WindowMetrics) { m.ROIScore = 0.001 }, store.VerdictKeep},
		// Quality failures MUST still trigger.
		{"free switch low accuracy", func(m *benchmark.WindowMetrics) { m.Accuracy = 0.80 }, store.VerdictSwitch},
		{"free switch high latency", func(m *benchmark.WindowMetrics) { m.P95LatencyMs = 40000 }, store.VerdictSwitch},
		{"free switch low tool rate", func(m *benchmark.WindowMetrics) { m.ToolSuccessRate = 0.85 }, store.VerdictSwitch},
		{"free urgent low accuracy", func(m *benchmark.WindowMetrics) { m.Accuracy = 0.50; m.ErrorRate = 0.40 }, store.VerdictUrgentSwitch},
		{"free urgent high error rate", func(m *benchmark.WindowMetrics) { m.ErrorRate = 0.40; m.Accuracy = 0.70 }, store.VerdictUrgentSwitch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := freeModelMetrics("agent-table")
			m.TotalCostUSD = 0 // Free model has no cost
			tc.modify(&m)
			got := decision.EvaluateRulesWithPricing(m, thresholds.Defaults, thresholds.UrgentTriggers, thresholds)
			if got != tc.want {
				t.Errorf("EvaluateRulesWithPricing: got %s, want %s", got, tc.want)
			}
		})
	}
}

// contains is a helper to check substring presence without importing strings in tests.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
