package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// makeModelLookup returns an AgentModelLookup that maps each agentID to a model
// from the provided map. Agents not in the map return not-found.
func makeModelLookup(m map[string]string) config.AgentModelLookup {
	return func(agentID string) (string, bool) {
		model, ok := m[agentID]
		return model, ok
	}
}

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

type failingQueryEventStore struct{}

func (f failingQueryEventStore) InsertEvent(context.Context, store.Event) (string, error) {
	return "", nil
}
func (f failingQueryEventStore) QueryEvents(context.Context, store.EventQuery) ([]store.Event, error) {
	return nil, errors.New("query failed before benchmark persistence")
}
func (f failingQueryEventStore) CountEvents(context.Context, store.EventQuery) (int, error) {
	return 0, nil
}
func (f failingQueryEventStore) QuerySessions(context.Context, store.SessionQuery) ([]store.SessionSummary, error) {
	return nil, nil
}
func (f failingQueryEventStore) GetSessionEvents(context.Context, string) ([]store.Event, error) {
	return nil, nil
}
func (f failingQueryEventStore) GetAgentEvents(context.Context, string, time.Time) ([]store.Event, error) {
	return nil, nil
}
func (f failingQueryEventStore) GetAgentSummary(context.Context, string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}
func (f failingQueryEventStore) QueryDailyCostByModel(context.Context, time.Time, time.Time) ([]store.DailyCostByModelRow, error) {
	return nil, nil
}
func (f failingQueryEventStore) Close() error { return nil }

// insertEvents inserts n events for the given agent in the last windowDays.
func insertEvents(t *testing.T, ctx context.Context, es store.EventStore, agentID string, n int, eventType string) {
	t.Helper()
	insertEventsWithModel(t, ctx, es, agentID, n, eventType, "claude-sonnet")
}

// insertEventsWithModel inserts n events for the given agent and model.
func insertEventsWithModel(t *testing.T, ctx context.Context, es store.EventStore, agentID string, n int, eventType, model string) {
	t.Helper()
	insertEventsWithModelAt(t, ctx, es, agentID, n, eventType, model, time.Now())
}

// insertEventsWithModelAt inserts n events for the given agent and model, starting at baseTime.
func insertEventsWithModelAt(t *testing.T, ctx context.Context, es store.EventStore, agentID string, n int, eventType, model string, baseTime time.Time) {
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
			Timestamp:    baseTime.Add(-time.Duration(i) * time.Hour).UTC(),
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

// TestRunnerRecordsFailedAttemptBeforeRunPersistence verifies that the operational
// attempt state is written even when the benchmark fails before any runs are saved.
func TestRunnerRecordsFailedAttemptBeforeRunPersistence(t *testing.T) {
	ctx := context.Background()
	es := failingQueryEventStore{}
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() { _ = bs.Close() })
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	if err := r.RunWeekly(ctx, 7); err == nil {
		t.Fatal("expected RunWeekly to fail")
	}

	states, err := bs.GetBenchmarkAttemptStates(ctx)
	if err != nil {
		t.Fatalf("GetBenchmarkAttemptStates: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 attempt state, got %d: %+v", len(states), states)
	}
	state := states[0]
	if state.RunKind != store.RunKindWeekly {
		t.Fatalf("RunKind = %q, want weekly", state.RunKind)
	}
	if state.LastAttemptAt.IsZero() {
		t.Fatal("expected attempt timestamp to be persisted")
	}
	if state.LastAttemptStatus != store.BenchmarkAttemptFailed {
		t.Fatalf("status = %q, want failed", state.LastAttemptStatus)
	}
	if state.LastAttemptError == "" {
		t.Fatal("expected attempt error to be persisted")
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

// TestRunnerIntraweekActiveModelFromWindowNotAllTime verifies that the active model is
// determined from WINDOW events (recent 7 days), not all historical events.
// This ensures that after a model switch the new model is marked active, even if the
// old model has many more total events in history.
func TestRunnerIntraweekActiveModelFromWindowNotAllTime(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, tmpDir, zap.NewNop())

	now := time.Now().UTC()
	windowDays := 7

	// Insert 500 old events for "old-model" (outside the 7-day window) — should NOT influence currentModel.
	oldBase := now.Add(-time.Duration(windowDays+2) * 24 * time.Hour)
	insertEventsWithModelAt(t, ctx, es, "switch-agent", 500, "complete", "old-model", oldBase)

	// Insert 60 recent events for "new-model" (within the 7-day window) — should be currentModel.
	insertEventsWithModelAt(t, ctx, es, "switch-agent", 60, "complete", "opencode/new-model", now)

	if err := r.RunIntraweek(ctx, windowDays); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "switch-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least one run")
	}

	// Find the active run.
	var activeRun *store.BenchmarkRun
	for i := range runs {
		if runs[i].Status == store.RunStatusActive {
			activeRun = &runs[i]
			break
		}
	}
	if activeRun == nil {
		t.Fatal("no active run found — expected new-model to be active")
	}
	if activeRun.Model != "new-model" {
		t.Errorf("active run Model: got %q, want %q (window-based currentModel should win over all-time count)", activeRun.Model, "new-model")
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

// ─── Config-based active model tests ────────────────────────────────────────

// TestRunnerIntraweekActiveModelFromConfig verifies that when an AgentModelLookup
// is provided, the model returned by the lookup is marked 'active', even when a
// different model has more events in the window.
func TestRunnerIntraweekActiveModelFromConfig(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)

	// The lookup says "config-model" is the currently configured model.
	lookup := makeModelLookup(map[string]string{
		"config-agent": "opencode/config-model",
	})
	r := runner.NewRunnerWithModelLookup(es, bs, engine, tmpDir, zap.NewNop(), lookup)

	// Insert 200 events for "old-model" (more events) and 60 for "config-model".
	// Without the fix the heuristic would pick "old-model" as active.
	insertEventsWithModel(t, ctx, es, "config-agent", 200, "complete", "old-model")
	insertEventsWithModel(t, ctx, es, "config-agent", 60, "complete", "opencode/config-model")

	if err := r.RunIntraweek(ctx, 7); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "config-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least one run")
	}

	// Exactly one run should be active — the one for "config-model".
	var activeRun *store.BenchmarkRun
	for i := range runs {
		if runs[i].Status == store.RunStatusActive {
			if activeRun != nil {
				t.Errorf("found more than one active run (models: %q and %q)", activeRun.Model, runs[i].Model)
			}
			activeRun = &runs[i]
		}
	}
	if activeRun == nil {
		t.Fatal("no active run found")
	}
	// Config lookup normalizes "opencode/config-model" → "config-model".
	if activeRun.Model != "config-model" {
		t.Errorf("active run model: got %q, want %q (config lookup should override event-count heuristic)", activeRun.Model, "config-model")
	}
}

