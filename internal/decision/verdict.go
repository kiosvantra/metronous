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

// EvaluateRules applies threshold rules to the given metrics and returns the verdict type.
// Urgent triggers are checked first; then switch triggers; finally KEEP.
func EvaluateRules(m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers) store.VerdictType {
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
	if m.P95LatencyMs > float64(thresholds.MaxLatencyP95Ms) {
		return store.VerdictSwitch
	}
	if m.ToolSuccessRate < thresholds.MinToolSuccessRate {
		return store.VerdictSwitch
	}
	// ROI is reliable only when we have non-zero cost data.
	// When TotalCostUSD==0, treat ROI as non-blocking to avoid noise.
	if m.TotalCostUSD > 0 && m.ROIScore < thresholds.MinROIScore {
		return store.VerdictSwitch
	}

	return store.VerdictKeep
}

// BuildReason constructs a human-readable explanation for a verdict.
// For URGENT_SWITCH and SWITCH verdicts, all failing thresholds are accumulated
// and joined with "; " so users see every issue at once.
func BuildReason(vt store.VerdictType, m benchmark.WindowMetrics, thresholds config.DefaultThresholds, urgent config.UrgentTriggers) string {
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
			failures = append(failures, fmt.Sprintf("Accuracy %.2f below threshold %.2f", m.Accuracy, thresholds.MinAccuracy))
		}
		if m.P95LatencyMs > float64(thresholds.MaxLatencyP95Ms) {
			failures = append(failures, fmt.Sprintf("P95 latency %.0fms exceeds threshold %dms", m.P95LatencyMs, thresholds.MaxLatencyP95Ms))
		}
		if m.ToolSuccessRate < thresholds.MinToolSuccessRate {
			failures = append(failures, fmt.Sprintf("Tool success rate %.2f below threshold %.2f", m.ToolSuccessRate, thresholds.MinToolSuccessRate))
		}
		if m.TotalCostUSD > 0 {
			if m.ROIScore < thresholds.MinROIScore {
				failures = append(failures, fmt.Sprintf("ROI score %.2f below threshold %.2f", m.ROIScore, thresholds.MinROIScore))
			}
		} else {
			// When cost is missing/unreliable (TotalCostUSD==0), ROI thresholds are ignored.
			if m.ROIScore < thresholds.MinROIScore {
				failures = append(failures, fmt.Sprintf("ROI ignored (TotalCostUSD==0; roi=%.2f < %.2f)", m.ROIScore, thresholds.MinROIScore))
			}
		}
		if len(failures) > 0 {
			return strings.Join(failures, "; ")
		}
		return "One or more soft thresholds breached"

	case store.VerdictKeep:
		return fmt.Sprintf("All thresholds passed (accuracy=%.2f, p95=%.0fms, tool_rate=%.2f, roi=%.2f)",
			m.Accuracy, m.P95LatencyMs, m.ToolSuccessRate, m.ROIScore)

	default:
		return "Unknown verdict"
	}
}
