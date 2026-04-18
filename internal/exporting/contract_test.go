package exporting_test

import (
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/exporting"
	"github.com/kiosvantra/metronous/internal/store"
)

func TestBuildContractAppliesSanitizationAndContractShape(t *testing.T) {
	now := time.Date(2026, 4, 18, 4, 0, 0, 0, time.UTC)
	runs := []store.BenchmarkRun{
		{
			AgentID:          "agent-alpha",
			Model:            "claude-sonnet-4-5",
			Verdict:          store.VerdictSwitch,
			RecommendedModel: "claude-haiku-4-5",
			SampleSize:       42,
			Accuracy:         0.91,
			P95LatencyMs:     1234,
			ToolSuccessRate:  0.95,
			ROIScore:         1.2,
			TotalCostUSD:     4.56,
			RunAt:            now,
			DecisionReason:   "contains potentially sensitive free-text",
			ArtifactPath:     "/home/user/.metronous/artifacts/private.json",
		},
	}
	dur := 50
	cost := 0.99
	quality := 0.88
	events := []store.Event{
		{
			AgentID:      "agent-alpha",
			SessionID:    "session-super-sensitive",
			EventType:    "complete",
			Model:        "claude-sonnet-4-5",
			Timestamp:    now,
			DurationMs:   &dur,
			CostUSD:      &cost,
			QualityScore: &quality,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "Verify",
				"api_key":                  "sk-secret",
			},
		},
		{
			AgentID:   "agent-alpha",
			SessionID: "session-2",
			EventType: "complete",
			Model:     "claude-sonnet-4-5",
			Timestamp: now,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "my-private-phase",
			},
		},
	}

	contract := exporting.BuildContract(now, runs, events, "")
	if contract.SchemaVersion != exporting.SchemaVersion {
		t.Fatalf("schema_version mismatch: got %q want %q", contract.SchemaVersion, exporting.SchemaVersion)
	}
	if contract.Provenance.Contract != "sharing-leaderboard" {
		t.Fatalf("expected sharing-leaderboard provenance contract, got %q", contract.Provenance.Contract)
	}
	if contract.Provenance.ConsentMode != "explicit-opt-in" {
		t.Fatalf("expected explicit opt-in provenance consent mode, got %q", contract.Provenance.ConsentMode)
	}
	if err := exporting.ValidateContract(contract); err != nil {
		t.Fatalf("expected generated contract to validate: %v", err)
	}
	if len(contract.BenchmarkRuns) != 1 {
		t.Fatalf("expected 1 benchmark run, got %d", len(contract.BenchmarkRuns))
	}
	if contract.BenchmarkRuns[0].AgentID == "agent-alpha" {
		t.Fatalf("expected agent_id to be sanitized, got raw id %q", contract.BenchmarkRuns[0].AgentID)
	}
	if contract.BenchmarkRuns[0].DecisionReason != "" {
		t.Fatalf("expected decision_reason to be removed from export contract")
	}
	if contract.BenchmarkRuns[0].ArtifactPath != "" {
		t.Fatalf("expected artifact_path to be removed from export contract")
	}
	if len(contract.SemanticPhaseSummary) != 2 {
		t.Fatalf("expected 2 semantic phase rows, got %d", len(contract.SemanticPhaseSummary))
	}
	if contract.SemanticPhaseSummary[0].Phase != "verify" {
		t.Fatalf("expected first phase to be normalized verify, got %q", contract.SemanticPhaseSummary[0].Phase)
	}
	if contract.SemanticPhaseSummary[1].Phase != "custom" {
		t.Fatalf("expected unknown phase to be redacted to custom, got %q", contract.SemanticPhaseSummary[1].Phase)
	}
}

func TestBuildContractDefaultsToNoEgress(t *testing.T) {
	if exporting.ExportDisabledByDefault() != true {
		t.Fatalf("expected export to be disabled by default")
	}
}

func TestValidateContractRejectsUnsanitizedPayload(t *testing.T) {
	contract := exporting.Contract{
		SchemaVersion: exporting.SchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		BenchmarkRuns: []exporting.BenchmarkRunContract{
			{
				AgentID:        "raw-agent-id",
				DecisionReason: "must not leak",
			},
		},
	}
	if err := exporting.ValidateContract(contract); err == nil {
		t.Fatalf("expected validation error for unsanitized payload")
	}
}
