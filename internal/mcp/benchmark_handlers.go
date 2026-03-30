package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/enduluc/metronous/internal/decision"
	"github.com/enduluc/metronous/internal/store"
)

// HandleReport handles the "report" MCP tool call.
// It retrieves benchmark runs from the BenchmarkStore, optionally filtered by agent_id.
// Optional parameters:
//   - agent_id: filter to a specific agent (string, optional)
//   - days: return runs from last N days (integer, optional, default 0 = all)
func HandleReport(bs store.BenchmarkStore) ToolHandler {
	return func(ctx context.Context, req CallToolRequest) (*CallToolResult, error) {
		agentID, _ := req.Arguments["agent_id"].(string)

		// Parse optional "days" parameter.
		var since time.Time
		if daysRaw, ok := req.Arguments["days"]; ok {
			days := toInt(daysRaw)
			if days > 0 {
				since = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
			}
		}

		runs, err := bs.GetRuns(ctx, agentID, 0)
		if err != nil {
			return nil, fmt.Errorf("query benchmark runs: %w", err)
		}

		// Apply date filter if since is set.
		if !since.IsZero() {
			filtered := runs[:0]
			for _, r := range runs {
				if r.RunAt.After(since) || r.RunAt.Equal(since) {
					filtered = append(filtered, r)
				}
			}
			runs = filtered
		}

		if len(runs) == 0 {
			return &CallToolResult{
				Content: []ContentItem{TextContent("No benchmark runs found.")},
			}, nil
		}

		text := formatRuns(runs)
		return &CallToolResult{
			Content: []ContentItem{TextContent(text)},
		}, nil
	}
}

// HandleModelChanges handles the "model_changes" MCP tool call.
// It returns only SWITCH and URGENT_SWITCH verdicts.
// Optional parameters:
//   - agent_id: filter to a specific agent (string, optional)
func HandleModelChanges(bs store.BenchmarkStore) ToolHandler {
	return func(ctx context.Context, req CallToolRequest) (*CallToolResult, error) {
		agentID, _ := req.Arguments["agent_id"].(string)

		runs, err := bs.GetRuns(ctx, agentID, 0)
		if err != nil {
			return nil, fmt.Errorf("query benchmark runs: %w", err)
		}

		// Filter to only pending switch verdicts.
		var pending []store.BenchmarkRun
		for _, r := range runs {
			if decision.IsPendingSwitch(r.Verdict) {
				pending = append(pending, r)
			}
		}

		if len(pending) == 0 {
			return &CallToolResult{
				Content: []ContentItem{TextContent("No pending model switches.")},
			}, nil
		}

		text := formatModelChanges(pending)
		return &CallToolResult{
			Content: []ContentItem{TextContent(text)},
		}, nil
	}
}

// RegisterBenchmarkHandlers wires the report and model_changes tools to real handlers.
// This replaces the stub handlers registered by RegisterDefaultTools.
func RegisterBenchmarkHandlers(s *Server, bs store.BenchmarkStore) {
	s.RegisterTool(ReportToolDefinition, HandleReport(bs))
	s.RegisterTool(ModelChangesToolDefinition, HandleModelChanges(bs))
}

// formatRuns formats a slice of BenchmarkRuns as human-readable text.
func formatRuns(runs []store.BenchmarkRun) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Benchmark Runs (%d total)\n", len(runs)))
	sb.WriteString(strings.Repeat("─", 60) + "\n")
	for _, r := range runs {
		sb.WriteString(fmt.Sprintf(
			"Agent: %-20s  Verdict: %-18s  Model: %s\n",
			r.AgentID, r.Verdict, r.Model,
		))
		sb.WriteString(fmt.Sprintf(
			"  RunAt: %s  Window: %dd  Samples: %d\n",
			r.RunAt.Format(time.RFC3339), r.WindowDays, r.SampleSize,
		))
		sb.WriteString(fmt.Sprintf(
			"  Accuracy: %.2f  P95: %.0fms  ToolRate: %.2f  ROI: %.2f  Cost: $%.4f\n",
			r.Accuracy, r.P95LatencyMs, r.ToolSuccessRate, r.ROIScore, r.TotalCostUSD,
		))
		sb.WriteString(fmt.Sprintf("  Reason: %s\n", r.DecisionReason))
		if r.ArtifactPath != "" {
			sb.WriteString(fmt.Sprintf("  Artifact: %s\n", r.ArtifactPath))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatModelChanges formats pending switch verdicts as human-readable text.
func formatModelChanges(runs []store.BenchmarkRun) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Pending Model Changes (%d)\n", len(runs)))
	sb.WriteString(strings.Repeat("─", 60) + "\n")
	for _, r := range runs {
		urgency := ""
		if r.Verdict == store.VerdictUrgentSwitch {
			urgency = " ⚠ URGENT"
		}
		sb.WriteString(fmt.Sprintf(
			"Agent: %-20s  [%s]%s\n",
			r.AgentID, r.Verdict, urgency,
		))
		sb.WriteString(fmt.Sprintf(
			"  Current:     %s\n", r.Model,
		))
		if r.RecommendedModel != "" {
			sb.WriteString(fmt.Sprintf(
				"  Recommended: %s\n", r.RecommendedModel,
			))
		}
		sb.WriteString(fmt.Sprintf("  Reason: %s\n", r.DecisionReason))
		if r.ArtifactPath != "" {
			sb.WriteString(fmt.Sprintf("  Artifact: %s\n", r.ArtifactPath))
		}
		sb.WriteString("\n")
	}

	// Append JSON representation for programmatic use.
	type entry struct {
		AgentID          string `json:"agent_id"`
		CurrentModel     string `json:"current_model"`
		Verdict          string `json:"verdict"`
		RecommendedModel string `json:"recommended_model,omitempty"`
		Reason           string `json:"reason"`
		ArtifactPath     string `json:"artifact_path,omitempty"`
	}
	entries := make([]entry, 0, len(runs))
	for _, r := range runs {
		entries = append(entries, entry{
			AgentID:          r.AgentID,
			CurrentModel:     r.Model,
			Verdict:          string(r.Verdict),
			RecommendedModel: r.RecommendedModel,
			Reason:           r.DecisionReason,
			ArtifactPath:     r.ArtifactPath,
		})
	}
	jsonData, _ := json.MarshalIndent(entries, "", "  ")
	sb.WriteString("JSON:\n")
	sb.Write(jsonData)
	sb.WriteString("\n")
	return sb.String()
}

// toInt converts an interface{} value to int.
// Handles float64 (from JSON unmarshalling) and int.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
