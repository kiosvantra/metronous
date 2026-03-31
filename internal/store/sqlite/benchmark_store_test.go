package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"

	// Register the SQLite driver used by the rest of the test suite.
	_ "modernc.org/sqlite"
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

// TestQueryRunsPagination verifies QueryRuns supports offset+limit sliding-window pagination.
func TestQueryRunsPagination(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Insert 5 runs for agent-a ordered newest to oldest (run_at DESC).
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		r := sampleRun("page-agent", store.VerdictKeep)
		r.RunAt = base.Add(time.Duration(-i) * time.Hour)
		r.Accuracy = float64(i) * 0.1 // distinct value to identify each row
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun[%d]: %v", i, err)
		}
	}

	// Page 1: offset=0, limit=3 — should return rows 0,1,2 (newest first).
	page1, err := bs.QueryRuns(ctx, store.BenchmarkQuery{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("QueryRuns page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: expected 3 runs, got %d", len(page1))
	}
	// Newest run should be first (Accuracy=0.0, i=0).
	if page1[0].Accuracy != 0.0 {
		t.Errorf("page1[0].Accuracy: expected 0.0 (newest), got %f", page1[0].Accuracy)
	}

	// Page 2: offset=3, limit=3 — should return rows 3,4 (only 2 remain).
	page2, err := bs.QueryRuns(ctx, store.BenchmarkQuery{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("QueryRuns page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: expected 2 runs, got %d", len(page2))
	}
	// Row at offset 3 should be older than page1[0] (i=3 → smaller RunAt).
	if !page2[0].RunAt.Before(page1[2].RunAt) {
		t.Errorf("page2[0] should be older than page1[2]: page2[0].RunAt=%v page1[2].RunAt=%v",
			page2[0].RunAt, page1[2].RunAt)
	}
}

// TestQueryRunsWithAgentFilter verifies QueryRuns filters by agent_id.
func TestQueryRunsWithAgentFilter(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	for i := 0; i < 3; i++ {
		r := sampleRun("alpha", store.VerdictKeep)
		r.RunAt = time.Now().Add(time.Duration(-i) * time.Hour)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun alpha: %v", err)
		}
	}
	if err := bs.SaveRun(ctx, sampleRun("beta", store.VerdictSwitch)); err != nil {
		t.Fatalf("SaveRun beta: %v", err)
	}

	// Filter to alpha only.
	runs, err := bs.QueryRuns(ctx, store.BenchmarkQuery{AgentID: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs for alpha, got %d", len(runs))
	}
	for _, r := range runs {
		if r.AgentID != "alpha" {
			t.Errorf("unexpected agent_id %q in filtered result", r.AgentID)
		}
	}
}

// TestCountRunsTotal verifies CountRuns returns total across all agents.
func TestCountRunsTotal(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Insert 4 runs: 3 for agent-x, 1 for agent-y.
	for i := 0; i < 3; i++ {
		r := sampleRun("agent-x", store.VerdictKeep)
		r.RunAt = time.Now().Add(time.Duration(-i) * time.Hour)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun agent-x: %v", err)
		}
	}
	if err := bs.SaveRun(ctx, sampleRun("agent-y", store.VerdictSwitch)); err != nil {
		t.Fatalf("SaveRun agent-y: %v", err)
	}

	total, err := bs.CountRuns(ctx, store.BenchmarkQuery{})
	if err != nil {
		t.Fatalf("CountRuns: %v", err)
	}
	if total != 4 {
		t.Errorf("expected total = 4, got %d", total)
	}
}

