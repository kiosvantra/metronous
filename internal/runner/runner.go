// Package runner orchestrates the weekly benchmark pipeline.
// It fetches events from the tracking store, computes metrics,
// evaluates thresholds via the decision engine, persists BenchmarkRuns,
// and generates decision artifact JSON files.
package runner

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/store"
)

// Runner orchestrates the weekly benchmark pipeline for all known agents.
type Runner struct {
	eventStore     store.EventStore
	benchmarkStore store.BenchmarkStore
	engine         *decision.DecisionEngine
	artifactDir    string
	logger         *zap.Logger
}

// NewRunner creates a Runner with the required dependencies.
func NewRunner(
	eventStore store.EventStore,
	benchmarkStore store.BenchmarkStore,
	engine *decision.DecisionEngine,
	artifactDir string,
	logger *zap.Logger,
) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Runner{
		eventStore:     eventStore,
		benchmarkStore: benchmarkStore,
		engine:         engine,
		artifactDir:    artifactDir,
		logger:         logger,
	}
}

// agentResult bundles the verdict and the pending BenchmarkRun for a single agent.
// The run is not yet persisted when this struct is returned — the ArtifactPath
// field is filled in by RunWeekly after the consolidated artifact is written.
type agentResult struct {
	verdict decision.Verdict
	run     store.BenchmarkRun
}

// RunWeekly executes the scheduled weekly benchmark pipeline.
// The event window is [now-windowDays, now). All runs are tagged run_kind=weekly.
func (r *Runner) RunWeekly(ctx context.Context, windowDays int) error {
	end := time.Now().UTC()
	start := end.Add(-time.Duration(windowDays) * 24 * time.Hour)
	return r.run(ctx, store.RunKindWeekly, start, end, windowDays)
}

// RunIntraweek executes a manual on-demand benchmark pipeline.
// The event window starts at lastRunAt+1ms (the first moment after the most recent
// stored run) and ends at now. If no prior run exists for any agent, the window
// falls back to [now-windowDays, now) — the same as a weekly run.
//
// Per-agent window derivation: we use the global max(run_at) across all agents
// so the interval is consistent across the whole batch.
func (r *Runner) RunIntraweek(ctx context.Context, windowDays int) error {
	end := time.Now().UTC()

	// Determine the global last run_at across all agents.
	// We query the benchmark store for the most recent run regardless of agent.
	runs, err := r.benchmarkStore.GetRuns(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("get last run for intraweek interval: %w", err)
	}

	var start time.Time
	if len(runs) > 0 && !runs[0].RunAt.IsZero() {
		// Start 1ms after the last recorded benchmark run.
		start = runs[0].RunAt.Add(time.Millisecond)
		r.logger.Info("intraweek: derived start from last run",
			zap.Time("last_run_at", runs[0].RunAt),
			zap.Time("window_start", start),
		)
	} else {
		// No prior run — fall back to windowDays.
		start = end.Add(-time.Duration(windowDays) * 24 * time.Hour)
		r.logger.Info("intraweek: no prior run found, using windowDays fallback",
			zap.Int("window_days", windowDays),
			zap.Time("window_start", start),
		)
	}

	return r.run(ctx, store.RunKindIntraweek, start, end, windowDays)
}

