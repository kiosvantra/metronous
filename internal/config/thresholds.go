// Package config provides configuration types and loading utilities for Metronous.
package config

// DefaultThresholds defines the baseline performance thresholds applied to all
// agents unless overridden by per-agent settings.
type DefaultThresholds struct {
	// MinAccuracy is the minimum required task accuracy (0.0–1.0). Default: 0.85.
	MinAccuracy float64 `json:"min_accuracy"`

	// MaxLatencyP95Ms is the maximum allowed P95 latency in milliseconds. Default: 30000.
	MaxLatencyP95Ms int `json:"max_latency_p95_ms"`

	// MinToolSuccessRate is the minimum required tool call success rate (0.0–1.0). Default: 0.90.
	MinToolSuccessRate float64 `json:"min_tool_success_rate"`

	// MinROIScore is the minimum acceptable ROI score. Default: 3.0.
	MinROIScore float64 `json:"min_roi_score"`

	// MaxCostUSDPerSession is the maximum allowed cost per session in USD. Default: 0.50.
	MaxCostUSDPerSession float64 `json:"max_cost_usd_per_session"`
}

// UrgentTriggers defines critical-failure thresholds that trigger an immediate
// URGENT_SWITCH recommendation regardless of other metrics.
type UrgentTriggers struct {
	// MinAccuracy is the floor accuracy below which an urgent switch is triggered. Default: 0.60.
	MinAccuracy float64 `json:"min_accuracy"`

	// MaxErrorRate is the maximum tolerated error rate before urgent action. Default: 0.30.
	MaxErrorRate float64 `json:"max_error_rate"`

	// MaxCostSpikeMultiplier is the allowed cost multiple vs. baseline before alerting. Default: 3.0.
	MaxCostSpikeMultiplier float64 `json:"max_cost_spike_multiplier"`
}

// AgentThresholds allows per-agent overrides of the default thresholds.
// Only fields set to non-zero values override the defaults.
type AgentThresholds struct {
	// MinAccuracy overrides DefaultThresholds.MinAccuracy for this agent.
	MinAccuracy *float64 `json:"min_accuracy,omitempty"`

	// MaxLatencyP95Ms overrides DefaultThresholds.MaxLatencyP95Ms for this agent.
	MaxLatencyP95Ms *int `json:"max_latency_p95_ms,omitempty"`

	// MinToolSuccessRate overrides DefaultThresholds.MinToolSuccessRate for this agent.
	MinToolSuccessRate *float64 `json:"min_tool_success_rate,omitempty"`

	// MinROIScore overrides DefaultThresholds.MinROIScore for this agent.
	MinROIScore *float64 `json:"min_roi_score,omitempty"`

	// MaxCostUSDPerSession overrides DefaultThresholds.MaxCostUSDPerSession for this agent.
	MaxCostUSDPerSession *float64 `json:"max_cost_usd_per_session,omitempty"`
}

// Thresholds is the root configuration structure loaded from thresholds.json.
type Thresholds struct {
	// Version is the schema version of this configuration file.
	Version string `json:"version"`

	// Defaults applies to all agents unless overridden.
	Defaults DefaultThresholds `json:"defaults"`

	// UrgentTriggers defines critical-failure thresholds.
	UrgentTriggers UrgentTriggers `json:"urgent_triggers"`

	// PerAgent maps agent IDs to agent-specific threshold overrides.
	PerAgent map[string]AgentThresholds `json:"per_agent,omitempty"`
}

// DefaultThresholdValues returns a Thresholds struct populated with the
// recommended default values for a new installation.
func DefaultThresholdValues() Thresholds {
	return Thresholds{
		Version: "1.0",
		Defaults: DefaultThresholds{
			MinAccuracy:          0.85,
			MaxLatencyP95Ms:      30000,
			MinToolSuccessRate:   0.90,
			MinROIScore:          3.0,
			MaxCostUSDPerSession: 0.50,
		},
		UrgentTriggers: UrgentTriggers{
			MinAccuracy:            0.60,
			MaxErrorRate:           0.30,
			MaxCostSpikeMultiplier: 3.0,
		},
		PerAgent: make(map[string]AgentThresholds),
	}
}

// EffectiveThresholds returns the thresholds to apply for a given agent ID,
// merging defaults with any per-agent overrides.
func (t *Thresholds) EffectiveThresholds(agentID string) DefaultThresholds {
	effective := t.Defaults
	override, ok := t.PerAgent[agentID]
	if !ok {
		return effective
	}
	if override.MinAccuracy != nil {
		effective.MinAccuracy = *override.MinAccuracy
	}
	if override.MaxLatencyP95Ms != nil {
		effective.MaxLatencyP95Ms = *override.MaxLatencyP95Ms
	}
	if override.MinToolSuccessRate != nil {
		effective.MinToolSuccessRate = *override.MinToolSuccessRate
	}
	if override.MinROIScore != nil {
		effective.MinROIScore = *override.MinROIScore
	}
	if override.MaxCostUSDPerSession != nil {
		effective.MaxCostUSDPerSession = *override.MaxCostUSDPerSession
	}
	return effective
}
