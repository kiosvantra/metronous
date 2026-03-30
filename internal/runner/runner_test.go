package runner_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// setupStores creates in-memory event and benchmark stores for testing.
func setupStores(t *testing.T) (store.EventStore, store.BenchmarkStore) {
	t.Helper()
	es, err := sqlitestore.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() {
		_ = es.Close()
		_ = bs.Close()
	})
	return es, bs
}

// insertEvents inserts n events for the given agent in the last windowDays.
func insertEvents(t *testing.T, ctx context.Context, es store.EventStore, agentID string, n int, eventType string) {
	t.Helper()
	dur := 1000
	cost := 0.01
	quality := 0.9
	toolName := "bash"
	toolSuccess := true

	for i := 0; i < n; i++ {
		e := store.Event{
			AgentID:      agentID,
			SessionID:    "session-x",
			EventType:    eventType,
			Model:        "claude-sonnet",
			Timestamp:    time.Now().Add(-time.Duration(i) * time.Hour).UTC(),
			DurationMs:   &dur,
			CostUSD:      &cost,
			QualityScore: &quality,
		}
		if eventType == "tool_call" {
			e.ToolName = &toolName
			e.ToolSuccess = &toolSuccess
		}
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}
}

// TestRunnerAggregatesAndPersistsWeeklyRun verifies the full pipeline:
// events are fetched, metrics computed, verdict evaluated, and a BenchmarkRun is persisted.
func TestRunnerAggregatesAndPersistsWeeklyRun(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)

	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Insert 60 complete events for one agent (>= MinSampleSize=50).
	insertEvents(t, ctx, es, "pipeline-agent", 60, "complete")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	// Verify a BenchmarkRun was persisted.
	latestRun, err := bs.GetLatestRun(ctx, "pipeline-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if latestRun == nil {
		t.Fatal("expected a BenchmarkRun to be persisted, got nil")
	}

	if latestRun.AgentID != "pipeline-agent" {
		t.Errorf("AgentID: got %q, want pipeline-agent", latestRun.AgentID)
	}
	if latestRun.SampleSize != 60 {
		t.Errorf("SampleSize: got %d, want 60", latestRun.SampleSize)
	}
	if latestRun.Verdict == "" {
		t.Error("Verdict should not be empty")
	}
	if latestRun.Model != "claude-sonnet" {
		t.Errorf("Model: got %q, want claude-sonnet", latestRun.Model)
	}
}

// TestRunnerNoAgentsNoRun verifies that an empty event window produces no runs.
func TestRunnerNoAgentsNoRun(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// No events inserted.
	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly with no events: %v", err)
	}

	agents, err := bs.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 runs, got %d agents", len(agents))
	}
}

// TestRunnerInsufficientDataVerdict verifies that < MinSampleSize events yield INSUFFICIENT_DATA.
func TestRunnerInsufficientDataVerdict(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Insert only 10 events (< MinSampleSize=50).
	insertEvents(t, ctx, es, "small-agent", 10, "complete")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	latestRun, err := bs.GetLatestRun(ctx, "small-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if latestRun == nil {
		t.Fatal("expected a BenchmarkRun, got nil")
	}
	if latestRun.Verdict != store.VerdictInsufficientData {
		t.Errorf("expected VerdictInsufficientData, got %s", latestRun.Verdict)
	}
}

// TestRunnerMultipleAgents verifies that multiple agents are processed independently.
func TestRunnerMultipleAgents(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	insertEvents(t, ctx, es, "agent-alpha", 60, "complete")
	insertEvents(t, ctx, es, "agent-beta", 80, "complete")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	for _, agentID := range []string{"agent-alpha", "agent-beta"} {
		run, err := bs.GetLatestRun(ctx, agentID)
		if err != nil {
			t.Fatalf("GetLatestRun(%q): %v", agentID, err)
		}
		if run == nil {
			t.Fatalf("expected BenchmarkRun for %q, got nil", agentID)
		}
	}
}
