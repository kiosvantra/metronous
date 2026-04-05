package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/store"
)

// DecisionEngine applies threshold rules to benchmark metrics and produces verdicts.
type DecisionEngine struct {
	thresholds *config.Thresholds
}

// NewDecisionEngine creates a DecisionEngine using the provided Thresholds config.
func NewDecisionEngine(thresholds *config.Thresholds) *DecisionEngine {
	if thresholds == nil {
		defaults := config.DefaultThresholdValues()
		thresholds = &defaults
	}
	return &DecisionEngine{thresholds: thresholds}
}

// LoadThresholds reads a thresholds.json file from path and returns the parsed Thresholds.
func LoadThresholds(path string) (*config.Thresholds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read thresholds file %q: %w", path, err)
	}
	var t config.Thresholds
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse thresholds file %q: %w", path, err)
	}
	return &t, nil
}

// Evaluate produces a Verdict for the given WindowMetrics using the engine's thresholds.
// Per-agent overrides are applied automatically.
//
// Free-model behaviour: if the model reported in m.Model is listed in model_pricing with
// price == 0, ROI/cost checks are skipped — only quality metrics (accuracy, error rate,
// latency, tool success rate) can trigger a SWITCH or URGENT_SWITCH.
//
// Unreliable-cost safeguard: for paid models, if m.TotalCostUSD == 0 (no billing data
// was collected), the ROI check is also suppressed to avoid false positives.
//
// ctx is reserved for future use (e.g., timeout, cancellation).
func (e *DecisionEngine) Evaluate(ctx context.Context, m benchmark.WindowMetrics) Verdict {
	_ = ctx // reserved for future use
	// Resolve effective thresholds (merges per-agent overrides).
	effective := e.thresholds.EffectiveThresholds(m.AgentID)
	urgent := e.thresholds.UrgentTriggers
	models := e.thresholds.EffectiveModelRecommendations()

	vt := EvaluateRulesWithPricing(m, effective, urgent, e.thresholds)
	reason := BuildReasonWithPricing(vt, m, effective, urgent, e.thresholds)
	recommended := recommendModel(vt, m, effective, models, e.thresholds)

	return Verdict{
		AgentID:          m.AgentID,
		CurrentModel:     m.Model,
		Type:             vt,
		Reason:           reason,
		RecommendedModel: recommended,
		Metrics:          m,
	}
}

// recommendModel returns a suggested replacement model based on which thresholds
// failed. Returns an empty string when no switch is needed.
//
// For free models or when cost data is unreliable, ROI failures do not drive the
// recommendation — only quality failures (accuracy, latency, tool success) do.
//
// Heuristic (accuracy-first, cost second):
//   - Accuracy failure → recommend a stronger/smarter model (AccuracyModel)
//   - ROI failure (paid model, reliable cost data) → recommend a cheaper model (PerformanceModel)
//   - Note: latency is not used as a trigger — current duration_ms data is noisy
func recommendModel(vt store.VerdictType, m benchmark.WindowMetrics, thresholds config.DefaultThresholds, models config.ModelRecommendations, root *config.Thresholds) string {
	if vt != store.VerdictSwitch && vt != store.VerdictUrgentSwitch {
		return ""
	}

	accuracyFailed := m.Accuracy < thresholds.MinAccuracy

	// ROI is only considered when the model is paid AND cost data is reliable.
	roiFailed := roiActive(m.Model, m, root) && m.ROIScore < thresholds.MinROIScore

	// Accuracy issues require a stronger/smarter model.
	if accuracyFailed {
		return models.AccuracyModel
	}

	// ROI failure → cheaper model that delivers similar accuracy at lower cost.
	if roiFailed {
		return models.PerformanceModel
	}

	// Fallback.
	return models.DefaultModel
}

// EvaluateAll evaluates multiple agents' metrics, returning one Verdict per agent.
func (e *DecisionEngine) EvaluateAll(ctx context.Context, metrics []benchmark.WindowMetrics) []Verdict {
	verdicts := make([]Verdict, 0, len(metrics))
	for _, m := range metrics {
		verdicts = append(verdicts, e.Evaluate(ctx, m))
	}
	return verdicts
}

// IsPendingSwitch returns true if the verdict requires a model change.
func IsPendingSwitch(v store.VerdictType) bool {
	return v == store.VerdictSwitch || v == store.VerdictUrgentSwitch
}