// run is the shared implementation for RunWeekly and RunIntraweek.
func (r *Runner) run(ctx context.Context, kind store.RunKindType, start, end time.Time, windowDays int) error {
	r.logger.Info("starting benchmark run",
		zap.String("run_kind", string(kind)),
		zap.Time("window_start", start),
		zap.Time("window_end", end),
		zap.Int("window_days", windowDays),
	)

	// Discover agents from the event store.
	agents, err := r.discoverAgents(ctx, start, end)
	if err != nil {
		return fmt.Errorf("discover agents: %w", err)
	}

	if len(agents) == 0 {
		r.logger.Info("no agents found in window, skipping benchmark run")
		return nil
	}

	r.logger.Info("discovered agents", zap.Strings("agents", agents))

	// Compute metrics and evaluate for each agent; collect results before saving.
	var results []agentResult
	var failedAgents []string
	for _, agentID := range agents {
		res, err := r.processAgent(ctx, agentID, start, end, windowDays)
		if err != nil {
			r.logger.Error("failed to process agent",
				zap.String("agent_id", agentID),
				zap.Error(err),
			)
			failedAgents = append(failedAgents, agentID)
			continue
		}
		// Tag with run kind and window bounds.
		res.run.RunKind = kind
		res.run.WindowStart = start
		res.run.WindowEnd = end
		results = append(results, res)
	}

	// Generate consolidated artifact for all verdicts so the path is available
	// before we persist the BenchmarkRuns.
	var artifactPath string
	if len(results) > 0 {
		verdicts := make([]decision.Verdict, 0, len(results))
		for _, res := range results {
			verdicts = append(verdicts, res.verdict)
		}
		var artifactErr error
		artifactPath, artifactErr = decision.GenerateArtifact(verdicts, windowDays, r.artifactDir)
		if artifactErr != nil {
			r.logger.Error("failed to generate artifact", zap.Error(artifactErr))
			// Non-fatal: continue saving runs with empty artifact path.
		} else {
			r.logger.Info("generated decision artifact", zap.String("path", artifactPath))
		}
	}

	// Persist each BenchmarkRun with the artifact path now populated.
	for i := range results {
		results[i].run.ArtifactPath = artifactPath
		if err := r.benchmarkStore.SaveRun(ctx, results[i].run); err != nil {
			r.logger.Error("failed to save benchmark run",
				zap.String("agent_id", results[i].run.AgentID),
				zap.Error(err),
			)
			// Continue saving remaining runs.
		}
	}

	r.logger.Info("benchmark run complete",
		zap.String("run_kind", string(kind)),
		zap.Int("agents_processed", len(results)),
		zap.Int("agents_failed", len(failedAgents)),
	)
	if len(failedAgents) > 0 {
		r.logger.Warn("agents failed during processing", zap.Strings("failed_agent_ids", failedAgents))
	}
	return nil
}

// processAgent computes metrics and evaluates the verdict for a single agent.
// It returns an agentResult with a partially-populated BenchmarkRun.
// ArtifactPath, RunKind, WindowStart, and WindowEnd are filled in by the caller (run).
func (r *Runner) processAgent(ctx context.Context, agentID string, start, end time.Time, windowDays int) (agentResult, error) {
	// 1. Fetch events for the window.
	events, err := benchmark.FetchEventsForWindow(ctx, r.eventStore, agentID, start, end)
	if err != nil {
		return agentResult{}, fmt.Errorf("fetch events for %q: %w", agentID, err)
	}

	// 2. Aggregate metrics.
	metrics := benchmark.AggregateMetrics(r.logger, agentID, events)

	// 3. Evaluate thresholds → verdict.
	verdict := r.engine.Evaluate(ctx, metrics)

	// 4. Build the BenchmarkRun (not yet saved — ArtifactPath filled by caller).
	run := store.BenchmarkRun{
		RunAt:            time.Now().UTC(),
		WindowDays:       windowDays,
		AgentID:          agentID,
		Model:            metrics.Model,
		Accuracy:         metrics.Accuracy,
		AvgLatencyMs:     metrics.AvgLatencyMs,
		P50LatencyMs:     metrics.P50LatencyMs,
		P95LatencyMs:     metrics.P95LatencyMs,
		P99LatencyMs:     metrics.P99LatencyMs,
		ToolSuccessRate:  metrics.ToolSuccessRate,
		ROIScore:         metrics.ROIScore,
		TotalCostUSD:     metrics.TotalCostUSD,
		SampleSize:       metrics.SampleSize,
		Verdict:          verdict.Type,
		RecommendedModel: verdict.RecommendedModel,
		DecisionReason:   verdict.Reason,
		AvgQualityScore:  metrics.AvgQuality,
		// ArtifactPath is set by RunWeekly after GenerateArtifact completes.
	}

	r.logger.Info("agent benchmark complete",
		zap.String("agent_id", agentID),
		zap.String("model", metrics.Model),
		zap.String("verdict", string(verdict.Type)),
		zap.Int("sample_size", metrics.SampleSize),
	)

	return agentResult{verdict: verdict, run: run}, nil
}

// discoverAgents returns distinct agent IDs from events within the given window.
func (r *Runner) discoverAgents(ctx context.Context, start, end time.Time) ([]string, error) {
	events, err := r.eventStore.QueryEvents(ctx, store.EventQuery{
		Since: start,
		Until: end,
	})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var agents []string
	for _, e := range events {
		// Only consider agents that emitted at least one non-error event.
		// Error-only agents usually come from telemetry ingestion issues and
		// produce INSUFFICIENT_DATA benchmark entries (e.g. model == "unknown").
		if e.EventType == "error" {
			continue
		}
		if _, ok := seen[e.AgentID]; !ok {
			seen[e.AgentID] = struct{}{}
			agents = append(agents, e.AgentID)
		}
	}
	return agents, nil
}
