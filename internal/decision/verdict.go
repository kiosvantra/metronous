// Package decision implements the decision engine that evaluates benchmark metrics
// against configurable thresholds and produces KEEP/SWITCH/URGENT_SWITCH/INSUFFICIENT_DATA
// verdicts for each agent.
package decision

import (
	"fmt"
	"strings"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/store"
)

// Verdict holds the decision engine's recommendation for a single agent.
type Verdict struct {
	// AgentID is the agent this verdict applies to.
	AgentID string

	// CurrentModel is the model the agent was using during the window.
	CurrentModel string

	// Type is the verdict classification.
	Type store.VerdictType

	// RecommendedModel is the suggested replacement (empty for KEEP/INSUFFICIENT_DATA).
	RecommendedModel string

	// Reason is a human-readable explanation of the verdict.
	Reason string

	// Metrics is the WindowMetrics used to derive the verdict.
	Metrics benchmark.WindowMetrics
}

// roiActive returns true when the ROI/cost rule should participate in the decision.
//
// ROI is suppressed when either:
//  1. The model is free (price == 0 in model_pricing) — quality is the only axis that
//     matters for free models because there is no cost to optimize.
//  2. The cost data is unreliable — TotalCostUSD == 0 means no real billing data was
//     collected, so an ROI score derived from it would be meaningless.
func roiActive(model string, m benchmark.WindowMetrics, thresholds *config.Thresholds) bool {
	if thresholds.IsModelFree(model) {
		return false
	}
	// Paid model but cost data is unreliable — suppress ROI to avoid false positives.
	if m.TotalCostUSD == 0 {
		return false
	}
	return true
}

// EvaluateRules applies threshold rules to the given metrics and returns the verdict type.
// Urgent triggers are checked first; then switch triggers; finally KEEP.
//
// For free models (price == 0) or when cost data is unreliable (TotalCostUSD == 0),
// the ROI check is skipped so that only quality metrics (accuracy and error rate)
// can trigger a SWITCH or URGENT_SWITCH.
func EvaluateRules(m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers) store.VerdictType {
	return EvaluateRulesWithPricing(m, thresholds, urgent, nil)
}

// EvaluateRulesWithPricing is the full-featured variant of EvaluateRules that
// honours the model pricing table when deciding whether ROI participates.
// Pass the root *config.Thresholds (not the flattened DefaultThresholds) so that
// the pricing map is accessible.
//
// Callers that do not have access to the root Thresholds can use EvaluateRules,
// which falls back to the old behaviour (ROI always active).
func EvaluateRulesWithPricing(m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers, root *config.Thresholds) store.VerdictType {
	// Insufficient data check.
	if m.SampleSize < benchmark.MinSampleSize {
		return store.VerdictInsufficientData
	}

	// Urgent triggers (checked first — any one triggers URGENT_SWITCH).
	if m.Accuracy < urgent.MinAccuracy {
		return store.VerdictUrgentSwitch
	}
	if m.ErrorRate > urgent.MaxErrorRate {
		return store.VerdictUrgentSwitch
	}

	// Switch triggers (soft thresholds — any one triggers SWITCH).
	if m.Accuracy < thresholds.MinAccuracy {
		return store.VerdictSwitch
	}
	// NOTE: latency (P95LatencyMs/AvgTurnMs) is intentionally excluded from SWITCH
	// triggers because the current duration_ms data is cumulative session time, not
	// per-call latency. It will be reintroduced once clean latency data is captured.

	// ROI check: only when the model is paid AND cost data is reliable.
	if roiActive(m.Model, m, root) && m.ROIScore < thresholds.MinROIScore {
		return store.VerdictSwitch
	}

	return store.VerdictKeep
}

// BuildReason constructs a human-readable explanation for a verdict.
// For URGENT_SWITCH and SWITCH verdicts, all failing thresholds are accumulated
// and joined with "; " so users see every issue at once.
func BuildReason(vt store.VerdictType, m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers) string {
	return BuildReasonWithPricing(vt, m, thresholds, urgent, nil)
}

// BuildReasonWithPricing is the full-featured variant that includes a note in the
// reason string when ROI is being ignored due to free model or unreliable cost data,
// and surfaces only the metrics that participate in the decision path.
func BuildReasonWithPricing(vt store.VerdictType, m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers, root *config.Thresholds) string {
	roiEnabled := roiActive(m.Model, m, root)

	switch vt {
	case store.VerdictInsufficientData:
		return fmt.Sprintf("Insufficient data: only %d events (minimum %d required)", m.SampleSize, benchmark.MinSampleSize)

	case store.VerdictUrgentSwitch:
		var failures []string
		if m.Accuracy < urgent.MinAccuracy {
			failures = append(failures, fmt.Sprintf("URGENT: Accuracy %.2f below critical threshold %.2f", m.Accuracy, urgent.MinAccuracy))
		}
		if m.ErrorRate > urgent.MaxErrorRate {
			failures = append(failures, fmt.Sprintf("URGENT: Error rate %.2f exceeds critical threshold %.2f", m.ErrorRate, urgent.MaxErrorRate))
		}
		if len(failures) > 0 {
			return strings.Join(failures, "; ")
		}
		return "URGENT: Critical threshold breached"

	case store.VerdictSwitch:
		var failures []string
		if m.Accuracy < thresholds.MinAccuracy {
			failures = append(failures, fmt.Sprintf("Accuracy %.1f%% below threshold %.1f%%",
				m.Accuracy*100, thresholds.MinAccuracy*100))
		}
		if roiEnabled && m.ROIScore < thresholds.MinROIScore {
			failures = append(failures, fmt.Sprintf("ROI score %.2f below threshold %.2f (cost per session too high relative to accuracy)",
				m.ROIScore, thresholds.MinROIScore))
		}
		if len(failures) > 0 {
			return strings.Join(failures, "; ")
		}
		return "One or more thresholds breached"

	case store.VerdictKeep:
		var parts []string
		parts = append(parts, fmt.Sprintf("accuracy=%.1f%%", m.Accuracy*100))
		if m.AvgTurnMs > 0 {
			parts = append(parts, fmt.Sprintf("avg_response=%.1fs", m.AvgTurnMs/1000))
		}
		if roiEnabled {
			parts = append(parts, fmt.Sprintf("roi=%.2f", m.ROIScore))
		} else {
			if root != nil && root.IsModelFree(m.Model) {
				parts = append(parts, "roi=N/A (free model)")
			} else {
				parts = append(parts, "roi=N/A (no billing data)")
			}
		}
		return fmt.Sprintf("All thresholds passed (%s)", strings.Join(parts, ", "))

	default:
		return "Unknown verdict"
	}
}
