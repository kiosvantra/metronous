package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// NewBenchmarkCommand creates the `metronous benchmark` cobra command.
func NewBenchmarkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark commands",
		Long:  `Run on-demand benchmarks and inspect agent performance.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run a benchmark immediately",
		Long: `Triggers an on-demand benchmark run using the last 7 days of data.

Analyzes agent performance, evaluates thresholds, and generates decisions.`,
		RunE: runBenchmarkRun,
	})

	return cmd
}

func runBenchmarkRun(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	metronousHome := filepath.Join(home, ".metronous")

	dataDir := os.Getenv("METRONOUS_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(metronousHome, "data")
	}

	logger, err := zap.NewDevelopment()
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	eventStore, err := sqlitestore.NewEventStore(filepath.Join(dataDir, "tracking.db"))
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer eventStore.Close()

	benchmarkStore, err := sqlitestore.NewBenchmarkStore(filepath.Join(dataDir, "benchmark.db"))
	if err != nil {
		return fmt.Errorf("open benchmark store: %w", err)
	}
	defer benchmarkStore.Close()

	// Thresholds live in ~/.metronous/ (not relative to dataDir)
	thresholdsPath := filepath.Join(metronousHome, "thresholds.json")
	thresholds, err := decision.LoadThresholds(thresholdsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load thresholds.json (%v), using defaults\n", err)
		defaults := config.DefaultThresholdValues()
		thresholds = &defaults
	} else {
		fmt.Printf("Thresholds loaded from: %s\n", thresholdsPath)
	}

	engine := decision.NewDecisionEngine(thresholds)
	r := runner.NewRunner(eventStore, benchmarkStore, engine, dataDir, logger)

	fmt.Println("Running benchmark...")
	fmt.Printf("Data directory: %s\n\n", dataDir)

	ctx := context.Background()
	if err := r.RunWeekly(ctx, 7); err != nil {
		return fmt.Errorf("benchmark run failed: %w", err)
	}

	fmt.Println("\nBenchmark completed successfully!")

	runs, err := benchmarkStore.GetRuns(ctx, "", 5)
	if err != nil {
		return fmt.Errorf("get runs: %w", err)
	}

	fmt.Printf("\nLatest %d benchmark run(s):\n", len(runs))
	fmt.Println("----------------------------------------------------------------------")
	for i, run := range runs {
		fmt.Printf("%d. Agent: %s\n", i+1, run.AgentID)
		fmt.Printf("   Verdict: %s | Model: %s\n", run.Verdict, run.Model)
		fmt.Printf("   Accuracy: %.2f | P95 Latency: %.0fms | Tool Success: %.2f\n",
			run.Accuracy, run.P95LatencyMs, run.ToolSuccessRate)
		fmt.Printf("   ROI Score: %.4f | Total Cost: $%.4f | Samples: %d\n",
			run.ROIScore, run.TotalCostUSD, run.SampleSize)
		fmt.Printf("   Reason: %s\n", run.DecisionReason)
		if run.RecommendedModel != "" {
			fmt.Printf("   Recommended Model: %s\n", run.RecommendedModel)
		}
		fmt.Println()
	}

	if len(runs) == 0 {
		fmt.Println("No benchmark runs found.")
	}

	return nil
}