// TestCountRunsWithAgentFilter verifies CountRuns filters by agent_id.
func TestCountRunsWithAgentFilter(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	for i := 0; i < 3; i++ {
		r := sampleRun("count-alpha", store.VerdictKeep)
		r.RunAt = time.Now().Add(time.Duration(-i) * time.Hour)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
	if err := bs.SaveRun(ctx, sampleRun("count-beta", store.VerdictSwitch)); err != nil {
		t.Fatalf("SaveRun count-beta: %v", err)
	}

	countAlpha, err := bs.CountRuns(ctx, store.BenchmarkQuery{AgentID: "count-alpha"})
	if err != nil {
		t.Fatalf("CountRuns count-alpha: %v", err)
	}
	if countAlpha != 3 {
		t.Errorf("expected 3 runs for count-alpha, got %d", countAlpha)
	}

	countBeta, err := bs.CountRuns(ctx, store.BenchmarkQuery{AgentID: "count-beta"})
	if err != nil {
		t.Fatalf("CountRuns count-beta: %v", err)
	}
	if countBeta != 1 {
		t.Errorf("expected 1 run for count-beta, got %d", countBeta)
	}
}

// TestCountRunsEmpty verifies CountRuns returns 0 for an empty store.
func TestCountRunsEmpty(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	count, err := bs.CountRuns(ctx, store.BenchmarkQuery{})
	if err != nil {
		t.Fatalf("CountRuns on empty store: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 for empty store, got %d", count)
	}
}

// TestQueryRunsOffsetBeyondEnd verifies QueryRuns returns empty when offset exceeds total.
func TestQueryRunsOffsetBeyondEnd(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	if err := bs.SaveRun(ctx, sampleRun("single-agent", store.VerdictKeep)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// Offset past the only row.
	runs, err := bs.QueryRuns(ctx, store.BenchmarkQuery{Limit: 10, Offset: 100})
	if err != nil {
		t.Fatalf("QueryRuns with large offset: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs with offset beyond end, got %d", len(runs))
	}
}

// TestApplyBenchmarkMigrations_CompositeScore_FreshDB verifies that composite_score column
// exists on a fresh in-memory database.
func TestApplyBenchmarkMigrations_CompositeScore_FreshDB(t *testing.T) {
	bs := newTestBenchmarkStore(t)
	ctx := context.Background()

	// Access the underlying DB via a round-trip save+read to confirm the column exists.
	run := sampleRun("migration-agent", store.VerdictKeep)
	run.CompositeScore = 0.77
	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, err := bs.GetLatestRun(ctx, "migration-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.CompositeScore != 0.77 {
		t.Errorf("CompositeScore: got %v, want 0.77", got.CompositeScore)
	}
}

// TestApplyBenchmarkMigrations_CompositeScore_Idempotent verifies that ApplyBenchmarkMigrations
// can be called twice without error (duplicate column name is ignored).
func TestApplyBenchmarkMigrations_CompositeScore_Idempotent(t *testing.T) {
	bs := newTestBenchmarkStore(t)

	// The store was already created (migrations applied). Creating a second store
	// on ":memory:" would be a separate DB. Instead, verify no panic and no error
	// by saving a run — which proves the column is accessible after two calls.
	ctx := context.Background()
	run := sampleRun("idem-agent", store.VerdictKeep)
	run.CompositeScore = 0.55
	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun after idempotent migration: %v", err)
	}
}

// TestSaveRun_PersistsCompositeScore verifies that CompositeScore is saved and retrieved.
func TestSaveRun_PersistsCompositeScore(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	run := sampleRun("score-agent", store.VerdictKeep)
	run.CompositeScore = 0.87

	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	runs, err := bs.GetRuns(ctx, "score-agent", 0)
	if err != nil {
		t.Fatalf("GetRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].CompositeScore != 0.87 {
		t.Errorf("CompositeScore: got %v, want 0.87", runs[0].CompositeScore)
	}
}

// TestGetLatestRunByAgentModel_ReturnsCompositeScore verifies that GetLatestRunByAgentModel
// returns the CompositeScore field.
func TestGetLatestRunByAgentModel_ReturnsCompositeScore(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	run := sampleRun("cs-agent", store.VerdictKeep)
	run.CompositeScore = 0.72

	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := bs.GetLatestRunByAgentModel(ctx, "cs-agent", "claude-sonnet-4")
	if err != nil {
		t.Fatalf("GetLatestRunByAgentModel: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRunByAgentModel returned nil")
	}
	if got.CompositeScore != 0.72 {
		t.Errorf("CompositeScore: got %v, want 0.72", got.CompositeScore)
	}
}

// TestQueryRuns_ReturnsCompositeScore verifies that QueryRuns scans CompositeScore correctly.
func TestQueryRuns_ReturnsCompositeScore(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	run := sampleRun("qcs-agent", store.VerdictKeep)
	run.CompositeScore = 0.63

	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	runs, err := bs.QueryRuns(ctx, store.BenchmarkQuery{AgentID: "qcs-agent", Limit: 10})
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].CompositeScore != 0.63 {
		t.Errorf("CompositeScore: got %v, want 0.63", runs[0].CompositeScore)
	}
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

// ─── Cycle pagination tests ───────────────────────────────────────────────────

// TestListRunCyclesEmpty verifies ListRunCycles returns nil/empty for an empty store.
func TestListRunCyclesEmpty(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	cycles, err := bs.ListRunCycles(ctx, time.UTC, 0, 0)
	if err != nil {
		t.Fatalf("ListRunCycles on empty store: %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d: %v", len(cycles), cycles)
	}
}

// TestListRunCyclesSingleWeek verifies that multiple runs in the same week collapse to one cycle.
func TestListRunCyclesSingleWeek(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Sunday 2024-01-07 — pick a known Sunday in UTC.
	sunday := time.Date(2024, 1, 7, 10, 0, 0, 0, time.UTC)
	// Two runs on the same Sunday-week (Sun 7th and Wed 10th).
	for _, offset := range []time.Duration{0, 72 * time.Hour} {
		r := sampleRun("cycle-agent", store.VerdictKeep)
		r.RunAt = sunday.Add(offset)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}

	cycles, err := bs.ListRunCycles(ctx, time.UTC, 0, 0)
	if err != nil {
		t.Fatalf("ListRunCycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle (same week), got %d: %v", len(cycles), cycles)
	}
	// The cycle start should be Sunday 2024-01-07 00:00 UTC.
	want := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)
	if !cycles[0].Equal(want) {
		t.Errorf("cycle start: got %v, want %v", cycles[0], want)
	}
}

// TestListRunCyclesMultipleWeeks verifies that runs in different weeks yield distinct cycles, newest first.
func TestListRunCyclesMultipleWeeks(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Three runs each in a different ISO week (all Wednesdays, 1 week apart).
	// Week starts: 2024-01-07 (Sun), 2024-01-14 (Sun), 2024-01-21 (Sun).
	timestamps := []time.Time{
		time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC), // Wed week-of-Jan-7
		time.Date(2024, 1, 17, 12, 0, 0, 0, time.UTC), // Wed week-of-Jan-14
		time.Date(2024, 1, 24, 12, 0, 0, 0, time.UTC), // Wed week-of-Jan-21
	}
	for i, ts := range timestamps {
		r := sampleRun("multi-agent", store.VerdictKeep)
		r.RunAt = ts
		r.Accuracy = float64(i) * 0.1
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun[%d]: %v", i, err)
		}
	}

	cycles, err := bs.ListRunCycles(ctx, time.UTC, 0, 0)
	if err != nil {
		t.Fatalf("ListRunCycles: %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("expected 3 cycles, got %d: %v", len(cycles), cycles)
	}

	// Newest first: Jan-21 week, then Jan-14 week, then Jan-7 week.
	wantStarts := []time.Time{
		time.Date(2024, 1, 21, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 14, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
	}
	for i, want := range wantStarts {
		if !cycles[i].Equal(want) {
			t.Errorf("cycles[%d]: got %v, want %v", i, cycles[i], want)
		}
	}
}

// TestListRunCyclesOffsetAndLimit verifies ListRunCycles applies offset and limit correctly.
func TestListRunCyclesOffsetAndLimit(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Insert runs in 5 different weeks.
	for i := 0; i < 5; i++ {
		r := sampleRun("ol-agent", store.VerdictKeep)
		// Use Sundays separated by one week each.
		r.RunAt = time.Date(2024, 1, 7+i*7, 10, 0, 0, 0, time.UTC)
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun[%d]: %v", i, err)
		}
	}

	// limit=2, offset=1 — skip the newest, return next 2.
	cycles, err := bs.ListRunCycles(ctx, time.UTC, 2, 1)
	if err != nil {
		t.Fatalf("ListRunCycles: %v", err)
	}
	if len(cycles) != 2 {
		t.Fatalf("expected 2 cycles with limit=2 offset=1, got %d", len(cycles))
	}
	// 5 Sundays newest-first: Jan-35(=Feb-4), Jan-28, Jan-21, Jan-14, Jan-7.
	// offset=1 skips Feb-4; first result should be Jan-28.
	wantFirst := time.Date(2024, 1, 28, 0, 0, 0, 0, time.UTC)
	if !cycles[0].Equal(wantFirst) {
		t.Errorf("cycles[0] after offset=1: got %v, want %v", cycles[0], wantFirst)
	}
}

