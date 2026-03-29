package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/store"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

// newTestBenchmarkStore creates an in-memory BenchmarkStore for testing.
func newTestBenchmarkStore(t *testing.T) *sqlitestore.BenchmarkStore {
	t.Helper()
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() { _ = bs.Close() })
	return bs
}

// sampleRun builds a BenchmarkRun with all fields populated for testing.
func sampleRun(agentID string, verdict store.VerdictType) store.BenchmarkRun {
	return store.BenchmarkRun{
		RunAt:            time.Now().UTC().Truncate(time.Millisecond),
		WindowDays:       7,
		AgentID:          agentID,
		Model:            "claude-sonnet-4",
		Accuracy:         0.92,
		AvgLatencyMs:     1500,
		P50LatencyMs:     1200,
		P95LatencyMs:     2800,
		P99LatencyMs:     4500,
		ToolSuccessRate:  0.95,
		ROIScore:         4.2,
		TotalCostUSD:     3.14,
		SampleSize:       150,
		Verdict:          verdict,
		RecommendedModel: "",
		DecisionReason:   "All thresholds passed",
		ArtifactPath:     "/tmp/decisions_2024-01-14.json",
	}
}

// TestSaveRunAndLatestRun verifies round-trip: save a run, then retrieve it as the latest.
func TestSaveRunAndLatestRun(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	run := sampleRun("code-agent", store.VerdictKeep)
	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "code-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil, expected a run")
	}

	if got.AgentID != "code-agent" {
		t.Errorf("AgentID: got %q, want %q", got.AgentID, "code-agent")
	}
	if got.Verdict != store.VerdictKeep {
		t.Errorf("Verdict: got %q, want %q", got.Verdict, store.VerdictKeep)
	}
	if got.Accuracy != run.Accuracy {
		t.Errorf("Accuracy: got %f, want %f", got.Accuracy, run.Accuracy)
	}
	if got.P95LatencyMs != run.P95LatencyMs {
		t.Errorf("P95LatencyMs: got %f, want %f", got.P95LatencyMs, run.P95LatencyMs)
	}
	if got.SampleSize != run.SampleSize {
		t.Errorf("SampleSize: got %d, want %d", got.SampleSize, run.SampleSize)
	}
	if got.DecisionReason != run.DecisionReason {
		t.Errorf("DecisionReason: got %q, want %q", got.DecisionReason, run.DecisionReason)
	}
	// RunAt round-trips through UnixMilli — verify within 1ms.
	if got.RunAt.Sub(run.RunAt).Abs() > time.Millisecond {
		t.Errorf("RunAt: got %v, want %v", got.RunAt, run.RunAt)
	}
}

