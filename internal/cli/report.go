package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/enduluc/metronous/internal/store"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

// NewReportCommand creates the `metronous report` cobra command.
func NewReportCommand() *cobra.Command {
	var agentID string
	var format string
	var dataDir string

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Display the latest benchmark results",
		Long: `Display the latest benchmark runs for one or all agents.

Benchmark runs are stored in benchmark.db after each weekly run.
Use --format=json for machine-readable output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(dataDir, agentID, format)
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "",
		"Filter report to a specific agent ID (optional)")
	cmd.Flags().StringVar(&format, "format", "table",
		"Output format: table or json")
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")

	return cmd
}

// runReport opens the benchmark store and prints results to stdout.
func runReport(dataDir, agentID, format string) error {
	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")

	bs, err := sqlitestore.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark.db: %w", err)
	}
	defer func() { _ = bs.Close() }()

	ctx := context.Background()

	runs, err := bs.GetRuns(ctx, agentID, 0)
	if err != nil {
		return fmt.Errorf("query benchmark runs: %w", err)
	}

	if len(runs) == 0 {
		if agentID != "" {
			fmt.Fprintf(os.Stdout, "No benchmark runs found for agent %q.\n", agentID)
		} else {
			fmt.Fprintln(os.Stdout, "No benchmark runs found. Run 'metronous server' and wait for the weekly benchmark.")
		}
		return nil
	}

	switch strings.ToLower(format) {
	case "json":
		return printJSON(runs)
	default:
		return printTable(runs)
	}
}

// printTable renders benchmark runs in a human-friendly tabular format.
func printTable(runs []store.BenchmarkRun) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tVERDICT\tMODEL\tSAMPLES\tACCURACY\tP95(ms)\tTOOL_RATE\tROI\tRUN_AT")
	fmt.Fprintln(w, "─────\t───────\t─────\t───────\t────────\t───────\t─────────\t───\t──────")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%.2f\t%.0f\t%.2f\t%.2f\t%s\n",
			r.AgentID,
			r.Verdict,
			r.Model,
			r.SampleSize,
			r.Accuracy,
			r.P95LatencyMs,
			r.ToolSuccessRate,
			r.ROIScore,
			r.RunAt.Format("2006-01-02 15:04"),
		)
	}
	return w.Flush()
}

// reportJSON is the JSON output structure for a single run.
type reportJSON struct {
	AgentID          string  `json:"agent_id"`
	Verdict          string  `json:"verdict"`
	Model            string  `json:"model"`
	RecommendedModel string  `json:"recommended_model,omitempty"`
	SampleSize       int     `json:"sample_size"`
	Accuracy         float64 `json:"accuracy"`
	P95LatencyMs     float64 `json:"p95_latency_ms"`
	ToolSuccessRate  float64 `json:"tool_success_rate"`
	ROIScore         float64 `json:"roi_score"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	DecisionReason   string  `json:"decision_reason"`
	RunAt            string  `json:"run_at"`
	ArtifactPath     string  `json:"artifact_path,omitempty"`
}

// printJSON renders benchmark runs as a JSON array.
func printJSON(runs []store.BenchmarkRun) error {
	out := make([]reportJSON, 0, len(runs))
	for _, r := range runs {
		out = append(out, reportJSON{
			AgentID:          r.AgentID,
			Verdict:          string(r.Verdict),
			Model:            r.Model,
			RecommendedModel: r.RecommendedModel,
			SampleSize:       r.SampleSize,
			Accuracy:         r.Accuracy,
			P95LatencyMs:     r.P95LatencyMs,
			ToolSuccessRate:  r.ToolSuccessRate,
			ROIScore:         r.ROIScore,
			TotalCostUSD:     r.TotalCostUSD,
			DecisionReason:   r.DecisionReason,
			RunAt:            r.RunAt.Format("2006-01-02T15:04:05Z"),
			ArtifactPath:     r.ArtifactPath,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
