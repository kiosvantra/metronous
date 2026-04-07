// Package config provides configuration types and loading utilities for Metronous.
package config

// KeymapPreset configures the TUI keybinding preset.
//
// "default" preserves the original Metronous keybindings.
// "nvim" enables additional Vim-style navigation shortcuts (hjkl).
type KeymapPreset string

const (
	// KeymapPresetDefault keeps the original keybindings and does not enable
	// any extra Vim-style shortcuts.
	KeymapPresetDefault KeymapPreset = "default"
	// KeymapPresetNvim enables Vim-style navigation (hjkl) in the TUI.
	KeymapPresetNvim KeymapPreset = "nvim"
)

// DefaultThresholds defines the baseline performance thresholds applied to all
// agents unless overridden by per-agent settings.
type DefaultThresholds struct {
	// MinAccuracy is the minimum required task accuracy (0.0–1.0). Default: 0.85.
	MinAccuracy float64 `json:"min_accuracy"`

	// MinROIScore is the minimum acceptable ROI score (tool_success_rate / cost_per_session).
	// Default: 0.05, representing a minimum efficiency of 0.05 successful tool calls per dollar.
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

// ModelRecommendations defines the model names to recommend for different failure scenarios.
type ModelRecommendations struct {
	// AccuracyModel is the model to recommend for accuracy failures.
	AccuracyModel string `json:"accuracy_model"`
	// PerformanceModel is the model to recommend for ROI or performance failures.
	PerformanceModel string `json:"performance_model"`
	// DefaultModel is the fallback model recommendation.
	DefaultModel string `json:"default_model"`
}

// AgentThresholds allows per-agent overrides of the default thresholds.
// Only fields set to non-zero values override the defaults.
type AgentThresholds struct {
	// MinAccuracy overrides DefaultThresholds.MinAccuracy for this agent.
	MinAccuracy *float64 `json:"min_accuracy,omitempty"`

	// MinROIScore overrides DefaultThresholds.MinROIScore for this agent.
	MinROIScore *float64 `json:"min_roi_score,omitempty"`

	// MaxCostUSDPerSession overrides DefaultThresholds.MaxCostUSDPerSession for this agent.
	MaxCostUSDPerSession *float64 `json:"max_cost_usd_per_session,omitempty"`
}

// ModelPricing holds pricing information for known models.
// A model with price == 0 is considered free and ROI/cost checks are skipped.
type ModelPricing struct {
	// Note is an informational comment about the pricing data.
	Note string `json:"note,omitempty"`

	// Models maps model names to their output price per 1M tokens in USD.
	// A value of 0.0 means the model is free; absent keys are treated as unknown (paid).
	Models map[string]float64 `json:"models,omitempty"`
}

// Thresholds is the root configuration structure loaded from thresholds.json.
type Thresholds struct {
	// Version is the schema version of this configuration file.
	Version string `json:"version"`

	// Defaults applies to all agents unless overridden.
	Defaults DefaultThresholds `json:"defaults"`

	// UrgentTriggers defines critical-failure thresholds.
	UrgentTriggers UrgentTriggers `json:"urgent_triggers"`

	// ModelRecommendations defines the model names to recommend for different failure scenarios.
	ModelRecommendations ModelRecommendations `json:"model_recommendations"`

	// PerAgent maps agent IDs to agent-specific threshold overrides.
	PerAgent map[string]AgentThresholds `json:"per_agent,omitempty"`

	// TrackingDurationSeverity configures the tracking UI duration color bands.
	TrackingDurationSeverity TrackingDurationSeverityConfig `json:"tracking_duration_severity,omitempty"`

	// ModelPricing holds pricing data used to determine whether a model is free.
	// Models with price == 0 have ROI/cost checks skipped in the decision engine.
	ModelPricing ModelPricing `json:"model_pricing,omitempty"`

	// KeymapPreset configures the TUI keybinding preset. When empty or unknown,
	// the default preset is used to preserve historical behaviour.
	KeymapPreset KeymapPreset `json:"keymap_preset,omitempty"`
}

// IsModelFree returns true if the model is explicitly listed in ModelPricing with
// a price of exactly 0. Models not listed in the pricing table are treated as paid.
func (t *Thresholds) IsModelFree(model string) bool {
	if t == nil || t.ModelPricing.Models == nil {
		return false
	}
	price, ok := t.ModelPricing.Models[model]
	return ok && price == 0
}

// DefaultThresholdValues returns a Thresholds struct populated with the
// recommended default values for a new installation.
func DefaultThresholdValues() Thresholds {
	return Thresholds{
		Version: "1.0",
		Defaults: DefaultThresholds{
			MinAccuracy:          0.85,
			MinROIScore:          0.05,
			MaxCostUSDPerSession: 0.50,
		},
		UrgentTriggers: UrgentTriggers{
			MinAccuracy:            0.60,
			MaxErrorRate:           0.30,
			MaxCostSpikeMultiplier: 3.0,
		},
		TrackingDurationSeverity: TrackingDurationSeverityConfig{
			GoodMaxMs: 10000,
			WarnMaxMs: 30000,
		},
		ModelRecommendations: ModelRecommendations{
			AccuracyModel:    "claude-opus-4-5",
			PerformanceModel: "claude-haiku-4-5",
			DefaultModel:     "claude-sonnet-4-5",
		},
		PerAgent:     make(map[string]AgentThresholds),
		KeymapPreset: KeymapPresetDefault,
	}
}

// EffectiveKeymapPreset returns the keymap preset to apply for the TUI.
//
// An empty or unknown value falls back to the default preset so that
// existing thresholds.json files continue to behave exactly as before.
func (t *Thresholds) EffectiveKeymapPreset() KeymapPreset {
	if t == nil {
		return KeymapPresetDefault
	}
	switch t.KeymapPreset {
	case "", KeymapPresetDefault:
		return KeymapPresetDefault
	case KeymapPresetNvim:
		return KeymapPresetNvim
	default:
		// Unknown string — treat as default for forwards compatibility.
		return KeymapPresetDefault
	}
}

// EffectiveModelRecommendations returns the model recommendations to apply.
// Returns default values if not configured.
func (t *Thresholds) EffectiveModelRecommendations() ModelRecommendations {
	if t.ModelRecommendations.AccuracyModel == "" {
		return DefaultThresholdValues().ModelRecommendations
	}
	return t.ModelRecommendations
}

// EffectiveThresholds returns the thresholds to apply for a given agent ID,
// merging defaults with any per-agent overrides.
func (t *Thresholds) EffectiveThresholds(agentID string) DefaultThresholds {
	if t == nil {
		return DefaultThresholds{}
	}
	effective := t.Defaults
	override, ok := t.PerAgent[agentID]
	if !ok {
		return effective
	}
	if override.MinAccuracy != nil {
		effective.MinAccuracy = *override.MinAccuracy
	}
	if override.MinROIScore != nil {
		effective.MinROIScore = *override.MinROIScore
	}
	if override.MaxCostUSDPerSession != nil {
		effective.MaxCostUSDPerSession = *override.MaxCostUSDPerSession
	}
	return effective
}

// TrackingDurationSeverityConfig configures the tracking UI duration color bands.
type TrackingDurationSeverityConfig struct {
	// GoodMaxMs is the upper bound for the green band in the tracking UI.
	GoodMaxMs int `json:"good_max_ms"`

	// WarnMaxMs is the upper bound for the amber band in the tracking UI.
	WarnMaxMs int `json:"warn_max_ms"`
}

// DurationSeverity represents how the tracking UI classifies a displayed duration.
type DurationSeverity int

const (
	DurationSeverityUnknown DurationSeverity = iota
	DurationSeverityGood
	DurationSeverityWarn
	DurationSeverityCritical
)

// String returns a stable lowercase label for the severity band.
func (s DurationSeverity) String() string {
	switch s {
	case DurationSeverityGood:
		return "good"
	case DurationSeverityWarn:
		return "warn"
	case DurationSeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Classify classifies a duration using the tracking UI display bands.
func (t TrackingDurationSeverityConfig) Classify(durationMs float64) DurationSeverity {
	if durationMs <= 0 {
		return DurationSeverityUnknown
	}
	warnLimit := float64(t.WarnMaxMs)
	if warnLimit <= 0 {
		return DurationSeverityGood
	}
	greenLimit := float64(t.GoodMaxMs)
	if greenLimit <= 0 || greenLimit > warnLimit {
		greenLimit = warnLimit / 3.0
		if greenLimit < 1 {
			greenLimit = warnLimit
		}
	}
	if durationMs <= greenLimit {
		return DurationSeverityGood
	}
	if durationMs <= warnLimit {
		return DurationSeverityWarn
	}
	return DurationSeverityCritical
}
