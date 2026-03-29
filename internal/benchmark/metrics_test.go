package benchmark_test

import (
	"context"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/benchmark"
	"github.com/enduluc/metronous/internal/store"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

// TestCalculateAccuracy verifies accuracy calculation.
func TestCalculateAccuracy(t *testing.T) {
	tests := []struct {
		name      string
		completed int
		total     int
		want      float64
	}{
		{"all good", 10, 10, 1.0},
		{"some errors", 8, 10, 0.8},
		{"all error", 0, 10, 0.0},
		{"zero total", 0, 0, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := benchmark.CalculateAccuracy(tc.completed, tc.total)
			if got != tc.want {
				t.Errorf("CalculateAccuracy(%d, %d) = %f, want %f", tc.completed, tc.total, got, tc.want)
			}
		})
	}
}

// TestCalculateLatencyPercentilesKnownDataset verifies percentile calculation against a known dataset.
func TestCalculateLatencyPercentilesKnownDataset(t *testing.T) {
	// Dataset: 1..100 ms — known p50=50, p95=95, p99=99.
	durations := make([]int, 100)
	for i := range durations {
		durations[i] = i + 1
	}

	p50, p95, p99 := benchmark.CalculateLatencyPercentiles(durations)

	// With nearest-rank, p50 index = 50, value = 50.
	if p50 != 50 {
		t.Errorf("p50 = %f, want 50", p50)
	}
	if p95 != 95 {
		t.Errorf("p95 = %f, want 95", p95)
	}
	if p99 != 99 {
		t.Errorf("p99 = %f, want 99", p99)
	}
}

// TestCalculateLatencyPercentilesEmpty verifies empty input returns zeros.
func TestCalculateLatencyPercentilesEmpty(t *testing.T) {
	p50, p95, p99 := benchmark.CalculateLatencyPercentiles(nil)
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("expected (0,0,0), got (%f,%f,%f)", p50, p95, p99)
	}
}

// TestCalculateLatencyPercentilesDoesNotMutateInput verifies the input slice is unchanged.
func TestCalculateLatencyPercentilesDoesNotMutateInput(t *testing.T) {
	input := []int{5, 3, 1, 4, 2}
	original := make([]int, len(input))
	copy(original, input)

	benchmark.CalculateLatencyPercentiles(input)

	for i, v := range input {
		if v != original[i] {
			t.Errorf("input mutated at index %d: got %d, want %d", i, v, original[i])
		}
	}
}

// TestCalculateToolSuccessRate verifies tool success rate calculation.
func TestCalculateToolSuccessRate(t *testing.T) {
	tests := []struct {
		name    string
		success int
		total   int
		want    float64
	}{
		{"all success", 10, 10, 1.0},
		{"partial", 9, 10, 0.9},
		{"none", 0, 10, 0.0},
		{"no tools", 0, 0, 1.0}, // no tools observed → no failures
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := benchmark.CalculateToolSuccessRate(tc.success, tc.total)
			if got != tc.want {
				t.Errorf("CalculateToolSuccessRate(%d, %d) = %f, want %f", tc.success, tc.total, got, tc.want)
			}
		})
	}
}

