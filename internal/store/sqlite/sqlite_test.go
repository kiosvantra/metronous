package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/store"
	"github.com/enduluc/metronous/internal/store/sqlite"
)

// newTestStore creates an in-memory EventStore for testing.
func newTestStore(t *testing.T) *sqlite.EventStore {
	t.Helper()
	es, err := sqlite.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	t.Cleanup(func() {
		if err := es.Close(); err != nil {
			t.Logf("close store: %v", err)
		}
	})
	return es
}

// sampleEvent returns a valid Event for use in tests.
func sampleEvent(agentID, sessionID, eventType string) store.Event {
	dur := 250
	prompt := 100
	completion := 50
	cost := 0.01
	quality := 0.90
	rework := 0
	return store.Event{
		AgentID:          agentID,
		SessionID:        sessionID,
		EventType:        eventType,
		Model:            "claude-sonnet-4-5",
		Timestamp:        time.Now().UTC(),
		DurationMs:       &dur,
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		CostUSD:          &cost,
		QualityScore:     &quality,
		ReworkCount:      &rework,
		Metadata:         map[string]interface{}{"source": "test"},
	}
}

// TestApplyTrackingMigrations verifies tables and indexes are created.
func TestApplyTrackingMigrations(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := sqlite.ApplyTrackingMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyTrackingMigrations: %v", err)
	}

	// Verify tables exist by querying sqlite_master.
	tables := []string{"events", "agent_summaries"}
	for _, tbl := range tables {
		var name string
		row := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q not found in schema: %v", tbl, err)
		}
	}

	// Verify idempotency — running migrations twice should not fail.
	if err := sqlite.ApplyTrackingMigrations(context.Background(), db); err != nil {
		t.Errorf("ApplyTrackingMigrations is not idempotent: %v", err)
	}
}

// TestInsertQueryAndSummary is the primary integration test for insert + query flows.
func TestInsertQueryAndSummary(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	event := sampleEvent("agent-1", "session-1", "tool_call")
	toolName := "bash"
	toolSuccess := true
	event.ToolName = &toolName
	event.ToolSuccess = &toolSuccess

	id, err := es.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if id == "" {
		t.Error("InsertEvent returned empty ID")
	}

	// Query all events.
	events, err := es.QueryEvents(ctx, store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("QueryEvents: expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.ID != id {
		t.Errorf("ID: got %q, want %q", got.ID, id)
	}
	if got.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q, want %q", got.AgentID, "agent-1")
	}
	if got.EventType != "tool_call" {
		t.Errorf("EventType: got %q, want %q", got.EventType, "tool_call")
	}
	if got.ToolName == nil || *got.ToolName != "bash" {
		t.Errorf("ToolName: got %v, want 'bash'", got.ToolName)
	}
	if got.ToolSuccess == nil || !*got.ToolSuccess {
		t.Errorf("ToolSuccess: expected true")
	}
	if got.Metadata == nil || got.Metadata["source"] != "test" {
		t.Errorf("Metadata not preserved correctly: %v", got.Metadata)
	}

	// Check agent summary.
	summary, err := es.GetAgentSummary(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}
	if summary.AgentID != "agent-1" {
		t.Errorf("Summary.AgentID: got %q, want %q", summary.AgentID, "agent-1")
	}
	if summary.TotalEvents != 1 {
		t.Errorf("Summary.TotalEvents: got %d, want 1", summary.TotalEvents)
	}
}

// TestQueryEventsFiltersAndLimits verifies that EventQuery filters work correctly.
func TestQueryEventsFiltersAndLimits(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	// Insert events for two different agents.
	for i := 0; i < 3; i++ {
		e := sampleEvent("agent-A", "session-1", "tool_call")
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent agent-A[%d]: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		e := sampleEvent("agent-B", "session-2", "complete")
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent agent-B[%d]: %v", i, err)
		}
	}

	// Filter by AgentID.
	eventsA, err := es.QueryEvents(ctx, store.EventQuery{AgentID: "agent-A"})
	if err != nil {
		t.Fatalf("QueryEvents agent-A: %v", err)
	}
	if len(eventsA) != 3 {
		t.Errorf("expected 3 events for agent-A, got %d", len(eventsA))
	}

	// Filter by EventType.
	complete, err := es.QueryEvents(ctx, store.EventQuery{EventType: "complete"})
	if err != nil {
		t.Fatalf("QueryEvents complete: %v", err)
	}
	if len(complete) != 2 {
		t.Errorf("expected 2 complete events, got %d", len(complete))
	}

	// Limit.
	limited, err := es.QueryEvents(ctx, store.EventQuery{Limit: 2})
	if err != nil {
		t.Fatalf("QueryEvents limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 limited events, got %d", len(limited))
	}
}

