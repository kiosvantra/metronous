package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

// TestEventMetadataRoundTrip verifies that metadata can be serialized to JSON
// and deserialized back without loss of data.
func TestEventMetadataRoundTrip(t *testing.T) {
	original := map[string]interface{}{
		"key1":   "value1",
		"count":  float64(42),
		"active": true,
	}

	jsonStr := store.MetadataToJSON(original)
	if jsonStr == "" {
		t.Fatal("MetadataToJSON returned empty string for non-nil map")
	}

	roundTripped := store.MetadataFromJSON(jsonStr)
	if roundTripped == nil {
		t.Fatal("MetadataFromJSON returned nil for valid JSON")
	}

	if roundTripped["key1"] != original["key1"] {
		t.Errorf("key1 mismatch: got %v, want %v", roundTripped["key1"], original["key1"])
	}
	if roundTripped["count"] != original["count"] {
		t.Errorf("count mismatch: got %v, want %v", roundTripped["count"], original["count"])
	}
	if roundTripped["active"] != original["active"] {
		t.Errorf("active mismatch: got %v, want %v", roundTripped["active"], original["active"])
	}
}

// TestEventMetadataFromJSONEmpty verifies that empty input returns nil.
func TestEventMetadataFromJSONEmpty(t *testing.T) {
	result := store.MetadataFromJSON("")
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

// TestEventMetadataFromJSONInvalid verifies that invalid JSON returns nil.
func TestEventMetadataFromJSONInvalid(t *testing.T) {
	result := store.MetadataFromJSON("{invalid json}")
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %v", result)
	}
}

// TestEventMetadataToJSONNil verifies that nil map returns empty string.
func TestEventMetadataToJSONNil(t *testing.T) {
	result := store.MetadataToJSON(nil)
	if result != "" {
		t.Errorf("expected empty string for nil map, got %q", result)
	}
}

// TestEventStructFields verifies that the Event struct has all required fields.
func TestEventStructFields(t *testing.T) {
	now := time.Now().UTC()
	dur := 500
	prompt := 100
	completion := 200
	cost := 0.05
	quality := 0.92
	rework := 0
	toolName := "bash"
	toolSuccess := true

	event := store.Event{
		ID:               "test-uuid",
		AgentID:          "agent-1",
		SessionID:        "session-1",
		EventType:        "tool_call",
		Model:            "claude-sonnet-4-5",
		Timestamp:        now,
		DurationMs:       &dur,
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		CostUSD:          &cost,
		QualityScore:     &quality,
		ReworkCount:      &rework,
		ToolName:         &toolName,
		ToolSuccess:      &toolSuccess,
		Metadata:         map[string]interface{}{"custom": "data"},
	}

	if event.ID != "test-uuid" {
		t.Errorf("ID mismatch")
	}
	if event.AgentID != "agent-1" {
		t.Errorf("AgentID mismatch")
	}
	if event.SessionID != "session-1" {
		t.Errorf("SessionID mismatch")
	}
	if event.EventType != "tool_call" {
		t.Errorf("EventType mismatch")
	}
	if event.Model != "claude-sonnet-4-5" {
		t.Errorf("Model mismatch")
	}
	if event.DurationMs == nil || *event.DurationMs != 500 {
		t.Errorf("DurationMs mismatch")
	}
	if event.ToolName == nil || *event.ToolName != "bash" {
		t.Errorf("ToolName mismatch")
	}
	if event.ToolSuccess == nil || !*event.ToolSuccess {
		t.Errorf("ToolSuccess mismatch")
	}
}

// TestEventQueryStructFields verifies EventQuery struct is usable.
func TestEventQueryStructFields(t *testing.T) {
	q := store.EventQuery{
		AgentID:   "agent-1",
		SessionID: "session-1",
		EventType: "tool_call",
		Since:     time.Now().Add(-24 * time.Hour),
		Until:     time.Now(),
		Limit:     100,
	}
	if q.AgentID != "agent-1" {
		t.Errorf("AgentID mismatch")
	}
	if q.Limit != 100 {
		t.Errorf("Limit mismatch")
	}
}

// TestAgentSummaryStructFields verifies AgentSummary struct is usable.
func TestAgentSummaryStructFields(t *testing.T) {
	s := store.AgentSummary{
		AgentID:      "agent-1",
		LastEventTs:  time.Now(),
		TotalEvents:  42,
		TotalCostUSD: 1.23,
		AvgQuality:   0.87,
	}
	if s.AgentID != "agent-1" {
		t.Errorf("AgentID mismatch")
	}
	if s.TotalEvents != 42 {
		t.Errorf("TotalEvents mismatch")
	}
}

