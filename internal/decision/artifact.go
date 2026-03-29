package decision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ArtifactMetrics holds the subset of metrics written to the artifact JSON.
type ArtifactMetrics struct {
	Accuracy        float64 `json:"accuracy"`
	P95LatencyMs    float64 `json:"p95_latency_ms"`
	ToolSuccessRate float64 `json:"tool_success_rate"`
	ROIScore        float64 `json:"roi_score"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	SampleSize      int     `json:"sample_size"`
}

// ArtifactVerdict is a single agent's verdict entry in the artifact JSON.
type ArtifactVerdict struct {
	AgentID          string          `json:"agent_id"`
	CurrentModel     string          `json:"current_model"`
	Verdict          string          `json:"verdict"`
	RecommendedModel string          `json:"recommended_model,omitempty"`
	Reason           string          `json:"reason"`
	Metrics          ArtifactMetrics `json:"metrics"`
}

// Artifact is the root structure written to the decisions_YYYY-MM-DD_HHMMSS.json file.
type Artifact struct {
	GeneratedAt string            `json:"generated_at"`
	WindowDays  int               `json:"window_days"`
	Verdicts    []ArtifactVerdict `json:"verdicts"`
}

// GenerateArtifact serializes the given verdicts to a JSON file in outputDir.
// The file is named decisions_YYYY-MM-DD_HHMMSS.json based on the current UTC
// date and time, ensuring uniqueness across multiple runs on the same day.
// Returns the absolute path to the written file.
func GenerateArtifact(verdicts []Verdict, windowDays int, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return "", fmt.Errorf("create artifact directory %q: %w", outputDir, err)
	}

	now := time.Now().UTC()
	fileName := fmt.Sprintf("decisions_%s_%s.json", now.Format("2006-01-02"), now.Format("150405"))
	filePath := filepath.Join(outputDir, fileName)

	artifact := Artifact{
		GeneratedAt: now.Format(time.RFC3339),
		WindowDays:  windowDays,
		Verdicts:    make([]ArtifactVerdict, 0, len(verdicts)),
	}

	for _, v := range verdicts {
		av := ArtifactVerdict{
			AgentID:          v.AgentID,
			CurrentModel:     v.CurrentModel,
			Verdict:          string(v.Type),
			RecommendedModel: v.RecommendedModel,
			Reason:           v.Reason,
			Metrics: ArtifactMetrics{
				Accuracy:        v.Metrics.Accuracy,
				P95LatencyMs:    v.Metrics.P95LatencyMs,
				ToolSuccessRate: v.Metrics.ToolSuccessRate,
				ROIScore:        v.Metrics.ROIScore,
				TotalCostUSD:    v.Metrics.TotalCostUSD,
				SampleSize:      v.Metrics.SampleSize,
			},
		}
		artifact.Verdicts = append(artifact.Verdicts, av)
	}

	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal artifact: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return "", fmt.Errorf("write artifact %q: %w", filePath, err)
	}

	return filePath, nil
}