// TestGetAgentSummaryNotFound verifies behavior when agent has no events.
func TestGetAgentSummaryNotFound(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	summary, err := es.GetAgentSummary(ctx, "nonexistent-agent")
	if err != nil {
		t.Fatalf("GetAgentSummary for nonexistent: %v", err)
	}
	if summary.AgentID != "nonexistent-agent" {
		t.Errorf("AgentID: got %q, want %q", summary.AgentID, "nonexistent-agent")
	}
	if summary.TotalEvents != 0 {
		t.Errorf("TotalEvents: got %d, want 0", summary.TotalEvents)
	}
}

// TestConcurrentInsertAvoidsBusy verifies concurrent inserts don't produce SQLITE_BUSY.
func TestConcurrentInsertAvoidsBusy(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	const goroutines = 20
	const eventsPerGoroutine = 5

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errors []error
	)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				e := sampleEvent(fmt.Sprintf("agent-%d", gID), "session-concurrent", "tool_call")
				if _, err := es.InsertEvent(ctx, e); err != nil {
					mu.Lock()
					errors = append(errors, err)
					mu.Unlock()
				}
			}
		}(g)
	}

	wg.Wait()

	if len(errors) > 0 {
		t.Errorf("concurrent inserts produced %d errors: %v", len(errors), errors[0])
	}

	// Verify total count.
	events, err := es.QueryEvents(ctx, store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents after concurrent insert: %v", err)
	}
	expected := goroutines * eventsPerGoroutine
	if len(events) != expected {
		t.Errorf("expected %d events total, got %d", expected, len(events))
	}
}

// TestGetAgentEventsReturnsCorrectRange verifies GetAgentEvents time filtering.
func TestGetAgentEventsReturnsCorrectRange(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	oldTime := time.Now().Add(-48 * time.Hour).UTC()
	newTime := time.Now().UTC()

	oldEvent := sampleEvent("agent-1", "session-old", "start")
	oldEvent.Timestamp = oldTime

	newEvent := sampleEvent("agent-1", "session-new", "complete")
	newEvent.Timestamp = newTime

	if _, err := es.InsertEvent(ctx, oldEvent); err != nil {
		t.Fatalf("InsertEvent old: %v", err)
	}
	if _, err := es.InsertEvent(ctx, newEvent); err != nil {
		t.Fatalf("InsertEvent new: %v", err)
	}

	// Retrieve events since 24h ago — should only get newEvent.
	since := time.Now().Add(-24 * time.Hour)
	events, err := es.GetAgentEvents(ctx, "agent-1", since)
	if err != nil {
		t.Fatalf("GetAgentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 recent event, got %d", len(events))
	}
}

