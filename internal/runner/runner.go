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
// The event window is [now-windowDays, now) — the same as a weekly run.
// This ensures each F5 press accumulates ALL events in the current week,
// so sample counts grow over time rather than shrinking to only new events.
func (r *Runner) RunIntraweek(ctx context.Context, windowDays int) error {
	end := time.Now().UTC()
	start := end.Add(-time.Duration(windowDays) * 24 * time.Hour)

	r.logger.Info("intraweek: using full weekly window",
		zap.Time("window_start", start),
		zap.Time("window_end", end),
		zap.Int("window_days", windowDays),
	)

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

	// Compute metrics and evaluate for each (agent, model) pair; collect results before saving.
	var results []agentResult
	var failedAgents []string
	for _, agentID := range agents {
		res, err := r.processAgentAllModels(ctx, agentID, start, end, windowDays)
		if err != nil {
			r.logger.Error("failed to process agent",
				zap.String("agent_id", agentID),
				zap.Error(err),
			)
			failedAgents = append(failedAgents, agentID)
			continue
		}
		// Tag each result with run kind and window bounds.
		for i := range res {
			res[i].run.RunKind = kind
			res[i].run.WindowStart = start
			res[i].run.WindowEnd = end
		}
		results = append(results, res...)
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

// modelMetrics holds the computed metrics and verdict for a single (agent, model) pair.
type modelMetrics struct {
	metrics benchmark.WindowMetrics
	verdict decision.Verdict
}

// processAgentAllModels computes metrics and evaluates the verdict for each
// distinct model used by the agent in the given window. It returns one
// agentResult per (agent, model) pair so the benchmark captures per-model
// performance independently.
//
// ArtifactPath, RunKind, WindowStart, and WindowEnd are filled in by the caller.
func (r *Runner) processAgentAllModels(ctx context.Context, agentID string, start, end time.Time, windowDays int) ([]agentResult, error) {
	// 1. Fetch all events for the agent in the window.
	events, err := benchmark.FetchEventsForWindow(ctx, r.eventStore, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("fetch events for %q: %w", agentID, err)
	}

	// 2. Group events by normalized model name.
	modelEvents := make(map[string][]store.Event)
	for _, e := range events {
		model := store.NormalizeModelName(e.Model)
		modelEvents[model] = append(modelEvents[model], e)
	}

	if len(modelEvents) == 0 {
		return nil, nil
	}

	// 3. Compute metrics for every model independently.
	now := time.Now().UTC()
	perModel := make(map[string]modelMetrics, len(modelEvents))
	for model, evts := range modelEvents {
		m := benchmark.AggregateMetrics(r.logger, agentID, evts)
		m.Model = model
		v := r.engine.Evaluate(ctx, m)
		perModel[model] = modelMetrics{metrics: m, verdict: v}
	}

	// 4. Build results, replacing the static recommended model with the best
	// alternative derived from real benchmark data for this agent.
	var results []agentResult
	for model, pm := range perModel {
		recommended := pm.verdict.RecommendedModel
		if pm.verdict.Type == store.VerdictSwitch || pm.verdict.Type == store.VerdictUrgentSwitch {
			recommended = bestAlternativeModel(model, pm.metrics, perModel)
			if recommended == "" {
				// No better model found in current window data — keep config fallback.
				recommended = pm.verdict.RecommendedModel
			}
		}

		run := store.BenchmarkRun{
			RunAt:               now,
			WindowDays:          windowDays,
			AgentID:             agentID,
			Model:               model,
			Accuracy:            pm.metrics.Accuracy,
			AvgLatencyMs:        pm.metrics.AvgTurnMs,
			P50LatencyMs:        pm.metrics.P50TurnMs,
			P95LatencyMs:        pm.metrics.P95TurnMs,
			P99LatencyMs:        pm.metrics.P99TurnMs,
			ToolSuccessRate:     pm.metrics.ToolSuccessRate,
			ROIScore:            pm.metrics.ROIScore,
			TotalCostUSD:        pm.metrics.TotalCostUSD,
			SampleSize:          pm.metrics.SampleSize,
			Verdict:             pm.verdict.Type,
			RecommendedModel:    recommended,
			DecisionReason:      pm.verdict.Reason,
			AvgQualityScore:     pm.metrics.AvgQuality,
			AvgPromptTokens:     pm.metrics.AvgPromptTokens,
			AvgCompletionTokens: pm.metrics.AvgCompletionTokens,
			AvgTurnMs:           pm.metrics.AvgTurnMs,
			P95TurnMs:           pm.metrics.P95TurnMs,
			// ArtifactPath, RunKind, WindowStart, WindowEnd set by caller.
		}

		// Also patch the verdict so the artifact reflects the data-driven recommendation.
		v := pm.verdict
		v.RecommendedModel = recommended
		results = append(results, agentResult{verdict: v, run: run})

		r.logger.Info("agent/model benchmark complete",
			zap.String("agent_id", agentID),
			zap.String("model", model),
			zap.String("verdict", string(pm.verdict.Type)),
			zap.String("recommended", recommended),
			zap.Int("sample_size", pm.metrics.SampleSize),
		)
	}

	return results, nil
}

// bestAlternativeModel selects the best alternative model for an agent that needs
// a SWITCH, based on real benchmark data from other models used by the same agent
// in the current window.
//
// Selection criteria (priority order — same as our objective):
//  1. Accuracy first — the candidate must have equal or better accuracy
//  2. Within equal accuracy, prefer higher ROI (more accurate per dollar)
//  3. Within equal ROI, prefer lower avg turn time
//
// Returns empty string if no better alternative is found in the current window.
func bestAlternativeModel(currentModel string, current benchmark.WindowMetrics, perModel map[string]modelMetrics) string {
	bestModel := ""
	bestAcc := current.Accuracy
	bestROI := current.ROIScore
	bestTurn := current.AvgTurnMs

	for model, pm := range perModel {
		if model == currentModel {
			continue
		}
		m := pm.metrics
		// Must have sufficient data.
		if m.SampleSize < benchmark.MinSampleSize {
			continue
		}
		// Must not itself be flagged for urgent switch.
		if pm.verdict.Type == store.VerdictUrgentSwitch {
			continue
		}

		// Better if: higher accuracy, OR same accuracy with better ROI,
		// OR same accuracy+ROI with lower turn time.
		betterAcc := m.Accuracy > bestAcc
		sameAcc := m.Accuracy >= bestAcc-0.001
		betterROI := m.ROIScore > bestROI
		sameROI := m.ROIScore >= bestROI-0.001
		betterTurn := bestTurn <= 0 || (m.AvgTurnMs > 0 && m.AvgTurnMs < bestTurn)

		if betterAcc || (sameAcc && betterROI) || (sameAcc && sameROI && betterTurn) {
			bestModel = model
			bestAcc = m.Accuracy
			bestROI = m.ROIScore
			bestTurn = m.AvgTurnMs
		}
	}
	return bestModel
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
