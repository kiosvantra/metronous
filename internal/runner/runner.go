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
func (r *Runner) RunIntraweek(ctx context.Context, windowDays int) error {
	end := time.Now().UTC()
	runs, err := r.benchmarkStore.GetRuns(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("get last run for intraweek interval: %w", err)
	}

	var start time.Time
	if len(runs) > 0 && !runs[0].RunAt.IsZero() {
		start = runs[0].RunAt.Add(time.Millisecond)
		r.logger.Info("intraweek: derived start from last run",
			zap.Time("last_run_at", runs[0].RunAt),
			zap.Time("window_start", start),
		)
	} else {
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

	// Compute metrics and evaluate for each agent+model combination; collect results before saving.
	var results []agentResult
	var failedAgents []string
	for _, agentID := range agents {
		agentResults, err := r.processAgent(ctx, agentID, start, end, windowDays)
		if err != nil {
			r.logger.Error("failed to process agent",
				zap.String("agent_id", agentID),
				zap.Error(err),
			)
			failedAgents = append(failedAgents, agentID)
			continue
		}
		for i := range agentResults {
			agentResults[i].run.RunKind = kind
			agentResults[i].run.WindowStart = start
			agentResults[i].run.WindowEnd = end
		}
		results = append(results, agentResults...)
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

// processAgent computes metrics and evaluates the verdict for each model used
// by a single agent. Events are grouped by model so each (agent_id, model)
// combination gets its own independent benchmark run with separate metrics.
// Returns one agentResult per model (ArtifactPath is left empty — RunWeekly
// sets it after the artifact file is written).
func (r *Runner) processAgent(ctx context.Context, agentID string, start, end time.Time, windowDays int) ([]agentResult, error) {
	// 1. Fetch all events for the agent in the window.
	events, err := benchmark.FetchEventsForWindow(ctx, r.eventStore, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("fetch events for %q: %w", agentID, err)
	}

	// 2. Group events by model — each group gets independent metrics.
	modelGroups := benchmark.GroupEventsByModel(events)

	var results []agentResult
	for model, modelEvents := range modelGroups {
		// 3. Aggregate metrics for this (agent, model) pair.
		metrics := benchmark.AggregateMetrics(r.logger, agentID, modelEvents)
		// Override Model to the exact model for this group (AggregateMetrics
		// uses dominantModel which is redundant here since all events share
		// the same model, but we set it explicitly for clarity).
		metrics.Model = model

		// 4. Evaluate thresholds → verdict.
		//    The engine resolves per-agent thresholds using agentID —
		//    the model is the VARIABLE being evaluated, not the threshold key.
		verdict := r.engine.Evaluate(ctx, metrics)

		// 5. Compute composite score using effective weights and thresholds.
		weights := r.engine.ScoreWeights()
		maxLat := r.engine.EffectiveMaxLatencyP95(agentID)
		score := benchmark.ComputeCompositeScore(
			benchmark.ScoreInput{
				Accuracy:        metrics.Accuracy,
				P95LatencyMs:    metrics.P95LatencyMs,
				ToolSuccessRate: metrics.ToolSuccessRate,
				ROIScore:        metrics.ROIScore,
			},
			weights,
			benchmark.ScoreThresholds{MaxLatencyP95Ms: float64(maxLat)},
		)

		// 6. Build the BenchmarkRun.
		run := store.BenchmarkRun{
			RunAt:            time.Now().UTC(),
			WindowDays:       windowDays,
			AgentID:          agentID,
			Model:            model,
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
			CompositeScore:   score,
			// ArtifactPath is set by RunWeekly after GenerateArtifact completes.
		}

		r.logger.Info("agent+model benchmark complete",
			zap.String("agent_id", agentID),
			zap.String("model", model),
			zap.String("verdict", string(verdict.Type)),
			zap.Int("sample_size", metrics.SampleSize),
		)

		results = append(results, agentResult{verdict: verdict, run: run})
	}

	// If the agent had zero events, return a single empty result so the
	// caller can still log it (backward compat with single-model agents).
	if len(results) == 0 {
		metrics := benchmark.AggregateMetrics(r.logger, agentID, nil)
		verdict := r.engine.Evaluate(ctx, metrics)
		run := store.BenchmarkRun{
			RunAt:      time.Now().UTC(),
			WindowDays: windowDays,
			AgentID:    agentID,
			Verdict:    verdict.Type,
		}
		results = append(results, agentResult{verdict: verdict, run: run})
	}

	return results, nil
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
		if _, ok := seen[e.AgentID]; !ok {
			seen[e.AgentID] = struct{}{}
			agents = append(agents, e.AgentID)
		}
	}
	return agents, nil
}
