package tracking_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/tracking"
)

// --- Mock EventStore ---

type mockStore struct {
	mu        sync.Mutex
	events    []store.Event
	failNext  bool
	failCount int
}

func (m *mockStore) InsertEvent(_ context.Context, e store.Event) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failCount++
		// Fail permanently for events that should fail (failCount <= 3 means all retries exhausted)
		if m.failCount <= 3 {
			return "", errors.New("mock store error")
		}
		// After 3 failures, allow success (but this shouldn't happen for first event)
		m.failNext = false
	}
	m.events = append(m.events, e)
	return e.ID, nil
}

func (m *mockStore) QueryEvents(_ context.Context, _ store.EventQuery) ([]store.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, nil
}

func (m *mockStore) GetAgentEvents(_ context.Context, agentID string, _ time.Time) ([]store.Event, error) {
	return nil, nil
}

func (m *mockStore) GetAgentSummary(_ context.Context, _ string) (store.AgentSummary, error) {
	return store.AgentSummary{}, nil
}

func (m *mockStore) QueryDailyCostByModel(_ context.Context, _, _ time.Time) ([]store.DailyCostByModelRow, error) {
	return nil, nil
}

func (m *mockStore) Close() error { return nil }

func (m *mockStore) CountEvents(_ context.Context, _ store.EventQuery) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events), nil
}

func (m *mockStore) QuerySessions(_ context.Context, _ store.SessionQuery) ([]store.SessionSummary, error) {
	return nil, nil
}

func (m *mockStore) GetSessionEvents(_ context.Context, _ string) ([]store.Event, error) {
	return nil, nil
}

func (m *mockStore) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

type blockingStore struct {
	mockStore
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingStore) InsertEvent(ctx context.Context, e store.Event) (string, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return b.mockStore.InsertEvent(ctx, e)
}

// --- Helpers ---

func makeEvent(agentID string) store.Event {
	return store.Event{
		ID:        "evt-" + agentID,
		AgentID:   agentID,
		SessionID: "session-1",
		EventType: "tool_call",
		Model:     "claude-sonnet-4-5",
		Timestamp: time.Now().UTC(),
	}
}

// --- Tests ---

// TestEventQueueEnqueueDrain verifies that all enqueued events are persisted
// after Stop() drains the queue.
func TestEventQueueEnqueueDrain(t *testing.T) {
	ms := &mockStore{}
	q := tracking.NewEventQueue(ms, 100, nil)
	q.Start()

	const count = 50
	for i := 0; i < count; i++ {
		e := makeEvent("agent-1")
		e.ID = "" // let store generate
		if err := q.Enqueue(e); err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
	}

	q.Stop()

	if ms.Count() != count {
		t.Errorf("expected %d persisted events, got %d", count, ms.Count())
	}
}

// TestEventQueueCloseRejectsNewEvents verifies that Enqueue returns ErrQueueClosed
// after Stop() has been called.
func TestEventQueueCloseRejectsNewEvents(t *testing.T) {
	ms := &mockStore{}
	q := tracking.NewEventQueue(ms, 10, nil)
	q.Start()
	q.Stop()

	err := q.Enqueue(makeEvent("agent-1"))
	if err == nil {
		t.Fatal("expected error from Enqueue after Stop, got nil")
	}
	if !errors.Is(err, tracking.ErrQueueClosed) {
		t.Errorf("expected ErrQueueClosed, got: %v", err)
	}
}

// TestEventQueueFullTimeout verifies ErrQueueFull when buffer is saturated and
// writer goroutine is not started (simulating a stalled writer).
func TestEventQueueFullTimeout(t *testing.T) {
	ms := &mockStore{}
	// Buffer of 1, no Start() called — queue will fill immediately.
	q := tracking.NewEventQueueWithTimeout(ms, 1, nil, 50*time.Millisecond)

	// Fill the buffer.
	_ = q.Enqueue(makeEvent("fill"))

	// Next enqueue should time out (no writer consuming from channel).
	err := q.Enqueue(makeEvent("overflow"))
	if err == nil {
		t.Fatal("expected ErrQueueFull, got nil")
	}
	if !errors.Is(err, tracking.ErrQueueFull) {
		t.Errorf("expected ErrQueueFull, got: %v", err)
	}

	// Clean up: drain manually without starting writer
	q.Stop()
}

// TestEventQueueConcurrentEnqueue verifies no data races under concurrent enqueue.
func TestEventQueueConcurrentEnqueue(t *testing.T) {
	ms := &mockStore{}
	q := tracking.NewEventQueue(ms, 500, nil)
	q.Start()

	const goroutines = 20
	const eventsPerGoroutine = 10
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				_ = q.Enqueue(makeEvent("concurrent-agent"))
			}
		}(g)
	}

	wg.Wait()
	q.Stop()

	expected := goroutines * eventsPerGoroutine
	if ms.Count() != expected {
		t.Errorf("expected %d events, got %d", expected, ms.Count())
	}
}

// TestEventQueueStoreErrorDoesNotBlock verifies that store errors don't crash
// or deadlock the writer goroutine.
func TestEventQueueStoreErrorDoesNotBlock(t *testing.T) {
	ms := &mockStore{failNext: true}
	q := tracking.NewEventQueue(ms, 10, nil)
	q.Start()

	// This event will fail to persist (failNext=true).
	if err := q.Enqueue(makeEvent("agent-1")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// This event should persist successfully.
	if err := q.Enqueue(makeEvent("agent-2")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	q.Stop()

	// With retry (MaxRetries=3), the second event is persisted after retries succeed.
	// First event fails permanently after 3 retries (failCount > 3), second event succeeds.
	// Expected: 1 persisted event (second one), first one dropped after max retries.
	if ms.Count() != 1 {
		t.Errorf("expected 1 persisted event (after error and retry), got %d", ms.Count())
	}
}

// TestEventQueueEnqueueAndWaitBlocksUntilPersisted verifies that the caller is
// not released until the store has durably written the event.
func TestEventQueueEnqueueAndWaitBlocksUntilPersisted(t *testing.T) {
	bs := &blockingStore{
		mockStore: mockStore{},
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	q := tracking.NewEventQueue(bs, 10, nil)
	q.Start()
	t.Cleanup(func() { q.Stop() })

	done := make(chan error, 1)
	go func() {
		done <- q.EnqueueAndWait(context.Background(), makeEvent("agent-1"))
	}()

	select {
	case <-bs.started:
	case <-time.After(2 * time.Second):
		t.Fatal("store insert did not start")
	}

	select {
	case err := <-done:
		t.Fatalf("EnqueueAndWait returned before persistence completed: %v", err)
	default:
	}

	close(bs.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EnqueueAndWait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnqueueAndWait did not return after persistence")
	}

	if bs.Count() != 1 {
		t.Fatalf("expected 1 persisted event, got %d", bs.Count())
	}
}

// TestEventQueueLenAndCap verifies Len() and Cap() report correctly.
func TestEventQueueLenAndCap(t *testing.T) {
	ms := &mockStore{}
	q := tracking.NewEventQueue(ms, 100, nil)
	// Don't Start — events stay in buffer.

	if q.Cap() != 100 {
		t.Errorf("Cap: expected 100, got %d", q.Cap())
	}

	_ = q.Enqueue(makeEvent("a"))
	_ = q.Enqueue(makeEvent("b"))
	if q.Len() != 2 {
		t.Errorf("Len after 2 enqueues: expected 2, got %d", q.Len())
	}

	q.Stop()
}