// TestCalculateROIScore verifies ROI score behavior.
func TestCalculateROIScore(t *testing.T) {
	tests := []struct {
		name     string
		quality  float64
		accuracy float64
		cost     float64
		wantMin  float64
		wantMax  float64
	}{
		{"high quality low cost", 0.9, 0.95, 0.01, 5.0, 10.0},
		{"low quality high cost", 0.3, 0.7, 5.0, 0.0, 1.0},
		{"zero cost clamped to 10", 1.0, 1.0, 0.0, 9.0, 10.0},
		{"zero quality", 0.0, 0.9, 1.0, 0.0, 0.01},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := benchmark.CalculateROIScore(tc.quality, tc.accuracy, tc.cost)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("CalculateROIScore(%f, %f, %f) = %f, want [%f, %f]",
					tc.quality, tc.accuracy, tc.cost, got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

// TestAggregateMetricsBasic verifies AggregateMetrics computes fields correctly.
func TestAggregateMetricsBasic(t *testing.T) {
	dur500 := 500
	dur1500 := 1500
	cost1 := 0.1
	quality := 0.9
	toolName := "bash"
	toolSuccess := true

	events := []store.Event{
		{AgentID: "agent-a", EventType: "complete", Model: "claude-sonnet", DurationMs: &dur500, CostUSD: &cost1, QualityScore: &quality},
		{AgentID: "agent-a", EventType: "tool_call", Model: "claude-sonnet", DurationMs: &dur1500, ToolName: &toolName, ToolSuccess: &toolSuccess},
		{AgentID: "agent-a", EventType: "error", Model: "claude-sonnet"},
	}

	m := benchmark.AggregateMetrics("agent-a", events)

	if m.SampleSize != 3 {
		t.Errorf("SampleSize: got %d, want 3", m.SampleSize)
	}
	if m.Model != "claude-sonnet" {
		t.Errorf("Model: got %q, want claude-sonnet", m.Model)
	}
	// accuracy: 2 non-error / 3 total
	wantAccuracy := 2.0 / 3.0
	if m.Accuracy-wantAccuracy > 0.001 {
		t.Errorf("Accuracy: got %f, want ~%f", m.Accuracy, wantAccuracy)
	}
	// error rate: 1/3
	wantErrorRate := 1.0 / 3.0
	if m.ErrorRate-wantErrorRate > 0.001 {
		t.Errorf("ErrorRate: got %f, want ~%f", m.ErrorRate, wantErrorRate)
	}
	// 1 tool call, 1 success
	if m.ToolSuccessRate != 1.0 {
		t.Errorf("ToolSuccessRate: got %f, want 1.0", m.ToolSuccessRate)
	}
	if m.TotalCostUSD != 0.1 {
		t.Errorf("TotalCostUSD: got %f, want 0.1", m.TotalCostUSD)
	}
}

// TestAggregateMetricsEmptyEvents handles no events gracefully.
func TestAggregateMetricsEmptyEvents(t *testing.T) {
	m := benchmark.AggregateMetrics("empty-agent", nil)

	if m.SampleSize != 0 {
		t.Errorf("SampleSize: got %d, want 0", m.SampleSize)
	}
	if m.Accuracy != 0 {
		t.Errorf("Accuracy: got %f, want 0", m.Accuracy)
	}
}

// TestInsufficientDataClassification verifies that SampleSize < MinSampleSize
// is detected correctly.
func TestInsufficientDataClassification(t *testing.T) {
	// Generate exactly MinSampleSize-1 events.
	n := benchmark.MinSampleSize - 1
	events := make([]store.Event, n)
	for i := range events {
		events[i] = store.Event{AgentID: "small-agent", EventType: "complete", Model: "m"}
	}

	m := benchmark.AggregateMetrics("small-agent", events)

	if m.SampleSize >= benchmark.MinSampleSize {
		t.Errorf("expected SampleSize < %d, got %d", benchmark.MinSampleSize, m.SampleSize)
	}
}

// TestFetchEventsForWindow verifies FetchEventsForWindow uses the store correctly.
func TestFetchEventsForWindow(t *testing.T) {
	ctx := context.Background()
	es, err := sqlitestore.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	now := time.Now().UTC()
	dur := 500

	// Insert an event in the window.
	inWindow := store.Event{
		AgentID:    "fetch-agent",
		SessionID:  "s1",
		EventType:  "complete",
		Model:      "claude-sonnet",
		Timestamp:  now.Add(-3 * 24 * time.Hour),
		DurationMs: &dur,
	}
	// Insert an event outside the window.
	outWindow := store.Event{
		AgentID:    "fetch-agent",
		SessionID:  "s2",
		EventType:  "complete",
		Model:      "claude-sonnet",
		Timestamp:  now.Add(-10 * 24 * time.Hour),
		DurationMs: &dur,
	}

	if _, err := es.InsertEvent(ctx, inWindow); err != nil {
		t.Fatalf("InsertEvent in-window: %v", err)
	}
	if _, err := es.InsertEvent(ctx, outWindow); err != nil {
		t.Fatalf("InsertEvent out-window: %v", err)
	}

	start := now.Add(-7 * 24 * time.Hour)
	end := now

	events, err := benchmark.FetchEventsForWindow(ctx, es, "fetch-agent", start, end)
	if err != nil {
		t.Fatalf("FetchEventsForWindow: %v", err)
	}

	if len(events) != 1 {
		t.Errorf("expected 1 event in window, got %d", len(events))
	}
}
