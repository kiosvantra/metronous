package benchmark

import (
	"fmt"
	"math"

	"github.com/kiosvantra/metronous/internal/store"
)

// MetricDelta represents the difference between two models for a single metric.
type MetricDelta struct {
	// MetricName is the human-readable metric name.
	MetricName string

	// ModelAValue is the metric value for model A.
	ModelAValue float64

	// ModelBValue is the metric value for model B.
	ModelBValue float64

	// Delta is B - A (positive = B is higher).
	Delta float64

	// DeltaPct is the percentage change: (B-A)/A * 100; 0 if A is 0.
	DeltaPct float64

	// BetterModel is "A", "B", or "tie".
	BetterModel string
}

// ModelComparison holds the full side-by-side comparison of two benchmark runs.
type ModelComparison struct {
	// AgentID is the agent being compared.
	AgentID string

	// ModelA is the first model name.
	ModelA string

	// ModelB is the second model name.
	ModelB string

	// ScoreA is the composite score for run A.
	ScoreA float64

	// ScoreB is the composite score for run B.
	ScoreB float64

	// ScoreDelta is ScoreA - ScoreB (positive = A better).
	ScoreDelta float64

	// Deltas contains per-metric deltas (Score, Accuracy, P95Latency, ToolSuccess, Cost).
	Deltas []MetricDelta

	// Winner is "A", "B", or "tie" based on CompositeScore comparison.
	Winner string

	// BetterModel is the model identifier of the winner, or "" when tied.
	BetterModel string

	// Recommendation is a generated human-readable sentence.
	Recommendation string

	// AHasInsufficient is true when runA.Verdict == INSUFFICIENT_DATA.
	AHasInsufficient bool

	// BHasInsufficient is true when runB.Verdict == INSUFFICIENT_DATA.
	BHasInsufficient bool

	// CostDelta is A.TotalCostUSD - B.TotalCostUSD.
	CostDelta float64

	// CostDeltaPct is ((A-B)/B)*100; 0 when B cost is 0.
	CostDeltaPct float64

	// AccuracyDelta is A.Accuracy - B.Accuracy.
	AccuracyDelta float64

	// LatencyDeltaMs is A.P95LatencyMs - B.P95LatencyMs (negative = A faster).
	LatencyDeltaMs float64

	// ToolSuccessDelta is A.ToolSuccessRate - B.ToolSuccessRate.
	ToolSuccessDelta float64

	// ROIDelta is A.ROIScore - B.ROIScore.
	ROIDelta float64
}

// tieMargin is the absolute score margin within which two models are considered tied.
// Spec FR-COMP-EC-03 says |delta| < 0.01. We add a small float64 epsilon to handle
// cases like 0.76 - 0.75 which in binary floating point is slightly above 0.01.
const tieMargin = 0.01 + 1e-10

