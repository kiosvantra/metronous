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
	insertEventsWithModel(t, ctx, es, agentID, n, eventType, "claude-sonnet")
}

// insertEventsWithModel inserts n events for the given agent and model.
func insertEventsWithModel(t *testing.T, ctx context.Context, es store.EventStore, agentID string, n int, eventType, model string) {
	t.Helper()
	dur := 1000
	cost := 0.01
	quality := 0.9
	toolName := "bash"
	toolSuccess := true

	for i := 0; i < n; i++ {
		e := store.Event{
			AgentID:      agentID,
			SessionID:    "session-" + model,
			EventType:    eventType,
			Model:        model,
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

// TestRunnerIntraweekUsesFullWeeklyWindow verifies that RunIntraweek always uses
// the full weekly window [now-windowDays, now) regardless of prior runs.
// This ensures F5 accumulates ALL events in the current week, not just
// incremental events since the last run — which would shrink sample counts.
func TestRunnerIntraweekUsesFullWeeklyWindow(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Save a synthetic prior run 3 days ago — this should NOT affect the window.
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

	// Insert events for a test agent.
	insertEvents(t, ctx, es, "iw-interval-agent", 60, "complete")

	beforeIntraweek := time.Now().UTC()
	if err := r.RunIntraweek(ctx, 7); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}
	afterIntraweek := time.Now().UTC()

	// Verify the intraweek run was created.
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

	// WindowStart must be [now-7days, now) — full weekly window, NOT lastRunAt+1ms.
	expectedWindowDuration := 7 * 24 * time.Hour
	actualDuration := iwRun.WindowEnd.Sub(iwRun.WindowStart)
	if diff := (actualDuration - expectedWindowDuration).Abs(); diff > 5*time.Second {
		t.Errorf("window duration: got %v, want ~%v (diff=%v)", actualDuration, expectedWindowDuration, diff)
	}

	// WindowStart must NOT be close to lastRunAt+1ms (that was the old broken behavior).
	oldStart := lastRunAt.Add(time.Millisecond)
	if diff := iwRun.WindowStart.Sub(oldStart).Abs(); diff < time.Hour {
		t.Errorf("WindowStart %v is too close to lastRunAt+1ms %v — old incremental behavior detected", iwRun.WindowStart, oldStart)
	}

	// WindowEnd must be approximately now.
	if iwRun.WindowEnd.Before(beforeIntraweek.Add(-time.Second)) {
		t.Errorf("WindowEnd %v is before the intraweek run started %v", iwRun.WindowEnd, beforeIntraweek)
	}
	if iwRun.WindowEnd.After(afterIntraweek.Add(time.Second)) {
		t.Errorf("WindowEnd %v is after the intraweek run completed %v", iwRun.WindowEnd, afterIntraweek)
	}
}

// TestRunnerGeneratesOneRunPerModelPerAgent verifies that when an agent uses
// multiple models in the window, a separate BenchmarkRun is created for each.
func TestRunnerGeneratesOneRunPerModelPerAgent(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Insert 60 events with model-A and 60 with model-B for the same agent.
	insertEventsWithModel(t, ctx, es, "multi-model-agent", 60, "complete", "model-a")
	insertEventsWithModel(t, ctx, es, "multi-model-agent", 60, "complete", "model-b")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "multi-model-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs (one per model), got %d", len(runs))
	}

	models := make(map[string]bool)
	for _, run := range runs {
		models[run.Model] = true
	}
	if !models["model-a"] {
		t.Error("expected run for model-a")
	}
	if !models["model-b"] {
		t.Error("expected run for model-b")
	}
}

// TestRunnerNormalizesModelPrefixes verifies that provider prefixes are stripped
// so opencode/claude-sonnet and claude-sonnet produce a single merged run.
func TestRunnerNormalizesModelPrefixes(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	// Same model, different prefixes — should be merged into one run.
	insertEventsWithModel(t, ctx, es, "prefix-agent", 40, "complete", "opencode/claude-sonnet")
	insertEventsWithModel(t, ctx, es, "prefix-agent", 30, "complete", "claude-sonnet")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "prefix-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 merged run, got %d: %v", len(runs), runs)
	}
	if runs[0].Model != "claude-sonnet" {
		t.Errorf("expected normalized model claude-sonnet, got %q", runs[0].Model)
	}
	if runs[0].SampleSize != 70 {
		t.Errorf("expected 70 samples (merged), got %d", runs[0].SampleSize)
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
