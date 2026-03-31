package tracking_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/tracking"
)

// --- Helpers ---

// makeIngestArgs returns a valid arguments map for an ingest request.
func makeIngestArgs(overrides map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{
		"agent_id":   "agent-1",
		"session_id": "session-1",
		"event_type": "tool_call",
		"model":      "claude-sonnet-4-5",
		"timestamp":  "2026-01-01T12:00:00Z",
	}
	for k, v := range overrides {
		if v == nil {
			delete(args, k)
		} else {
			args[k] = v
		}
	}
	return args
}

// newQueueWithStore creates a queue backed by the given store, starts it,
// and returns it with a cleanup function.
func newQueueWithStore(t *testing.T, ms store.EventStore) *tracking.EventQueue {
	t.Helper()
	q := tracking.NewEventQueue(ms, 100, nil)
	q.Start()
	t.Cleanup(func() {
		q.Stop()
	})
	return q
}

// --- Unit Tests for HandleIngest ---

// TestIngestHandlerValidEvent verifies a valid request is accepted and enqueued.
func TestIngestHandlerValidEvent(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(nil),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest returned unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("HandleIngest returned isError=true: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("HandleIngest returned empty content")
	}
	t.Logf("result: %s", result.Content[0].Text)
}

// TestIngestHandlerRejectsMissingAgentID verifies validation of required agent_id.
func TestIngestHandlerRejectsMissingAgentID(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"agent_id": nil}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for missing agent_id")
	}
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Error("expected validation error message in content")
	}
	t.Logf("validation error: %s", result.Content[0].Text)
}

// TestIngestHandlerRejectsMissingSessionID verifies session_id is required.
func TestIngestHandlerRejectsMissingSessionID(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"session_id": nil}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for missing session_id")
	}
}

// TestIngestHandlerRejectsMissingEventType verifies event_type is required.
func TestIngestHandlerRejectsMissingEventType(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"event_type": nil}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for missing event_type")
	}
}

// TestIngestHandlerRejectsMissingModel verifies model is required.
func TestIngestHandlerRejectsMissingModel(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"model": nil}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for missing model")
	}
}

// TestIngestHandlerRejectsMissingTimestamp verifies timestamp is required.
func TestIngestHandlerRejectsMissingTimestamp(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"timestamp": nil}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for missing timestamp")
	}
}

// TestIngestHandlerRejectsInvalidEventType verifies unknown event types are rejected.
func TestIngestHandlerRejectsInvalidEventType(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"event_type": "unknown_type"}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for invalid event_type")
	}
}

// TestIngestHandlerRejectsInvalidTimestamp verifies timestamp format validation.
func TestIngestHandlerRejectsInvalidTimestamp(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"timestamp": "not-a-date"}),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for invalid timestamp")
	}
}

// TestIngestHandlerOptionalFieldsAreParsed verifies optional fields are correctly handled.
func TestIngestHandlerOptionalFieldsAreParsed(t *testing.T) {
	ms := &mockStore{}
	q := newQueueWithStore(t, ms)

	args := makeIngestArgs(map[string]interface{}{
		"duration_ms":       float64(250),
		"prompt_tokens":     float64(100),
		"completion_tokens": float64(50),
		"cost_usd":          0.01,
		"quality_score":     0.92,
		"rework_count":      float64(1),
		"tool_name":         "bash",
		"tool_success":      true,
		"metadata":          map[string]interface{}{"key": "value"},
	})

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: args,
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest: %v", err)
	}
	if result.IsError {
		t.Fatalf("HandleIngest isError=true: %v", result.Content)
	}
}

// TestIngestHandlerQueueClosedReturnsError verifies closed queue is handled.
func TestIngestHandlerQueueClosedReturnsError(t *testing.T) {
	ms := &mockStore{}
	q := tracking.NewEventQueue(ms, 10, nil)
	q.Start()
	q.Stop() // close the queue before enqueuing

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(nil),
	}

	result, err := tracking.HandleIngest(context.Background(), req, q)
	if err != nil {
		t.Fatalf("HandleIngest returned Go error (should be MCP error): %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when queue is closed")
	}
	if len(result.Content) == 0 {
		t.Error("expected error message in content")
	}
	t.Logf("closed queue error: %s", result.Content[0].Text)
}

// --- Integration Test with Direct Store Path ---

// TestIngestHandlerValidEventPersistsToSQLite tests the end-to-end path:
// HandleIngestDirect → EventStore → SQLite (via mockStore for unit-level test).
func TestIngestHandlerValidEventPersistsToSQLite(t *testing.T) {
	ms := &mockStore{}

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(nil),
	}

	result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
	if err != nil {
		t.Fatalf("HandleIngestDirect: %v", err)
	}
	if result.IsError {
		t.Fatalf("HandleIngestDirect isError=true: %v", result.Content)
	}

	if ms.Count() != 1 {
		t.Errorf("expected 1 persisted event, got %d", ms.Count())
	}

	events, _ := ms.QueryEvents(context.Background(), store.EventQuery{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event in store, got %d", len(events))
	}
	e := events[0]
	if e.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q, want %q", e.AgentID, "agent-1")
	}
	if e.EventType != "tool_call" {
		t.Errorf("EventType: got %q, want %q", e.EventType, "tool_call")
	}
}

// TestIngestHandlerInvalidPayloadLeavesStoreUnchanged verifies invalid requests
// don't write to the store.
func TestIngestHandlerInvalidPayloadLeavesStoreUnchanged(t *testing.T) {
	ms := &mockStore{}

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"agent_id": nil}),
	}

	result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
	if err != nil {
		t.Fatalf("HandleIngestDirect: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true for invalid request")
	}

	if ms.Count() != 0 {
		t.Errorf("invalid request should not write to store, got %d events", ms.Count())
	}
}