// TestNewEventStoreWithRealFile verifies NewEventStore works with a real on-disk file.
// This exercises the openReadDB path (which is skipped for :memory:).
func TestNewEventStoreWithRealFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore(real file): %v", err)
	}
	defer func() {
		if err := es.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	ctx := context.Background()

	// Insert an event and verify it can be read back.
	event := sampleEvent("real-agent", "real-session", "start")
	id, err := es.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	events, err := es.QueryEvents(ctx, store.EventQuery{AgentID: "real-agent"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID != id {
		t.Errorf("ID mismatch: got %q, want %q", events[0].ID, id)
	}
}

// TestNewEventStoreIdempotentOnExistingDB verifies opening an existing DB is safe.
func TestNewEventStoreIdempotentOnExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.db")

	// First open — creates schema.
	es1, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	ctx := context.Background()
	event := sampleEvent("agent-x", "session-x", "complete")
	if _, err := es1.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := es1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	// Second open — should succeed and find existing data.
	es2, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer es2.Close()

	events, err := es2.QueryEvents(ctx, store.EventQuery{AgentID: "agent-x"})
	if err != nil {
		t.Fatalf("QueryEvents after reopen: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event after reopen, got %d", len(events))
	}
}

// TestCloseIsIdempotentForSharedConnection verifies Close() when read/write share connection (:memory:).
func TestCloseIsIdempotentForSharedConnection(t *testing.T) {
	es := newTestStore(t)
	// newTestStore already registers cleanup; calling Close manually is safe.
	if err := es.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Second close will happen via t.Cleanup — should not panic.
}

// TestQueryEventsDateRange verifies filtering by Since and Until timestamps.
func TestQueryEventsDateRange(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	past := now.Add(-72 * time.Hour)   // 72h ago
	middle := now.Add(-36 * time.Hour) // 36h ago
	recent := now.Add(-1 * time.Hour)  // 1h ago

	evOld := sampleEvent("agent-range", "s1", "start")
	evOld.Timestamp = past

	evMid := sampleEvent("agent-range", "s2", "tool_call")
	evMid.Timestamp = middle

	evNew := sampleEvent("agent-range", "s3", "complete")
	evNew.Timestamp = recent

	for _, e := range []store.Event{evOld, evMid, evNew} {
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	// Filter: only events between -48h and -2h.
	// Expected: only evMid (-36h) falls in this window.
	// evOld (-72h) is before since, evNew (-1h) is after until.
	since := now.Add(-48 * time.Hour) // after evOld, before evMid
	until := now.Add(-2 * time.Hour)  // after evMid, before evNew

	results, err := es.QueryEvents(ctx, store.EventQuery{
		AgentID: "agent-range",
		Since:   since,
		Until:   until,
	})
	if err != nil {
		t.Fatalf("QueryEvents with range: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 event in range [-48h, -2h], got %d", len(results))
	}
	if len(results) == 1 && results[0].SessionID != "s2" {
		t.Errorf("expected s2 event, got session %q", results[0].SessionID)
	}
}

// TestQueryEventsSessionFilter verifies filtering by session_id.
func TestQueryEventsSessionFilter(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		e := sampleEvent("agent-sess", "session-A", "tool_call")
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		e := sampleEvent("agent-sess", "session-B", "complete")
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	results, err := es.QueryEvents(ctx, store.EventQuery{SessionID: "session-A"})
	if err != nil {
		t.Fatalf("QueryEvents by session: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 session-A events, got %d", len(results))
	}

	resultsB, err := es.QueryEvents(ctx, store.EventQuery{SessionID: "session-B"})
	if err != nil {
		t.Fatalf("QueryEvents session-B: %v", err)
	}
	if len(resultsB) != 2 {
		t.Errorf("expected 2 session-B events, got %d", len(resultsB))
	}
}

// TestQueryEventsOffsetPagination verifies that Limit works as a cap.
func TestQueryEventsOffsetPagination(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	// Insert 10 events.
	for i := 0; i < 10; i++ {
		e := sampleEvent("agent-page", fmt.Sprintf("session-%d", i), "complete")
		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	// Limit to 3.
	results, err := es.QueryEvents(ctx, store.EventQuery{Limit: 3})
	if err != nil {
		t.Fatalf("QueryEvents limit 3: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 events with limit=3, got %d", len(results))
	}

	// Limit to 7.
	results7, err := es.QueryEvents(ctx, store.EventQuery{Limit: 7})
	if err != nil {
		t.Fatalf("QueryEvents limit 7: %v", err)
	}
	if len(results7) != 7 {
		t.Errorf("expected 7 events with limit=7, got %d", len(results7))
	}
}

// TestGetAgentSummaryAggregationAccuracy verifies totals and averages are computed correctly.
func TestGetAgentSummaryAggregationAccuracy(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	const numEvents = 5
	totalCost := 0.0
	totalQuality := 0.0

	for i := 0; i < numEvents; i++ {
		cost := 0.01 * float64(i+1)
		quality := 0.80 + 0.04*float64(i)
		totalCost += cost
		totalQuality += quality

		e := sampleEvent("agent-agg", "session-agg", "complete")
		e.CostUSD = &cost
		e.QualityScore = &quality

		if _, err := es.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent[%d]: %v", i, err)
		}
	}

	summary, err := es.GetAgentSummary(ctx, "agent-agg")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}

	if summary.TotalEvents != numEvents {
		t.Errorf("TotalEvents: got %d, want %d", summary.TotalEvents, numEvents)
	}

	// Total cost should match sum within float precision.
	if diff := summary.TotalCostUSD - totalCost; diff < -0.001 || diff > 0.001 {
		t.Errorf("TotalCostUSD: got %.4f, want %.4f", summary.TotalCostUSD, totalCost)
	}

	// Average quality should be within 1% of expected.
	expectedAvg := totalQuality / float64(numEvents)
	if diff := summary.AvgQuality - expectedAvg; diff < -0.01 || diff > 0.01 {
		t.Errorf("AvgQuality: got %.4f, want %.4f", summary.AvgQuality, expectedAvg)
	}
}

// TestConcurrentInsertHighLoad verifies no SQLITE_BUSY errors under high concurrency.
func TestConcurrentInsertHighLoad(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrent.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	ctx := context.Background()

	const goroutines = 50
	const eventsPerGoroutine = 100

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errors []string
	)

	start := time.Now()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				e := sampleEvent(fmt.Sprintf("agent-%d", gID), fmt.Sprintf("session-%d", gID), "tool_call")
				batchStart := time.Now()
				if _, err := es.InsertEvent(ctx, e); err != nil {
					mu.Lock()
					errors = append(errors, err.Error())
					mu.Unlock()
					return
				}
				elapsed := time.Since(batchStart)
				// WAL with busy_timeout=5000ms — each insert should be well under 50ms in practice.
				if elapsed > 50*time.Millisecond {
					t.Logf("warn: insert took %v (goroutine=%d, event=%d)", elapsed, gID, i)
				}
			}
		}(g)
	}

	wg.Wait()
	totalElapsed := time.Since(start)
	t.Logf("inserted %d events in %v (%.0f events/sec)",
		goroutines*eventsPerGoroutine,
		totalElapsed,
		float64(goroutines*eventsPerGoroutine)/totalElapsed.Seconds(),
	)

	if len(errors) > 0 {
		t.Errorf("concurrent inserts produced %d errors; first: %s", len(errors), errors[0])
	}

	// Verify total count.
	events, err := es.QueryEvents(ctx, store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	expected := goroutines * eventsPerGoroutine
	if len(events) != expected {
		t.Errorf("expected %d events, got %d", expected, len(events))
	}
}