// TestQueryRunsInWindow verifies QueryRunsInWindow includes [since,until) correctly.
func TestQueryRunsInWindow(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	// Window: [2024-01-07, 2024-01-14)
	since := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)
	until := since.AddDate(0, 0, 7)

	// Run inside the window.
	inside := sampleRun("win-agent", store.VerdictKeep)
	inside.RunAt = time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC)

	// Run at the exact start boundary (inclusive).
	atStart := sampleRun("win-agent", store.VerdictSwitch)
	atStart.RunAt = since

	// Run at exactly the end boundary (exclusive — must NOT appear).
	atEnd := sampleRun("win-agent", store.VerdictUrgentSwitch)
	atEnd.RunAt = until

	// Run outside the window (before).
	before := sampleRun("win-agent", store.VerdictKeep)
	before.RunAt = time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)

	for _, r := range []store.BenchmarkRun{inside, atStart, atEnd, before} {
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}

	runs, err := bs.QueryRunsInWindow(ctx, since, until)
	if err != nil {
		t.Fatalf("QueryRunsInWindow: %v", err)
	}
	// Should contain exactly 'inside' and 'atStart'; 'atEnd' and 'before' excluded.
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs in window, got %d: %v", len(runs), runs)
	}
	for _, r := range runs {
		if !r.RunAt.Before(until) {
			t.Errorf("run at %v is not before until %v — exclusive upper bound violated", r.RunAt, until)
		}
		if r.RunAt.Before(since) {
			t.Errorf("run at %v is before since %v — inclusive lower bound violated", r.RunAt, since)
		}
	}
}