// TestEventStoreInterface verifies EventStore interface is definable (compile check).
func TestEventStoreInterface(t *testing.T) {
	// Compile-time check: a nil pointer to a struct that implements EventStore
	// can be assigned to the interface without panicking.
	var _ store.EventStore = (*mockEventStore)(nil)
}

// mockEventStore is a no-op implementation used only for the interface compile check.
type mockEventStore struct{}

func (m *mockEventStore) InsertEvent(ctx context.Context, event store.Event) (string, error) {
	return event.ID, nil
}
func (m *mockEventStore) QueryEvents(ctx context.Context, _ store.EventQuery) ([]store.Event, error) {
	return nil, nil
}

func (m *mockEventStore) CountEvents(ctx context.Context, _ store.EventQuery) (int, error) {
	return 0, nil
}
func (m *mockEventStore) GetAgentEvents(ctx context.Context, _ string, _ time.Time) ([]store.Event, error) {
	return nil, nil
}
func (m *mockEventStore) GetAgentSummary(ctx context.Context, _ string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}
func (m *mockEventStore) Close() error { return nil }

// TestBenchmarkRunFieldMapping verifies BenchmarkRun struct has all required fields.
func TestBenchmarkRunFieldMapping(t *testing.T) {
	now := time.Now().UTC()
	run := store.BenchmarkRun{
		ID:               "run-uuid",
		RunAt:            now,
		WindowDays:       7,
		AgentID:          "code-agent",
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
		Verdict:          store.VerdictKeep,
		RecommendedModel: "",
		DecisionReason:   "All thresholds passed",
		ArtifactPath:     "/tmp/decisions_2024-01-14.json",
	}

	if run.ID != "run-uuid" {
		t.Errorf("ID mismatch")
	}
	if run.AgentID != "code-agent" {
		t.Errorf("AgentID mismatch")
	}
	if run.WindowDays != 7 {
		t.Errorf("WindowDays mismatch: got %d", run.WindowDays)
	}
	if run.Verdict != store.VerdictKeep {
		t.Errorf("Verdict mismatch: got %s", run.Verdict)
	}
	if run.P95LatencyMs != 2800 {
		t.Errorf("P95LatencyMs mismatch: got %f", run.P95LatencyMs)
	}
	if run.SampleSize != 150 {
		t.Errorf("SampleSize mismatch: got %d", run.SampleSize)
	}

	// Verify all VerdictType constants are defined.
	verdicts := []store.VerdictType{
		store.VerdictKeep,
		store.VerdictSwitch,
		store.VerdictUrgentSwitch,
		store.VerdictInsufficientData,
	}
	for _, v := range verdicts {
		if v == "" {
			t.Errorf("VerdictType constant is empty string")
		}
	}
}

// TestBenchmarkStoreInterface verifies BenchmarkStore interface is definable (compile check).
func TestBenchmarkStoreInterface(t *testing.T) {
	var _ store.BenchmarkStore = (*mockBenchmarkStore)(nil)
}

// mockBenchmarkStore is a no-op implementation used only for the interface compile check.
type mockBenchmarkStore struct{}

func (m *mockBenchmarkStore) SaveRun(ctx context.Context, run store.BenchmarkRun) error {
	return nil
}
func (m *mockBenchmarkStore) GetRuns(ctx context.Context, agentID string, limit int) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m *mockBenchmarkStore) GetLatestRun(ctx context.Context, agentID string) (*store.BenchmarkRun, error) {
	return nil, nil
}
func (m *mockBenchmarkStore) ListAgents(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockBenchmarkStore) GetVerdictTrend(ctx context.Context, agentID string, weeks int) ([]string, error) {
	return nil, nil
}
func (m *mockBenchmarkStore) Close() error { return nil }

// Verify JSON round-trip for Event metadata using stdlib json.
func TestEventMetadataJSONCompatibility(t *testing.T) {
	meta := map[string]interface{}{
		"nested": map[string]interface{}{
			"key": "value",
		},
		"list": []interface{}{"a", "b"},
	}

	jsonStr := store.MetadataToJSON(meta)
	if jsonStr == "" {
		t.Fatal("MetadataToJSON returned empty for complex metadata")
	}

	// Also verify it can be decoded via stdlib
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("JSON from MetadataToJSON is not valid JSON: %v", err)
	}
}
