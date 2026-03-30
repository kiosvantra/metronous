// Package tracking provides event ingestion and queue management for Metronous.
package tracking

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/store"
)

const (
	// DefaultBufferSize is the number of events the channel can buffer before
	// Enqueue blocks or times out.
	DefaultBufferSize = 1000

	// DefaultEnqueueTimeout is how long Enqueue will wait before returning an
	// error when the channel is full.
	DefaultEnqueueTimeout = 5 * time.Second
)

// ErrQueueClosed is returned when Enqueue is called after Close().
var ErrQueueClosed = errors.New("event queue is closed")

// ErrQueueFull is returned when the queue buffer is full and the enqueue
// timeout is exceeded.
var ErrQueueFull = errors.New("event queue is full: timeout exceeded")

// EventQueue serializes writes to an EventStore through a single writer goroutine,
// eliminating SQLite write contention under high concurrency.
//
// Usage:
//
//	q := NewEventQueue(store, DefaultBufferSize, logger)
//	q.Start()
//	defer q.Stop()
//	q.Enqueue(event)
type EventQueue struct {
	events        chan store.Event
	store         store.EventStore
	logger        *zap.Logger
	wg            sync.WaitGroup
	closed        atomic.Bool
	timeout       time.Duration
	droppedEvents atomic.Int64
}

// NewEventQueue creates a new EventQueue with the given buffer size.
// Call Start() before Enqueue, and Stop() to drain and shut down.
func NewEventQueue(es store.EventStore, bufferSize int, logger *zap.Logger) *EventQueue {
	return NewEventQueueWithTimeout(es, bufferSize, logger, DefaultEnqueueTimeout)
}

// NewEventQueueWithTimeout creates a new EventQueue with custom enqueue timeout.
// Primarily intended for testing scenarios where a short timeout is needed.
func NewEventQueueWithTimeout(es store.EventStore, bufferSize int, logger *zap.Logger, timeout time.Duration) *EventQueue {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &EventQueue{
		events:  make(chan store.Event, bufferSize),
		store:   es,
		logger:  logger,
		timeout: timeout,
	}
}

// Start launches the background writer goroutine. Must be called before Enqueue.
// Calling Start more than once has no additional effect beyond launching another
// writer goroutine (avoid this).
func (q *EventQueue) Start() {
	q.wg.Add(1)
	go q.writer()
}

// Stop signals the queue to stop accepting new events, drains all pending events
// to the store, and waits for the writer goroutine to complete.
// After Stop returns, the underlying EventStore is NOT closed (call store.Close separately).
func (q *EventQueue) Stop() {
	// Mark as closed first so Enqueue rejects new items immediately.
	q.closed.Store(true)
	// Close the channel to unblock the writer goroutine's range loop.
	close(q.events)
	// Wait for all pending events to be flushed.
	q.wg.Wait()
}

// Enqueue adds an event to the queue for asynchronous persistence.
// Returns ErrQueueClosed if the queue has been stopped.
// Returns ErrQueueFull if the buffer is at capacity and the timeout elapses.
func (q *EventQueue) Enqueue(event store.Event) error {
	if q.closed.Load() {
		return ErrQueueClosed
	}

	timer := time.NewTimer(q.timeout)
	defer timer.Stop()

	select {
	case q.events <- event:
		return nil
	case <-timer.C:
		return ErrQueueFull
	}
}

// Len returns the current number of events waiting in the buffer.
func (q *EventQueue) Len() int {
	return len(q.events)
}

// Cap returns the total buffer capacity of the queue.
func (q *EventQueue) Cap() int {
	return cap(q.events)
}

// DroppedEvents returns the number of events that were dropped after max retries.
func (q *EventQueue) DroppedEvents() int64 {
	return q.droppedEvents.Load()
}

// MaxRetries is the maximum number of retry attempts before dropping an event
const MaxRetries = 3

// writer is the single goroutine that drains the channel and writes to SQLite.
// It processes events one at a time in arrival order with exponential backoff retry.
func (q *EventQueue) writer() {
	defer q.wg.Done()

	for event := range q.events {
		var err error
		var lastErr error

		// Retry with exponential backoff (1s, 2s, 4s)
		for attempt := 0; attempt < MaxRetries; attempt++ {
			if attempt > 0 {
				backoff := time.Duration(1<<uint(attempt-1)) * time.Second
				time.Sleep(backoff)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err = q.store.InsertEvent(ctx, event)
			cancel()

			if err == nil {
				break // Success, exit retry loop
			}
			lastErr = err
		}

		if err != nil {
			q.logger.Error("failed to persist event after retries",
				zap.String("agent_id", event.AgentID),
				zap.String("event_type", event.EventType),
				zap.Int("retries", MaxRetries),
				zap.Error(lastErr),
			)
			q.droppedEvents.Add(1)
		}
	}
}
