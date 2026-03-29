package config_test

import (
	"encoding/json"
	"testing"

	"github.com/enduluc/metronous/internal/config"
)

// TestThresholdsJSONDecode verifies that a thresholds.json payload decodes
// into the Thresholds struct correctly.
func TestThresholdsJSONDecode(t *testing.T) {
	raw := `{
		"version": "1.0",
		"defaults": {
			"min_accuracy": 0.85,
			"max_latency_p95_ms": 30000,
			"min_tool_success_rate": 0.90,
			"min_roi_score": 0.05,
			"max_cost_usd_per_session": 0.50
		},
		"urgent_triggers": {
			"min_accuracy": 0.60,
			"max_error_rate": 0.30,
			"max_cost_spike_multiplier": 3.0
		},
		"per_agent": {
			"code-agent": {
				"min_accuracy": 0.92
			}
		}
	}`

	var t1 config.Thresholds
	if err := json.Unmarshal([]byte(raw), &t1); err != nil {
		t.Fatalf("failed to decode Thresholds JSON: %v", err)
	}

	if t1.Version != "1.0" {
		t.Errorf("Version: got %q, want %q", t1.Version, "1.0")
	}
	if t1.Defaults.MinAccuracy != 0.85 {
		t.Errorf("Defaults.MinAccuracy: got %v, want 0.85", t1.Defaults.MinAccuracy)
	}
	if t1.Defaults.MaxLatencyP95Ms != 30000 {
		t.Errorf("Defaults.MaxLatencyP95Ms: got %v, want 30000", t1.Defaults.MaxLatencyP95Ms)
	}
	if t1.Defaults.MinToolSuccessRate != 0.90 {
		t.Errorf("Defaults.MinToolSuccessRate: got %v, want 0.90", t1.Defaults.MinToolSuccessRate)
	}
	if t1.Defaults.MinROIScore != 0.05 {
		t.Errorf("Defaults.MinROIScore: got %v, want 0.05", t1.Defaults.MinROIScore)
	}
	if t1.Defaults.MaxCostUSDPerSession != 0.50 {
		t.Errorf("Defaults.MaxCostUSDPerSession: got %v, want 0.50", t1.Defaults.MaxCostUSDPerSession)
	}
	if t1.UrgentTriggers.MinAccuracy != 0.60 {
		t.Errorf("UrgentTriggers.MinAccuracy: got %v, want 0.60", t1.UrgentTriggers.MinAccuracy)
	}
	if t1.UrgentTriggers.MaxErrorRate != 0.30 {
		t.Errorf("UrgentTriggers.MaxErrorRate: got %v, want 0.30", t1.UrgentTriggers.MaxErrorRate)
	}
	if t1.UrgentTriggers.MaxCostSpikeMultiplier != 3.0 {
		t.Errorf("UrgentTriggers.MaxCostSpikeMultiplier: got %v, want 3.0", t1.UrgentTriggers.MaxCostSpikeMultiplier)
	}

	// Per-agent override
	codeAgent, ok := t1.PerAgent["code-agent"]
	if !ok {
		t.Fatal("per_agent.code-agent not decoded")
	}
	if codeAgent.MinAccuracy == nil {
		t.Fatal("code-agent.min_accuracy not decoded")
	}
	if *codeAgent.MinAccuracy != 0.92 {
		t.Errorf("code-agent.min_accuracy: got %v, want 0.92", *codeAgent.MinAccuracy)
	}
}

// TestThresholdsDefaultValues verifies DefaultThresholdValues returns correct defaults.
func TestThresholdsDefaultValues(t *testing.T) {
	defaults := config.DefaultThresholdValues()

	if defaults.Version != "1.0" {
		t.Errorf("Version: got %q, want %q", defaults.Version, "1.0")
	}
	if defaults.Defaults.MinAccuracy != 0.85 {
		t.Errorf("MinAccuracy: got %v, want 0.85", defaults.Defaults.MinAccuracy)
	}
	if defaults.UrgentTriggers.MinAccuracy != 0.60 {
		t.Errorf("UrgentTriggers.MinAccuracy: got %v, want 0.60", defaults.UrgentTriggers.MinAccuracy)
	}
}

// TestThresholdsEffectiveThresholdsNoOverride verifies that defaults are returned
// when no per-agent override exists.
func TestThresholdsEffectiveThresholdsNoOverride(t *testing.T) {
	thresholds := config.DefaultThresholdValues()
	effective := thresholds.EffectiveThresholds("unknown-agent")

	if effective.MinAccuracy != 0.85 {
		t.Errorf("MinAccuracy: got %v, want 0.85", effective.MinAccuracy)
	}
}

// TestThresholdsEffectiveThresholdsWithOverride verifies that per-agent overrides
// replace only the specified fields.
func TestThresholdsEffectiveThresholdsWithOverride(t *testing.T) {
	thresholds := config.DefaultThresholdValues()
	minAccuracy := 0.92
	thresholds.PerAgent["code-agent"] = config.AgentThresholds{
		MinAccuracy: &minAccuracy,
	}

	effective := thresholds.EffectiveThresholds("code-agent")

	if effective.MinAccuracy != 0.92 {
		t.Errorf("MinAccuracy: got %v, want 0.92 (override)", effective.MinAccuracy)
	}
	// Other fields should remain at default
	if effective.MinROIScore != 0.05 {
		t.Errorf("MinROIScore should remain at default 0.05, got %v", effective.MinROIScore)
	}
}

// TestThresholdsJSONRoundTrip verifies encode → decode round-trip consistency.
func TestThresholdsJSONRoundTrip(t *testing.T) {
	original := config.DefaultThresholdValues()
	minAcc := 0.95
	original.PerAgent["my-agent"] = config.AgentThresholds{MinAccuracy: &minAcc}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded config.Thresholds
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.Defaults.MinAccuracy != original.Defaults.MinAccuracy {
		t.Errorf("MinAccuracy round-trip mismatch: got %v, want %v",
			decoded.Defaults.MinAccuracy, original.Defaults.MinAccuracy)
	}

	agent, ok := decoded.PerAgent["my-agent"]
	if !ok {
		t.Fatal("per-agent round-trip lost my-agent")
	}
	if agent.MinAccuracy == nil || *agent.MinAccuracy != 0.95 {
		t.Errorf("per-agent MinAccuracy round-trip mismatch")
	}
}
