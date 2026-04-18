package benchmark

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/store"
)

// WindowMetrics holds all computed metrics for a single agent over a time window.
type WindowMetrics struct {
	// AgentID is the agent these metrics belong to.
	AgentID string

	// Model is the model used during the window (normalized, no provider prefix).
	Model string

	// SampleSize is the total number of events in the window.
	SampleSize int

	// Accuracy is the ratio of non-error events to total events.
	Accuracy float64

	// ErrorRate is the ratio of error events to total events.
	ErrorRate float64

	// AvgTurnMs is the mean turn duration in milliseconds.
	// A "turn" is defined as the time from start to complete (duration_ms on
	// complete events only). tool_call duration is always 0 and is excluded.
	AvgTurnMs float64

	// P50TurnMs is the 50th-percentile turn duration in milliseconds.
	P50TurnMs float64

	// P95TurnMs is the 95th-percentile turn duration in milliseconds.
	P95TurnMs float64

	// P99TurnMs is the 99th-percentile turn duration in milliseconds.
	P99TurnMs float64

	// AvgPromptTokens is the mean number of prompt tokens per complete event.
	AvgPromptTokens float64

	// AvgCompletionTokens is the mean number of completion tokens per complete event.
	AvgCompletionTokens float64

	// ROIScore is accuracy / cost_per_session.
	// Zero when cost data is unavailable (free models or no billing data).
	ROIScore float64

	// TotalCostUSD is the total cost for the window, computed as the
	// sum of the maximum cost_usd per distinct session. This correctly
	// handles cumulative cost_usd values emitted by the OpenCode plugin.
	TotalCostUSD float64

	// SessionCount is the number of distinct sessions observed in the window.
	SessionCount int

	// Deprecated fields kept for backward compatibility with the decision engine
	// and benchmark store until a full migration is done.
	AvgLatencyMs    float64
	P50LatencyMs    float64
	P95LatencyMs    float64
	P99LatencyMs    float64
	ToolSuccessRate float64
	AvgQuality      float64
}

// FetchEventsForWindow retrieves all events for the given agent within the time window.
// If start and end are both zero, retrieves all events (used for historical metrics).
func FetchEventsForWindow(ctx context.Context, es store.EventStore, agentID string, start, end time.Time) ([]store.Event, error) {
	query := store.EventQuery{
		AgentID: agentID,
	}
	// Only set time bounds if provided (non-zero).
	if !start.IsZero() {
		query.Since = start
	}
	if !end.IsZero() {
		query.Until = end
	}
	return es.QueryEvents(ctx, query)
}

