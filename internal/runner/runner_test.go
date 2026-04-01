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

// ─── Intraweek runner tests ───────────────────────────────────────────────────

// TestRunnerWeeklyTagsRunKind verifies that RunWeekly persists run_kind=weekly.
func TestRunnerWeeklyTagsRunKind(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	insertEvents(t, ctx, es, "tag-agent", 60, "complete")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "tag-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.RunKind != store.RunKindWeekly {
		t.Errorf("RunKind: got %q, want %q", got.RunKind, store.RunKindWeekly)
	}
	if got.WindowStart.IsZero() {
		t.Error("WindowStart should not be zero for weekly run")
	}
	if got.WindowEnd.IsZero() {
		t.Error("WindowEnd should not be zero for weekly run")
	}
}

// TestRunnerIntraweekTagsRunKind verifies that RunIntraweek persists run_kind=intraweek.
func TestRunnerIntraweekTagsRunKind(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	insertEvents(t, ctx, es, "iw-tag-agent", 60, "complete")

	if err := r.RunIntraweek(ctx, 7); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "iw-tag-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.RunKind != store.RunKindIntraweek {
		t.Errorf("RunKind: got %q, want %q", got.RunKind, store.RunKindIntraweek)
	}
}

// TestRunnerIntraweekUsesLastRunAtPlusOne verifies that RunIntraweek sets the
// window start to lastRunAt+1ms when a prior run exists in the benchmark store.
//
// Strategy: directly save a synthetic "previous" BenchmarkRun with a well-known
// RunAt in the past, then call RunIntraweek and verify the window start is
// exactly lastRunAt+1ms.  We avoid actually processing agents (no events are
// inserted) so the test focuses only on the interval derivation logic.
func TestRunnerIntraweekUsesLastRunAtPlusOne(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Directly save a synthetic "previous" run with a known RunAt in the past.
	// This simulates a weekly run that happened 3 days ago.
	lastRunAt := time.Now().UTC().Add(-3 * 24 * time.Hour).Truncate(time.Millisecond)
	prevRun := store.BenchmarkRun{
		RunAt:      lastRunAt,
		RunKind:    store.RunKindWeekly,
		WindowDays: 7,
		AgentID:    "synthetic-agent",
		Model:      "claude-sonnet",
		Verdict:    store.VerdictKeep,
	}
	if err := bs.SaveRun(ctx, prevRun); err != nil {
		t.Fatalf("SaveRun (synthetic): %v", err)
	}

	// Insert events for a different agent so there is work for RunIntraweek to do,
	// with timestamps falling within [lastRunAt+1ms, now].
	// We use time.Now() timestamps so they naturally fall in the window.
	insertEvents(t, ctx, es, "iw-interval-agent", 60, "complete")

	beforeIntraweek := time.Now().UTC()
	if err := r.RunIntraweek(ctx, 7); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}
	afterIntraweek := time.Now().UTC()

	// Verify the intraweek run was created for iw-interval-agent.
	iwRun, err := bs.GetLatestRun(ctx, "iw-interval-agent")
	if err != nil {
		t.Fatalf("GetLatestRun after intraweek: %v", err)
	}
	if iwRun == nil {
		t.Fatal("expected intraweek run for iw-interval-agent")
	}

	if iwRun.RunKind != store.RunKindIntraweek {
		t.Errorf("RunKind: got %q, want intraweek", iwRun.RunKind)
	}

	// WindowStart must be lastRunAt+1ms (within 1ms storage precision).
	expectedStart := lastRunAt.Add(time.Millisecond)
	if diff := iwRun.WindowStart.Sub(expectedStart).Abs(); diff > time.Millisecond {
		t.Errorf("WindowStart: got %v, want %v (diff=%v)", iwRun.WindowStart, expectedStart, diff)
	}

	// WindowEnd must be approximately now.
	if iwRun.WindowEnd.Before(beforeIntraweek.Add(-time.Second)) {
		t.Errorf("WindowEnd %v is before the intraweek run started %v", iwRun.WindowEnd, beforeIntraweek)
	}
	if iwRun.WindowEnd.After(afterIntraweek.Add(time.Second)) {
		t.Errorf("WindowEnd %v is after the intraweek run completed %v", iwRun.WindowEnd, afterIntraweek)
	}
}

// TestRunnerIntraweekFallbackWhenNoHistory verifies that when no prior run exists,
// RunIntraweek falls back to the windowDays window (same as weekly).
func TestRunnerIntraweekFallbackWhenNoHistory(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	insertEvents(t, ctx, es, "iw-fallback-agent", 60, "complete")

	beforeRun := time.Now().UTC()
	if err := r.RunIntraweek(ctx, 7); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "iw-fallback-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("expected a run")
	}
	if got.RunKind != store.RunKindIntraweek {
		t.Errorf("RunKind: got %q, want intraweek", got.RunKind)
	}
	// With fallback, WindowStart should be ~7 days before the run (within a second of tolerance).
	expectedStart := beforeRun.Add(-7 * 24 * time.Hour)
	if diff := got.WindowStart.Sub(expectedStart).Abs(); diff > 2*time.Second {
		t.Errorf("WindowStart fallback: got %v, expected near %v (diff=%v)", got.WindowStart, expectedStart, diff)
	}
}