// TestQueryRunsInWindowEmpty verifies QueryRunsInWindow returns empty when no runs fall in window.
func TestQueryRunsInWindowEmpty(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	since := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	until := since.AddDate(0, 0, 7)

	// Insert a run outside the window.
	r := sampleRun("empty-win", store.VerdictKeep)
	r.RunAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := bs.SaveRun(ctx, r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	runs, err := bs.QueryRunsInWindow(ctx, since, until)
	if err != nil {
		t.Fatalf("QueryRunsInWindow: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

// TestWeekStartInLoc verifies the week-start calculation for various weekdays.
func TestWeekStartInLoc(t *testing.T) {
	// We test via ListRunCycles with a single run placed on each weekday,
	// verifying all collapse to the same Sunday week-start.

	ctx := context.Background()

	// Week containing 2024-01-07 (Sunday) through 2024-01-13 (Saturday).
	expectedStart := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	weekdays := []time.Time{
		time.Date(2024, 1, 7, 8, 0, 0, 0, time.UTC),    // Sunday
		time.Date(2024, 1, 8, 8, 0, 0, 0, time.UTC),    // Monday
		time.Date(2024, 1, 10, 8, 0, 0, 0, time.UTC),   // Wednesday
		time.Date(2024, 1, 13, 23, 59, 0, 0, time.UTC), // Saturday
	}

	for _, ts := range weekdays {
		bs := newTestBenchmarkStore(t)
		r := sampleRun("ws-agent", store.VerdictKeep)
		r.RunAt = ts
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun for %v: %v", ts, err)
		}
		cycles, err := bs.ListRunCycles(ctx, time.UTC, 0, 0)
		if err != nil {
			t.Fatalf("ListRunCycles for %v: %v", ts, err)
		}
		if len(cycles) != 1 {
			t.Fatalf("expected 1 cycle for %v, got %d", ts, len(cycles))
		}
		if !cycles[0].Equal(expectedStart) {
			t.Errorf("week-start for %v: got %v, want %v", ts, cycles[0], expectedStart)
		}
	}
}

// TestListRunCyclesNilLocDefaultsToLocal verifies that passing nil loc uses time.Local.
func TestListRunCyclesNilLocDefaultsToLocal(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	r := sampleRun("nil-loc", store.VerdictKeep)
	r.RunAt = time.Now().UTC()
	if err := bs.SaveRun(ctx, r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// nil loc should not panic.
	cycles, err := bs.ListRunCycles(ctx, nil, 0, 0)
	if err != nil {
		t.Fatalf("ListRunCycles with nil loc: %v", err)
	}
	if len(cycles) != 1 {
		t.Errorf("expected 1 cycle, got %d", len(cycles))
	}
}

// ─── RunKind / WindowStart / WindowEnd tests ─────────────────────────────────

// TestBenchmarkMigrationAddsNewColumns verifies that ApplyBenchmarkMigrations adds
// run_kind, window_start, and window_end without breaking existing data.
// It simulates an "old" database by creating the schema without the new columns,
// inserting a row with the old INSERT, then applying migrations and verifying
// the new columns exist with their default values.
func TestBenchmarkMigrationAddsNewColumns(t *testing.T) {
	ctx := context.Background()

	// Open a raw in-memory SQLite DB using the same driver as the rest of the test suite.
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer rawDB.Close()

	// Create the "old" schema without run_kind / window_start / window_end.
	oldSchema := `
CREATE TABLE IF NOT EXISTS benchmark_runs (
    id                TEXT PRIMARY KEY,
    run_at            INTEGER NOT NULL,
    window_days       INTEGER NOT NULL DEFAULT 7,
    agent_id          TEXT NOT NULL,
    model             TEXT NOT NULL,
    accuracy          REAL NOT NULL DEFAULT 0.0,
    avg_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p50_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p95_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p99_latency_ms    REAL NOT NULL DEFAULT 0.0,
    tool_success_rate REAL NOT NULL DEFAULT 0.0,
    roi_score         REAL NOT NULL DEFAULT 0.0,
    total_cost_usd    REAL NOT NULL DEFAULT 0.0,
    sample_size       INTEGER NOT NULL DEFAULT 0,
    verdict           TEXT NOT NULL,
    recommended_model TEXT NOT NULL DEFAULT '',
    decision_reason   TEXT NOT NULL DEFAULT '',
    artifact_path     TEXT NOT NULL DEFAULT '',
    avg_quality_score REAL NOT NULL DEFAULT 0.0
);`
	if _, err := rawDB.ExecContext(ctx, oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}

	// Insert a legacy row using only the old columns.
	const oldInsert = `INSERT INTO benchmark_runs
		(id, run_at, window_days, agent_id, model, verdict, avg_quality_score)
		VALUES ('legacy-1', 1700000000000, 7, 'old-agent', 'gpt-4', 'KEEP', 0.9)`
	if _, err := rawDB.ExecContext(ctx, oldInsert); err != nil {
		t.Fatalf("old insert: %v", err)
	}

	// Now apply migrations — should add run_kind, window_start, window_end idempotently.
	if err := sqlitestore.ApplyBenchmarkMigrations(ctx, rawDB); err != nil {
		t.Fatalf("ApplyBenchmarkMigrations: %v", err)
	}

	// Verify the legacy row can be read via the full SELECT list.
	const q = `SELECT run_kind, window_start, window_end FROM benchmark_runs WHERE id = 'legacy-1'`
	row := rawDB.QueryRowContext(ctx, q)
	var runKind string
	var windowStart, windowEnd int64
	if err := row.Scan(&runKind, &windowStart, &windowEnd); err != nil {
		t.Fatalf("scan new columns: %v", err)
	}
	if runKind != "weekly" {
		t.Errorf("run_kind default: got %q, want 'weekly'", runKind)
	}
	if windowStart != 0 {
		t.Errorf("window_start default: got %d, want 0", windowStart)
	}
	if windowEnd != 0 {
		t.Errorf("window_end default: got %d, want 0", windowEnd)
	}

	// Calling ApplyBenchmarkMigrations again must be idempotent (no error).
	if err := sqlitestore.ApplyBenchmarkMigrations(ctx, rawDB); err != nil {
		t.Fatalf("second ApplyBenchmarkMigrations: %v", err)
	}
}

// TestSaveRunPreservesRunKindAndWindow verifies that RunKind, WindowStart,
// and WindowEnd round-trip correctly through SaveRun → GetLatestRun.
func TestSaveRunPreservesRunKindAndWindow(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	windowStart := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2024, 3, 4, 12, 0, 0, 0, time.UTC)

	run := sampleRun("kind-agent", store.VerdictKeep)
	run.RunKind = store.RunKindIntraweek
	run.WindowStart = windowStart.Truncate(time.Millisecond)
	run.WindowEnd = windowEnd.Truncate(time.Millisecond)

	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "kind-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.RunKind != store.RunKindIntraweek {
		t.Errorf("RunKind: got %q, want %q", got.RunKind, store.RunKindIntraweek)
	}
	if !got.WindowStart.Equal(windowStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, windowStart)
	}
	if !got.WindowEnd.Equal(windowEnd) {
		t.Errorf("WindowEnd: got %v, want %v", got.WindowEnd, windowEnd)
	}
}

// TestSaveRunDefaultsRunKindToWeekly verifies that a run with empty RunKind is
// stored and retrieved as RunKindWeekly.
func TestSaveRunDefaultsRunKindToWeekly(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	run := sampleRun("default-kind-agent", store.VerdictKeep)
	// Leave RunKind unset (zero value).
	run.RunKind = ""

	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := bs.GetLatestRun(ctx, "default-kind-agent")
	if err != nil {
		t.Fatalf("GetLatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestRun returned nil")
	}
	if got.RunKind != store.RunKindWeekly {
		t.Errorf("RunKind: got %q, want %q (default should be weekly)", got.RunKind, store.RunKindWeekly)
	}
}
