package benchmark

import (
	"context"
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

	// TotalCostUSD is the sum of all event costs in the window.
	TotalCostUSD float64

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
		durations    []int
		totalCost    float64
		totalQuality float64
		qualityCount int
		errorCount   int
		toolTotal    int
		toolSuccess  int
		modelCounts  = make(map[string]int)
	)

	for _, e := range events {
		// Count by model to find the dominant model.
		modelCounts[e.Model]++

		if e.EventType == "error" {
			errorCount++
		}

		if e.DurationMs != nil {
			durations = append(durations, *e.DurationMs)
		}

		if e.CostUSD != nil {
			totalCost += *e.CostUSD
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

	// Cost.
	m.TotalCostUSD = totalCost

	// Quality average.
	if qualityCount > 0 {
		m.AvgQuality = totalQuality / float64(qualityCount)
	}

	// ROI score.
	m.ROIScore = CalculateROIScore(m.AvgQuality, m.Accuracy, totalCost)

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
