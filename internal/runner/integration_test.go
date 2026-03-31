package runner_test

import (
	"context"
	"math"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// TestEndToEnd_CompositeScore_StoredAndReadBack verifies the full pipeline:
// events → runner → store → retrieve → CompositeScore matches manual calculation.
func TestEndToEnd_CompositeScore_StoredAndReadBack(t *testing.T) {
	ctx := context.Background()
	es, err := sqlitestore.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer bs.Close()

	tmpDir := t.TempDir()
	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Insert 60 tool_call events with known metrics.
	dur := 1000 // 1000ms P95 latency (low)
	cost := 0.01
	quality := 0.9
	toolName := "bash"
	toolSuccess := true
	for i := 0; i < 60; i++ {
		e := store.Event{
			AgentID:      "e2e-agent",
			SessionID:    "session-e2e",
			EventType:    "tool_call",
			Model:        "test-model",
			Timestamp:    time.Now().Add(-time.Duration(i) * time.Minute).UTC(),
			DurationMs:   &dur,
			CostUSD:      &cost,
			QualityScore: &quality,
			ToolName:     &toolName,
			ToolSuccess:  &toolSuccess,
		}
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	run, err := bs.GetLatestRun(ctx, "e2e-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if run == nil {
		t.Fatal("expected a BenchmarkRun, got nil")
	}

	// Verify CompositeScore is in valid range.
	if run.CompositeScore < 0 || run.CompositeScore > 1.0 {
		t.Errorf("CompositeScore = %v, want in [0, 1]", run.CompositeScore)
	}
	if run.CompositeScore == 0 {
		t.Errorf("CompositeScore should be > 0 for non-trivial metrics")
	}

	// Manually compute the expected score to verify consistency.
	weights := engine.ScoreWeights()
	maxLat := engine.EffectiveMaxLatencyP95("e2e-agent")
	expected := benchmark.ComputeCompositeScore(
		benchmark.ScoreInput{
			Accuracy:        run.Accuracy,
			P95LatencyMs:    run.P95LatencyMs,
			ToolSuccessRate: run.ToolSuccessRate,
			ROIScore:        run.ROIScore,
		},
		weights,
		benchmark.ScoreThresholds{MaxLatencyP95Ms: float64(maxLat)},
	)
	if math.Abs(run.CompositeScore-expected) > 1e-9 {
		t.Errorf("CompositeScore = %v, want %v (manual calculation)", run.CompositeScore, expected)
	}
}

