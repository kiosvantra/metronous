//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/config"
	"github.com/enduluc/metronous/internal/decision"
	"github.com/enduluc/metronous/internal/runner"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

func main() {
	dataDir := os.Getenv("METRONOUS_DATA_DIR")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".metronous", "data")
	}

	logger, _ := zap.NewDevelopment()

	// Open stores
	eventStore, err := sqlitestore.NewEventStore(filepath.Join(dataDir, "tracking.db"))
	if err != nil {
		log.Fatal("open event store:", err)
	}
	defer eventStore.Close()

	benchmarkStore, err := sqlitestore.NewBenchmarkStore(filepath.Join(dataDir, "benchmark.db"))
	if err != nil {
		log.Fatal("open benchmark store:", err)
	}
	defer benchmarkStore.Close()

	// Load thresholds from thresholds.json (edited via TUI Config tab)
	// Falls back to defaults if file not found
	thresholdsPath := filepath.Join(dataDir, "..", "thresholds.json")
	thresholds, err := decision.LoadThresholds(thresholdsPath)
	if err != nil {
		log.Printf("⚠️  Could not load thresholds.json (%v), using defaults", err)
		defaults := config.DefaultThresholdValues()
		thresholds = &defaults
	} else {
		fmt.Printf("📋 Thresholds loaded from: %s\n", thresholdsPath)
	}
	engine := decision.NewDecisionEngine(thresholds)

	// Create runner
	r := runner.NewRunner(eventStore, benchmarkStore, engine, dataDir, logger)

	// Run benchmark with 7-day window
	ctx := context.Background()
	fmt.Println("🚀 Running manual benchmark with real data...")
	fmt.Printf("📁 Data directory: %s\n\n", dataDir)

	if err := r.RunWeekly(ctx, 7); err != nil {
		log.Fatal("❌ Benchmark run failed:", err)
	}

	fmt.Println("✅ Benchmark completed successfully!")

	// Show latest runs
	runs, err := benchmarkStore.GetRuns(ctx, "", 5)
	if err != nil {
		log.Fatal("get runs:", err)
	}

	fmt.Printf("\n📊 Latest %d benchmark run(s):\n", len(runs))
	fmt.Println("─────────────────────────────────────────────────────────────────────────────")
	for i, run := range runs {
		fmt.Printf("%d. Agent: %s\n", i+1, run.AgentID)
		fmt.Printf("   Verdict: %s | Model: %s\n", run.Verdict, run.Model)
		fmt.Printf("   Accuracy: %.2f | P95 Latency: %.0fms | Tool Success: %.2f\n",
			run.Accuracy, run.P95LatencyMs, run.ToolSuccessRate)
		fmt.Printf("   ROI Score: %.2f | Total Cost: $%.4f | Samples: %d\n",
			run.ROIScore, run.TotalCostUSD, run.SampleSize)
		fmt.Printf("   Reason: %s\n", run.DecisionReason)
		if run.RecommendedModel != "" {
			fmt.Printf("   Recommended Model: %s\n", run.RecommendedModel)
		}
		fmt.Println()
	}

	if len(runs) == 0 {
		fmt.Println("   (No benchmark runs found)")
	}
}