// TestRunnerIntraweekActiveModelFallbackWhenNotInConfig verifies that when an
// agent is NOT in the AgentModelLookup, the runner falls back to the window
// heuristic (most events in the 7-day window).
func TestRunnerIntraweekActiveModelFallbackWhenNotInConfig(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)

	// Lookup has no entry for "fallback-agent".
	lookup := makeModelLookup(map[string]string{
		"other-agent": "opencode/some-model",
	})
	r := runner.NewRunnerWithModelLookup(es, bs, engine, tmpDir, zap.NewNop(), lookup)

	now := time.Now().UTC()
	windowDays := 7

	// Insert 500 old events for "old-model" (outside window) and 60 recent events
	// for "recent-model" (inside window). Heuristic should pick "recent-model".
	oldBase := now.Add(-time.Duration(windowDays+2) * 24 * time.Hour)
	insertEventsWithModelAt(t, ctx, es, "fallback-agent", 500, "complete", "old-model", oldBase)
	insertEventsWithModelAt(t, ctx, es, "fallback-agent", 60, "complete", "recent-model", now)

	if err := r.RunIntraweek(ctx, windowDays); err != nil {
		t.Fatalf("RunIntraweek: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "fallback-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}

	var activeRun *store.BenchmarkRun
	for i := range runs {
		if runs[i].Status == store.RunStatusActive {
			activeRun = &runs[i]
			break
		}
	}
	if activeRun == nil {
		t.Fatal("no active run found")
	}
	if activeRun.Model != "recent-model" {
		t.Errorf("active run model: got %q, want %q (heuristic fallback should pick window model)", activeRun.Model, "recent-model")
	}
}

// TestRunnerWeeklyAllRunsActiveWhenConfigProvidesModel verifies that for weekly
// runs, all models are always marked active (weekly runs never mark each other
// superseded in the same cycle — that's handled by cross-cycle logic).
// The config-based currentModel only affects intraweek runs.
func TestRunnerWeeklyAllRunsActiveWhenConfigProvidesModel(t *testing.T) {
	ctx := context.Background()
	es, bs := setupStores(t)
	tmpDir := t.TempDir()

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)

	// Config says "model-a" is current, but for weekly runs both should be active.
	lookup := makeModelLookup(map[string]string{
		"weekly-agent": "model-a",
	})
	r := runner.NewRunnerWithModelLookup(es, bs, engine, tmpDir, zap.NewNop(), lookup)

	insertEventsWithModel(t, ctx, es, "weekly-agent", 60, "complete", "model-a")
	insertEventsWithModel(t, ctx, es, "weekly-agent", 60, "complete", "model-b")

	if err := r.RunWeekly(ctx, 7); err != nil {
		t.Fatalf("RunWeekly: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "weekly-agent", 10)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	for _, run := range runs {
		if run.Status != store.RunStatusActive {
			t.Errorf("weekly run for model %q: got status %q, want active (weekly runs are always initially active)", run.Model, run.Status)
		}
	}
}