// AggregateMetrics computes WindowMetrics from a slice of events.
// If the event slice has fewer than MinSampleSize events, the returned
// WindowMetrics will have SampleSize < MinSampleSize and the decision
// engine should assign INSUFFICIENT_DATA.
//
// Turn latency is derived exclusively from complete events (duration_ms),
// which represents the time from the start of a turn to its completion.
// tool_call duration_ms is always 0 and is not included.
func AggregateMetrics(logger *zap.Logger, agentID string, events []store.Event) WindowMetrics {
	m := WindowMetrics{
		AgentID:    agentID,
		SampleSize: len(events),
	}

	if len(events) == 0 {
		return m
	}

	var (
		turnDurations   []int // duration_ms from complete events only
		errorCount      int
		modelCounts     = make(map[string]int)
		sessionSeen     = make(map[string]struct{})
		sessionMaxCost  = make(map[string]float64)
		totalPrompt     int64
		totalCompletion int64
		tokenCount      int
		totalQuality    float64
		qualityCount    int
	)

	for _, e := range events {
		modelCounts[e.Model]++

		if e.SessionID != "" {
			sessionSeen[e.SessionID] = struct{}{}
		}

		if e.EventType == "error" {
			errorCount++
		}

		// cost_usd is cumulative per session — track MAX per session.
		if e.CostUSD != nil && e.SessionID != "" {
			if *e.CostUSD > sessionMaxCost[e.SessionID] {
				sessionMaxCost[e.SessionID] = *e.CostUSD
			}
		} else if e.CostUSD != nil && *e.CostUSD > 0 {
			if logger != nil {
				logger.Warn("dropping cost for event with missing session_id",
					zap.Float64("cost_usd", *e.CostUSD),
					zap.String("agent_id", agentID),
				)
			}
		}

		// Turn latency: only complete events carry meaningful duration_ms.
		// tool_call duration_ms is always 0 — excluded to avoid noise.
		if e.EventType == "complete" && e.DurationMs != nil && *e.DurationMs > 0 {
			turnDurations = append(turnDurations, *e.DurationMs)
		}

		// Token counts: only complete events have real token data.
		if e.EventType == "complete" && e.PromptTokens != nil && e.CompletionTokens != nil {
			totalPrompt += int64(*e.PromptTokens)
			totalCompletion += int64(*e.CompletionTokens)
			tokenCount++
		}

		// Quality score (deprecated — kept for backward compat).
		if e.QualityScore != nil {
			totalQuality += *e.QualityScore
			qualityCount++
		}
	}

	// Dominant model (caller overrides this with the per-model filter in processAgentAllModels).
	m.Model = dominantModel(modelCounts)

	// Accuracy = non-error / total.
	m.Accuracy = CalculateAccuracy(len(events)-errorCount, len(events))
	m.ErrorRate = CalculateErrorRate(errorCount, len(events))

	// Turn latency percentiles (complete events only).
	m.AvgTurnMs = CalculateAvgLatency(turnDurations)
	m.P50TurnMs, m.P95TurnMs, m.P99TurnMs = CalculateLatencyPercentiles(turnDurations)

	// Backfill deprecated fields so existing decision engine and store keep working.
	m.AvgLatencyMs = m.AvgTurnMs
	m.P50LatencyMs = m.P50TurnMs
	m.P95LatencyMs = m.P95TurnMs
	m.P99LatencyMs = m.P99TurnMs

	// Token efficiency.
	if tokenCount > 0 {
		m.AvgPromptTokens = float64(totalPrompt) / float64(tokenCount)
		m.AvgCompletionTokens = float64(totalCompletion) / float64(tokenCount)
	}

	// Cost: sum MAX cost_usd per session.
	var totalCost float64
	for _, maxCost := range sessionMaxCost {
		totalCost += maxCost
	}
	m.TotalCostUSD = totalCost
	m.SessionCount = len(sessionSeen)

	// ROI = accuracy / cost_per_session.
	// Replaces tool_success_rate/cost because tool_success_rate is always 1.0.
	var costPerSession float64
	if m.SessionCount > 0 {
		costPerSession = totalCost / float64(m.SessionCount)
	}
	if costPerSession > 0 {
		m.ROIScore = m.Accuracy / costPerSession
	}

	// ToolSuccessRate kept for backward compat — always 1.0 in practice.
	m.ToolSuccessRate = 1.0

	// AvgQuality kept for backward compat.
	if qualityCount > 0 {
		m.AvgQuality = totalQuality / float64(qualityCount)
	}

	return m
}

// GroupEventsByModel partitions events into separate slices keyed by model.
// This enables per-model metric computation instead of mixing all models
// together and picking a "dominant" one.
func GroupEventsByModel(events []store.Event) map[string][]store.Event {
	groups := make(map[string][]store.Event)
	for _, e := range events {
		normalized := NormalizeModelName(e.Model)
		groups[normalized] = append(groups[normalized], e)
	}
	return groups
}

// dominantModel returns the model with the highest event count.
func dominantModel(counts map[string]int) string {
	var best string
	var bestCount int
	for model, count := range counts {
		if count > bestCount || (count == bestCount && model < best) {
			best = model
			bestCount = count
		}
	}
	return best
}