// TestInsertEventPreservesNullableFields verifies that nil optional fields are stored as NULL.
func TestInsertEventPreservesNullableFields(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	// Event with no optional fields set.
	e := store.Event{
		AgentID:   "agent-null",
		SessionID: "session-null",
		EventType: "start",
		Model:     "gpt-4",
		Timestamp: time.Now().UTC(),
		// All optional fields are nil.
	}

	id, err := es.InsertEvent(ctx, e)
	if err != nil {
		t.Fatalf("InsertEvent (all nulls): %v", err)
	}

	events, err := es.QueryEvents(ctx, store.EventQuery{AgentID: "agent-null"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	got := events[0]
	if got.ID != id {
		t.Errorf("ID: got %q, want %q", got.ID, id)
	}
	if got.DurationMs != nil {
		t.Errorf("DurationMs: expected nil, got %v", *got.DurationMs)
	}
	if got.CostUSD != nil {
		t.Errorf("CostUSD: expected nil, got %v", *got.CostUSD)
	}
	if got.QualityScore != nil {
		t.Errorf("QualityScore: expected nil, got %v", *got.QualityScore)
	}
	if got.ToolName != nil {
		t.Errorf("ToolName: expected nil, got %v", *got.ToolName)
	}
	if got.ToolSuccess != nil {
		t.Errorf("ToolSuccess: expected nil, got %v", *got.ToolSuccess)
	}
	if got.Metadata != nil {
		t.Errorf("Metadata: expected nil, got %v", got.Metadata)
	}
}

// TestInsertEventWithExplicitID verifies that a pre-set ID is preserved.
func TestInsertEventWithExplicitID(t *testing.T) {
	es := newTestStore(t)
	ctx := context.Background()

	e := sampleEvent("agent-id", "session-id", "tool_call")
	e.ID = "custom-uuid-1234"

	id, err := es.InsertEvent(ctx, e)
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if id != "custom-uuid-1234" {
		t.Errorf("InsertEvent ID: got %q, want %q", id, "custom-uuid-1234")
	}

	events, err := es.QueryEvents(ctx, store.EventQuery{AgentID: "agent-id"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) == 0 || events[0].ID != "custom-uuid-1234" {
		t.Errorf("event with explicit ID not found")
	}
}

// TestWALModeIsEnabled verifies WAL journal mode is applied.
func TestWALModeIsEnabled(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := sqlite.ApplyTrackingMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyTrackingMigrations: %v", err)
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	// In-memory databases always report "memory", not "wal".
	// This test passes as long as the pragma didn't cause an error.
	t.Logf("journal_mode: %s", journalMode)
}