// TestAllRequiredFieldsValidation runs a table-driven test for all required fields.
func TestAllRequiredFieldsValidation(t *testing.T) {
	ms := &mockStore{}

	requiredFields := []string{"agent_id", "session_id", "event_type", "model", "timestamp"}

	for _, field := range requiredFields {
		t.Run("missing_"+field, func(t *testing.T) {
			req := mcp.CallToolRequest{
				Name:      "ingest",
				Arguments: makeIngestArgs(map[string]interface{}{field: nil}),
			}

			result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
			if err != nil {
				t.Fatalf("HandleIngestDirect: %v", err)
			}
			if !result.IsError {
				t.Errorf("missing %q: expected isError=true", field)
			}
			if ms.Count() != 0 {
				t.Errorf("missing %q: should not write to store", field)
			}
		})
	}
}

// TestValidEventTypes verifies each valid event_type is accepted.
func TestValidEventTypes(t *testing.T) {
	validTypes := []string{"start", "tool_call", "retry", "complete", "error"}
	for _, eventType := range validTypes {
		t.Run(eventType, func(t *testing.T) {
			ms := &mockStore{}
			req := mcp.CallToolRequest{
				Name:      "ingest",
				Arguments: makeIngestArgs(map[string]interface{}{"event_type": eventType}),
			}
			result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
			if err != nil {
				t.Fatalf("HandleIngestDirect: %v", err)
			}
			if result.IsError {
				t.Errorf("event_type=%q should be valid, got error: %v", eventType, result.Content)
			}
		})
	}
}

// TestIngestTimestampIsNormalizedToUTC verifies timestamps are stored in UTC.
func TestIngestTimestampIsNormalizedToUTC(t *testing.T) {
	ms := &mockStore{}

	// Send a timestamp with a non-UTC offset
	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(map[string]interface{}{"timestamp": "2026-01-01T09:00:00+05:00"}),
	}

	result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
	if err != nil {
		t.Fatalf("HandleIngestDirect: %v", err)
	}
	if result.IsError {
		t.Fatalf("valid timestamp rejected: %v", result.Content)
	}

	events, _ := ms.QueryEvents(context.Background(), store.EventQuery{})
	if len(events) == 0 {
		t.Fatal("no events stored")
	}
	if events[0].Timestamp.Location() != time.UTC {
		t.Errorf("expected UTC timestamp, got: %v", events[0].Timestamp.Location())
	}

	// 2026-01-01T09:00:00+05:00 → UTC is 04:00:00
	expectedUTC := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC)
	if !events[0].Timestamp.Equal(expectedUTC) {
		t.Errorf("timestamp not converted to UTC: got %v, want %v",
			events[0].Timestamp, expectedUTC)
	}
}

// TestIngestValidationErrorDoesNotProduceGoError ensures validation errors
// are returned as MCP-level errors, not Go errors.
func TestIngestValidationErrorDoesNotProduceGoError(t *testing.T) {
	ms := &mockStore{}

	badRequests := []map[string]interface{}{
		makeIngestArgs(map[string]interface{}{"agent_id": nil}),
		makeIngestArgs(map[string]interface{}{"event_type": "invalid"}),
		makeIngestArgs(map[string]interface{}{"timestamp": "bad-date"}),
	}

	for i, args := range badRequests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			req := mcp.CallToolRequest{Name: "ingest", Arguments: args}
			result, err := tracking.HandleIngestDirect(context.Background(), req, ms)
			if err != nil {
				t.Errorf("should not return Go error for validation failure: %v", err)
			}
			if !result.IsError {
				t.Error("expected isError=true")
			}
		})
	}
}

// mockStore is shared from queue_test.go via package-level declaration.
// However, since we are in package tracking_test, we need the compatible version.
// (The mockStore in queue_test.go is in the same package.)

// failingStore simulates store errors for testing error paths.
type failingStore struct{}

func (f *failingStore) InsertEvent(_ context.Context, _ store.Event) (string, error) {
	return "", errors.New("simulated store failure")
}
func (f *failingStore) QueryEvents(_ context.Context, _ store.EventQuery) ([]store.Event, error) {
	return nil, nil
}
func (f *failingStore) GetAgentEvents(_ context.Context, _ string, _ time.Time) ([]store.Event, error) {
	return nil, nil
}
func (f *failingStore) GetAgentSummary(_ context.Context, _ string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}

func (f *failingStore) CountEvents(_ context.Context, _ store.EventQuery) (int, error) {
	return 0, nil
}
func (f *failingStore) QuerySessions(_ context.Context, _ store.SessionQuery) ([]store.SessionSummary, error) {
	return nil, nil
}
func (f *failingStore) GetSessionEvents(_ context.Context, _ string) ([]store.Event, error) {
	return nil, nil
}
func (f *failingStore) Close() error { return nil }

// TestHandleIngestDirectStoreError verifies store errors are surfaced as MCP errors.
func TestHandleIngestDirectStoreError(t *testing.T) {
	fs := &failingStore{}

	req := mcp.CallToolRequest{
		Name:      "ingest",
		Arguments: makeIngestArgs(nil),
	}

	result, err := tracking.HandleIngestDirect(context.Background(), req, fs)
	if err != nil {
		t.Fatalf("HandleIngestDirect: unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when store fails")
	}
	t.Logf("store error: %s", result.Content[0].Text)
}