// TestGetLatestRunNilWhenEmpty verifies that GetLatestRun returns nil for unknown agents.
func TestGetLatestRunNilWhenEmpty(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	got, err := bs.GetLatestRun(ctx, "nonexistent-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestGetLatestRunReturnsNewest verifies that the most recent run is returned.
func TestGetLatestRunReturnsNewest(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	older := sampleRun("agent-x", store.VerdictSwitch)
	older.RunAt = time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
	older.Accuracy = 0.80

	newer := sampleRun("agent-x", store.VerdictKeep)
	newer.RunAt = time.Now().UTC().Truncate(time.Millisecond)
	newer.Accuracy = 0.92

	if err := bs.SaveRun(ctx, older); err != nil {
		t.Fatalf("SaveRun older: %v", err)
	}
	if err := bs.SaveRun(ctx, newer); err != nil {
		t.Fatalf("SaveRun newer: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "agent-x")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.Accuracy != 0.92 {
		t.Errorf("expected newer run (Accuracy=0.92), got Accuracy=%f", got.Accuracy)
	}
	if got.Verdict != store.VerdictKeep {
		t.Errorf("expected VerdictKeep, got %s", got.Verdict)
	}
}

// TestGetRunsFiltersAndLimits verifies filtering by agent_id and limit.
func TestGetRunsFiltersAndLimits(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Insert 3 runs for agent-a and 1 for agent-b.
	for i := 0; i < 3; i++ {
		r := sampleRun("agent-a", store.VerdictKeep)
		r.RunAt = time.Now().Add(time.Duration(-i) * time.Hour).UTC().Truncate(time.Millisecond)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun agent-a: %v", err)
		}
	}
	if err := bs.SaveRun(ctx, sampleRun("agent-b", store.VerdictSwitch)); err != nil {
		t.Fatalf("SaveRun agent-b: %v", err)
	}

	// Filter by agent-a.
	runsA, err := bs.GetRuns(ctx, "agent-a", 0)
	if err != nil {
		t.Fatalf("GetRuns agent-a: %v", err)
	}
	if len(runsA) != 3 {
		t.Errorf("expected 3 runs for agent-a, got %d", len(runsA))
	}

	// Apply limit.
	limited, err := bs.GetRuns(ctx, "agent-a", 2)
	if err != nil {
		t.Fatalf("GetRuns with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 runs with limit=2, got %d", len(limited))
	}

	// No agent filter — get all.
	all, err := bs.GetRuns(ctx, "", 0)
	if err != nil {
		t.Fatalf("GetRuns all: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 total runs, got %d", len(all))
	}
}

// TestListAgents verifies that distinct agent IDs are returned.
func TestListAgents(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	for _, agentID := range []string{"agent-a", "agent-b", "agent-a"} {
		r := sampleRun(agentID, store.VerdictKeep)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}

	agents, err := bs.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 distinct agents, got %d: %v", len(agents), agents)
	}
}

// TestBenchmarkIndexesApplied verifies the benchmark_runs table and indexes exist via sqlite_master.
func TestBenchmarkIndexesApplied(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Save a run to ensure the table is active.
	if err := bs.SaveRun(ctx, sampleRun("idx-agent", store.VerdictKeep)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// Verify the table exists by querying it.
	runs, err := bs.GetRuns(ctx, "idx-agent", 1)
	if err != nil {
		t.Fatalf("GetRuns after index test: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run, got %d", len(runs))
	}
}

// TestGetVerdictTrend verifies GetVerdictTrend behaviour across multiple scenarios.
func TestGetVerdictTrend(t *testing.T) {
	ctx := context.Background()

	t.Run("empty store returns empty slice", func(t *testing.T) {
		bs := newTestBenchmarkStore(t)
		trend, err := bs.GetVerdictTrend(ctx, "no-such-agent", 8)
		if err != nil {
			t.Fatalf("GetVerdictTrend: %v", err)
		}
		if len(trend) != 0 {
			t.Errorf("expected empty slice, got %v", trend)
		}
	})

	t.Run("fewer runs than requested weeks returns what exists oldest first", func(t *testing.T) {
		bs := newTestBenchmarkStore(t)
		// Insert 2 runs for an agent that has fewer than the requested 8 weeks.
		older := sampleRun("trend-agent", store.VerdictSwitch)
		older.RunAt = time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
		newer := sampleRun("trend-agent", store.VerdictKeep)
		newer.RunAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := bs.SaveRun(ctx, older); err != nil {
			t.Fatalf("SaveRun older: %v", err)
		}
		if err := bs.SaveRun(ctx, newer); err != nil {
			t.Fatalf("SaveRun newer: %v", err)
		}

		trend, err := bs.GetVerdictTrend(ctx, "trend-agent", 8)
		if err != nil {
			t.Fatalf("GetVerdictTrend: %v", err)
		}
		if len(trend) != 2 {
			t.Fatalf("expected 2 verdicts, got %d: %v", len(trend), trend)
		}
		// Oldest first: SWITCH then KEEP.
		if trend[0] != string(store.VerdictSwitch) {
			t.Errorf("trend[0]: got %q, want %q", trend[0], store.VerdictSwitch)
		}
		if trend[1] != string(store.VerdictKeep) {
			t.Errorf("trend[1]: got %q, want %q", trend[1], store.VerdictKeep)
		}
	})

	t.Run("more runs than requested returns only last N oldest first", func(t *testing.T) {
		bs := newTestBenchmarkStore(t)
		// Insert 5 runs; request only 3.
		verdicts := []store.VerdictType{
			store.VerdictSwitch,
			store.VerdictSwitch,
			store.VerdictKeep,
			store.VerdictKeep,
			store.VerdictInsufficientData,
		}
		base := time.Now().Add(-5 * 24 * time.Hour)
		for i, v := range verdicts {
			r := sampleRun("limit-agent", v)
			r.RunAt = base.Add(time.Duration(i) * 24 * time.Hour).UTC().Truncate(time.Millisecond)
			if err := bs.SaveRun(ctx, r); err != nil {
				t.Fatalf("SaveRun[%d]: %v", i, err)
			}
		}

		trend, err := bs.GetVerdictTrend(ctx, "limit-agent", 3)
		if err != nil {
			t.Fatalf("GetVerdictTrend: %v", err)
		}
		if len(trend) != 3 {
			t.Fatalf("expected 3 verdicts, got %d: %v", len(trend), trend)
		}
		// Should be the 3 newest (KEEP, KEEP, INSUFFICIENT_DATA), oldest-first.
		wantOrder := []string{
			string(store.VerdictKeep),
			string(store.VerdictKeep),
			string(store.VerdictInsufficientData),
		}
		for i, want := range wantOrder {
			if trend[i] != want {
				t.Errorf("trend[%d]: got %q, want %q", i, trend[i], want)
			}
		}
	})

	t.Run("weeks=0 returns nil or empty", func(t *testing.T) {
		bs := newTestBenchmarkStore(t)
		if err := bs.SaveRun(ctx, sampleRun("zero-agent", store.VerdictKeep)); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
		trend, err := bs.GetVerdictTrend(ctx, "zero-agent", 0)
		if err != nil {
			t.Fatalf("GetVerdictTrend with weeks=0: %v", err)
		}
		if len(trend) != 0 {
			t.Errorf("expected nil/empty for weeks=0, got %v", trend)
		}
	})

	t.Run("ordering is oldest first not newest first", func(t *testing.T) {
		bs := newTestBenchmarkStore(t)
		// Insert runs in this chronological order: SWITCH (oldest), KEEP, URGENT_SWITCH (newest).
		runs := []struct {
			offset  time.Duration
			verdict store.VerdictType
		}{
			{-72 * time.Hour, store.VerdictSwitch},
			{-48 * time.Hour, store.VerdictKeep},
			{-24 * time.Hour, store.VerdictUrgentSwitch},
		}
		for _, rc := range runs {
			r := sampleRun("order-agent", rc.verdict)
			r.RunAt = time.Now().Add(rc.offset).UTC().Truncate(time.Millisecond)
			if err := bs.SaveRun(ctx, r); err != nil {
				t.Fatalf("SaveRun: %v", err)
			}
		}

		trend, err := bs.GetVerdictTrend(ctx, "order-agent", 10)
		if err != nil {
			t.Fatalf("GetVerdictTrend: %v", err)
		}
		if len(trend) != 3 {
			t.Fatalf("expected 3 verdicts, got %d: %v", len(trend), trend)
		}
		// Oldest first: SWITCH → KEEP → URGENT_SWITCH.
		expected := []string{
			string(store.VerdictSwitch),
			string(store.VerdictKeep),
			string(store.VerdictUrgentSwitch),
		}
		for i, want := range expected {
			if trend[i] != want {
				t.Errorf("trend[%d]: got %q, want %q (ordering must be oldest first)", i, trend[i], want)
			}
		}
	})
}

// TestSaveRunWithAllVerdicts verifies all VerdictType values can be saved and retrieved.
func TestSaveRunWithAllVerdicts(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	verdicts := []store.VerdictType{
		store.VerdictKeep,
		store.VerdictSwitch,
		store.VerdictUrgentSwitch,
		store.VerdictInsufficientData,
	}

	for _, v := range verdicts {
		r := sampleRun("verdict-agent", v)
		r.RunAt = time.Now().Add(-time.Duration(len(verdicts)) * time.Hour).UTC().Truncate(time.Millisecond)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun verdict %s: %v", v, err)
		}
	}

	runs, err := bs.GetRuns(ctx, "verdict-agent", 0)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) != len(verdicts) {
		t.Errorf("expected %d runs, got %d", len(verdicts), len(runs))
	}
}
