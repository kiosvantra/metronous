package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/enduluc/metronous/internal/benchmark"
	"github.com/enduluc/metronous/internal/config"
	"github.com/enduluc/metronous/internal/store"
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
func (e *DecisionEngine) Evaluate(_ context.Context, m benchmark.WindowMetrics) Verdict {
	// Resolve effective thresholds (merges per-agent overrides).
	effective := e.thresholds.EffectiveThresholds(m.AgentID)
	urgent := e.thresholds.UrgentTriggers

	vt := EvaluateRules(m, effective, urgent)
	reason := BuildReason(vt, m, effective, urgent)
	recommended := recommendModel(vt, m, effective)

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
// Heuristic:
//   - Accuracy or error-rate failures → recommend a stronger/smarter model
//   - Latency or cost failures        → recommend a faster/cheaper model
//   - Both accuracy and latency fail  → accuracy takes precedence (correctness first)
func recommendModel(vt store.VerdictType, m benchmark.WindowMetrics, thresholds config.DefaultThresholds) string {
	if vt != store.VerdictSwitch && vt != store.VerdictUrgentSwitch {
		return ""
	}

	accuracyFailed := m.Accuracy < thresholds.MinAccuracy
	latencyFailed := m.P95LatencyMs > float64(thresholds.MaxLatencyP95Ms)
	roiFailed := m.ROIScore < thresholds.MinROIScore

	// Accuracy issues require a stronger model regardless of other failures.
	if accuracyFailed {
		return "claude-opus-4-5"
	}

	// Latency or cost/ROI issues → cheaper, faster model.
	if latencyFailed || roiFailed {
		return "claude-haiku-4-5"
	}

	// Fallback for other switch triggers (tool success rate, etc.).
	return "claude-sonnet-4-5"
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
