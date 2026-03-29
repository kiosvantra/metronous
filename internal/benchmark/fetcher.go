package benchmark

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/enduluc/metronous/internal/store"
)

// WindowMetrics holds all computed metrics for a single agent over a time window.
type WindowMetrics struct {
	// AgentID is the agent these metrics belong to.
	AgentID string

	// Model is the most common model used during the window.
	Model string

	// SampleSize is the number of events in the window.
	SampleSize int

	// Accuracy is the ratio of non-error events to total events.
	Accuracy float64

	// ErrorRate is the ratio of error events to total events.
	ErrorRate float64

	// AvgLatencyMs is the mean event duration in milliseconds.
	AvgLatencyMs float64

	// P50LatencyMs is the 50th-percentile latency in milliseconds.
	P50LatencyMs float64

	// P95LatencyMs is the 95th-percentile latency in milliseconds.
	P95LatencyMs float64

	// P99LatencyMs is the 99th-percentile latency in milliseconds.
	P99LatencyMs float64

	// ToolSuccessRate is the fraction of tool_call events that succeeded.
	ToolSuccessRate float64

	// ROIScore is the composite quality/cost ratio.
	ROIScore float64

	// TotalCostUSD is the total cost for the window, computed as the
	// sum of the maximum cost_usd per distinct session. This correctly
	// handles cumulative cost_usd values emitted by the OpenCode plugin.
	TotalCostUSD float64

	// SessionCount is the number of distinct sessions observed in the window.
	SessionCount int

	// AvgQuality is the mean quality score across all rated events.
	AvgQuality float64
}

// FetchEventsForWindow retrieves all events for the given agent within the time window.
func FetchEventsForWindow(ctx context.Context, es store.EventStore, agentID string, start, end time.Time) ([]store.Event, error) {
	return es.QueryEvents(ctx, store.EventQuery{
		AgentID: agentID,
		Since:   start,
		Until:   end,
	})
}

// AggregateMetrics computes WindowMetrics from a slice of events.
// If the event slice has fewer than MinSampleSize events, the returned
// WindowMetrics will have SampleSize < MinSampleSize and the decision
// engine should assign INSUFFICIENT_DATA.
func AggregateMetrics(agentID string, events []store.Event) WindowMetrics {
	m := WindowMetrics{
		AgentID:    agentID,
		SampleSize: len(events),
	}

	if len(events) == 0 {
		return m
	}

	var (
		durations      []int
		totalQuality   float64
		qualityCount   int
		errorCount     int
		toolTotal      int
		toolSuccess    int
		modelCounts    = make(map[string]int)
		sessionSeen    = make(map[string]struct{})
		sessionMaxCost = make(map[string]float64) // max cost_usd per session (cumulative values)
	)

	for _, e := range events {
		// Count by model to find the dominant model.
		modelCounts[e.Model]++

		// Track distinct sessions.
		if e.SessionID != "" {
			sessionSeen[e.SessionID] = struct{}{}
		}

		if e.EventType == "error" {
			errorCount++
		}

		if e.DurationMs != nil {
			durations = append(durations, *e.DurationMs)
		}

		// cost_usd in stored events is always a cumulative session total emitted
		// by the plugin — MAX per session gives the true cost for that session.
		if e.CostUSD != nil {
			if e.SessionID != "" {
				if *e.CostUSD > sessionMaxCost[e.SessionID] {
					sessionMaxCost[e.SessionID] = *e.CostUSD
				}
			} else if *e.CostUSD > 0 {
				fmt.Fprintf(os.Stderr, "metronous/benchmark: dropping cost %.4f for agent %s — missing session_id\n", *e.CostUSD, agentID)
			}
		}

		if e.QualityScore != nil {
			totalQuality += *e.QualityScore
			qualityCount++
		}

		if e.EventType == "tool_call" {
			toolTotal++
			if e.ToolSuccess != nil && *e.ToolSuccess {
				toolSuccess++
			}
		}
	}

	// Dominant model.
	m.Model = dominantModel(modelCounts)

	// Accuracy = non-error / total.
	m.Accuracy = CalculateAccuracy(len(events)-errorCount, len(events))
	m.ErrorRate = CalculateErrorRate(errorCount, len(events))

	// Latency percentiles.
	m.AvgLatencyMs = CalculateAvgLatency(durations)
	m.P50LatencyMs, m.P95LatencyMs, m.P99LatencyMs = CalculateLatencyPercentiles(durations)

	// Tool success rate.
	m.ToolSuccessRate = CalculateToolSuccessRate(toolSuccess, toolTotal)

	// Cost: sum the MAX cost_usd per session (each session's final cumulative total).
	var totalCost float64
	for _, maxCost := range sessionMaxCost {
		totalCost += maxCost
	}
	m.TotalCostUSD = totalCost

	// Session count.
	m.SessionCount = len(sessionSeen)

	// Quality average.
	if qualityCount > 0 {
		m.AvgQuality = totalQuality / float64(qualityCount)
	}

	// ROI score: tool_success_rate / cost_per_session.
	// cost_per_session = total_cost / session_count (or 0 if no sessions).
	var costPerSession float64
	if m.SessionCount > 0 {
		costPerSession = totalCost / float64(m.SessionCount)
	}
	m.ROIScore = CalculateROIScore(m.ToolSuccessRate, costPerSession)

	return m
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