// CompareModels produces a pairwise comparison between two BenchmarkRun records.
// runA and runB may belong to any agent (the caller is responsible for filtering).
// This is a PURE FUNCTION: no I/O, no DB calls, no side effects.
func CompareModels(runA, runB store.BenchmarkRun) ModelComparison {
	scoreDelta := runA.CompositeScore - runB.CompositeScore

	// Determine winner by composite score.
	var winner, betterModel string
	if math.Abs(scoreDelta) <= tieMargin {
		winner = "tie"
		betterModel = ""
	} else if scoreDelta > 0 {
		winner = "A"
		betterModel = runA.Model
	} else {
		winner = "B"
		betterModel = runB.Model
	}

	// Compute individual metric deltas.
	costDelta := runA.TotalCostUSD - runB.TotalCostUSD
	var costDeltaPct float64
	if runB.TotalCostUSD != 0 {
		costDeltaPct = (costDelta / runB.TotalCostUSD) * 100
	}

	accuracyDelta := runA.Accuracy - runB.Accuracy
	latencyDelta := runA.P95LatencyMs - runB.P95LatencyMs
	toolDelta := runA.ToolSuccessRate - runB.ToolSuccessRate
	roiDelta := runA.ROIScore - runB.ROIScore

	deltas := []MetricDelta{
		buildDelta("Composite Score", runA.CompositeScore, runB.CompositeScore, true, tieMargin),
		buildDelta("Accuracy", runA.Accuracy, runB.Accuracy, true, 0.02),
		buildDelta("P95 Latency", runA.P95LatencyMs, runB.P95LatencyMs, false, 0),
		buildDelta("Tool Success", runA.ToolSuccessRate, runB.ToolSuccessRate, true, 0.02),
		buildDelta("Cost", runA.TotalCostUSD, runB.TotalCostUSD, false, 0),
	}

	// Generate recommendation sentence.
	recommendation := buildRecommendation(winner, betterModel, scoreDelta, runA, runB)

	return ModelComparison{
		AgentID:          runA.AgentID,
		ModelA:           runA.Model,
		ModelB:           runB.Model,
		ScoreA:           runA.CompositeScore,
		ScoreB:           runB.CompositeScore,
		ScoreDelta:       scoreDelta,
		Deltas:           deltas,
		Winner:           winner,
		BetterModel:      betterModel,
		Recommendation:   recommendation,
		AHasInsufficient: runA.Verdict == store.VerdictInsufficientData,
		BHasInsufficient: runB.Verdict == store.VerdictInsufficientData,
		CostDelta:        costDelta,
		CostDeltaPct:     costDeltaPct,
		AccuracyDelta:    accuracyDelta,
		LatencyDeltaMs:   latencyDelta,
		ToolSuccessDelta: toolDelta,
		ROIDelta:         roiDelta,
	}
}

// buildDelta constructs a MetricDelta for one metric.
// higherIsBetter indicates whether a higher value is preferred.
// tiePct is the absolute margin within which the metric is considered tied (0 = no tie logic).
func buildDelta(name string, aVal, bVal float64, higherIsBetter bool, tiePct float64) MetricDelta {
	delta := bVal - aVal // positive = B higher
	var deltaPct float64
	if aVal != 0 {
		deltaPct = (delta / math.Abs(aVal)) * 100
	}

	var better string
	if tiePct > 0 && math.Abs(delta) <= tiePct {
		better = "tie"
	} else if delta == 0 {
		better = "tie"
	} else if higherIsBetter {
		if delta > 0 {
			better = "B"
		} else {
			better = "A"
		}
	} else {
		// Lower is better (latency, cost).
		if delta < 0 {
			better = "B" // B is lower → B wins
		} else {
			better = "A"
		}
	}

	return MetricDelta{
		MetricName:  name,
		ModelAValue: aVal,
		ModelBValue: bVal,
		Delta:       delta,
		DeltaPct:    deltaPct,
		BetterModel: better,
	}
}

// buildRecommendation generates the human-readable recommendation sentence.
func buildRecommendation(winner, betterModel string, scoreDelta float64, runA, runB store.BenchmarkRun) string {
	if winner == "tie" {
		return "Both models are equivalent (score delta < 0.01)"
	}

	scoreDeltaPts := math.Abs(scoreDelta) * 100

	var winnerRun, loserRun store.BenchmarkRun
	if winner == "A" {
		winnerRun = runA
		loserRun = runB
	} else {
		winnerRun = runB
		loserRun = runA
	}

	// Accuracy delta relative to loser.
	var accPct float64
	if loserRun.Accuracy != 0 {
		accPct = (winnerRun.Accuracy - loserRun.Accuracy) / loserRun.Accuracy * 100
	}

	// Cost delta relative to winner (positive = winner is more expensive).
	var costPct float64
	if winnerRun.TotalCostUSD != 0 {
		costPct = (winnerRun.TotalCostUSD - loserRun.TotalCostUSD) / math.Abs(loserRun.TotalCostUSD+1e-10) * 100
	}

	return fmt.Sprintf("%s scores %.1fpts higher overall (%+.1f%% accuracy, %+.1f%% cost)",
		betterModel, scoreDeltaPts, accPct, costPct)
}
