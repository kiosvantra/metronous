package decision_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/store"
)

// sampleVerdict builds a Verdict for testing artifact generation.
func sampleVerdict(agentID string, vt store.VerdictType) decision.Verdict {
	return decision.Verdict{
		AgentID:          agentID,
		CurrentModel:     "claude-sonnet-4",
		Type:             vt,
		RecommendedModel: "claude-haiku",
		Reason:           "Accuracy 0.82 below threshold 0.85",
		Metrics: benchmark.WindowMetrics{
			AgentID:         agentID,
			Model:           "claude-sonnet-4",
			SampleSize:      100,
			Accuracy:        0.82,
			P95LatencyMs:    25000,
			ToolSuccessRate: 0.94,
			ROIScore:        0.145,
			TotalCostUSD:    3.5,
		},
	}
}

// TestGenerateArtifactWritesJSONFile verifies that a JSON file is written to the output dir.
func TestGenerateArtifactWritesJSONFile(t *testing.T) {
	tmpDir := t.TempDir()
	verdicts := []decision.Verdict{sampleVerdict("code-agent", store.VerdictSwitch)}

	path, err := decision.GenerateArtifact(verdicts, 7, tmpDir)
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}

	// File must exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("artifact file not found at %q: %v", path, err)
	}

	// File name must match the pattern decisions_YYYY-MM-DD_HHMMSS.json.
	base := filepath.Base(path)
	today := time.Now().UTC().Format("2006-01-02")
	prefix := "decisions_" + today + "_"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, ".json") {
		t.Errorf("artifact filename: got %q, want prefix %q and suffix .json", base, prefix)
	}
}

// TestArtifactContainsReasonsMetricsAndVerdict verifies the artifact JSON structure.
func TestArtifactContainsReasonsMetricsAndVerdict(t *testing.T) {
	tmpDir := t.TempDir()
	verdicts := []decision.Verdict{
		sampleVerdict("code-agent", store.VerdictSwitch),
		sampleVerdict("ops-agent", store.VerdictKeep),
	}

	path, err := decision.GenerateArtifact(verdicts, 7, tmpDir)
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}

	var artifact decision.Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}

	// Validate root fields.
	if artifact.WindowDays != 7 {
		t.Errorf("WindowDays: got %d, want 7", artifact.WindowDays)
	}
	if artifact.GeneratedAt == "" {
		t.Error("GeneratedAt should not be empty")
	}
	// Verify it's a valid RFC3339 timestamp.
	if _, err := time.Parse(time.RFC3339, artifact.GeneratedAt); err != nil {
		t.Errorf("GeneratedAt %q is not RFC3339: %v", artifact.GeneratedAt, err)
	}

	if len(artifact.Verdicts) != 2 {
		t.Fatalf("expected 2 verdicts, got %d", len(artifact.Verdicts))
	}

	// Find the code-agent verdict.
	var codeAgent *decision.ArtifactVerdict
	for i := range artifact.Verdicts {
		if artifact.Verdicts[i].AgentID == "code-agent" {
			codeAgent = &artifact.Verdicts[i]
		}
	}
	if codeAgent == nil {
		t.Fatal("code-agent verdict not found in artifact")
	}

	if codeAgent.Verdict != "SWITCH" {
		t.Errorf("Verdict: got %q, want SWITCH", codeAgent.Verdict)
	}
	if codeAgent.Reason == "" {
		t.Error("Reason should not be empty")
	}
	if !strings.Contains(codeAgent.Reason, "Accuracy") {
		t.Errorf("Reason should mention Accuracy, got: %q", codeAgent.Reason)
	}
	if codeAgent.Metrics.Accuracy != 0.82 {
		t.Errorf("Metrics.Accuracy: got %f, want 0.82", codeAgent.Metrics.Accuracy)
	}
	if codeAgent.Metrics.P95LatencyMs != 25000 {
		t.Errorf("Metrics.P95LatencyMs: got %f, want 25000", codeAgent.Metrics.P95LatencyMs)
	}
	if codeAgent.Metrics.ROIScore != 0.145 {
		t.Errorf("Metrics.ROIScore: got %f, want 0.145", codeAgent.Metrics.ROIScore)
	}
	if codeAgent.RecommendedModel != "claude-haiku" {
		t.Errorf("RecommendedModel: got %q, want claude-haiku", codeAgent.RecommendedModel)
	}
}

// TestGenerateArtifactCreatesOutputDir verifies that a missing outputDir is created.
func TestGenerateArtifactCreatesOutputDir(t *testing.T) {
	tmpDir := t.TempDir()
	newDir := filepath.Join(tmpDir, "deep", "nested", "artifacts")

	path, err := decision.GenerateArtifact([]decision.Verdict{}, 7, newDir)
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("artifact file not found: %v", err)
	}
}

// TestGenerateArtifactEmptyVerdicts verifies that an empty verdict list produces valid JSON.
func TestGenerateArtifactEmptyVerdicts(t *testing.T) {
	tmpDir := t.TempDir()

	path, err := decision.GenerateArtifact(nil, 7, tmpDir)
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}

	var artifact decision.Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}

	if len(artifact.Verdicts) != 0 {
		t.Errorf("expected 0 verdicts, got %d", len(artifact.Verdicts))
	}
}
