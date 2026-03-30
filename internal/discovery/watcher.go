// Package discovery provides agent auto-discovery and hot-reload functionality
// for Metronous. It watches the ~/.opencode/agents/ directory for changes and
// maintains an in-memory registry of known agents.
package discovery

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/fsnotify/fsnotify"
)

// EventType classifies the kind of filesystem change that was detected.
type EventType string

func (e EventType) String() string { return string(e) }

const (
	// EventCreate is emitted when a file is created.
	EventCreate EventType = "create"
	// EventWrite is emitted when a file is written.
	EventWrite EventType = "write"
	// EventRemove is emitted when a file is removed.
	EventRemove EventType = "remove"
)

// WatchEvent describes a single filesystem change.
type WatchEvent struct {
	// Type is the kind of change (create/write/remove).
	Type EventType
	// Path is the absolute path of the changed file or directory.
	Path string
	// Timestamp is when the event was detected.
	Timestamp time.Time
}

// debounceDuration is the minimum gap between two events for the same path
// before a WRITE event is forwarded. This prevents flooding on rapid saves.
const debounceDuration = 100 * time.Millisecond

// Watcher wraps fsnotify and emits debounced WatchEvents on its channel.
type Watcher struct {
	fw     *fsnotify.Watcher
	events chan WatchEvent
	done   chan struct{}
	logger *zap.Logger

	mu      sync.Mutex
	timers  map[string]*time.Timer
	lastEvt map[string]time.Time
	closed  bool
}

// NewWatcher creates a new Watcher. Call Watch() to start observing a directory.
// If logger is nil, a no-op logger is used.
func NewWatcher(logger *zap.Logger) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	w := &Watcher{
		fw:      fw,
		events:  make(chan WatchEvent, 64),
		done:    make(chan struct{}),
		logger:  logger,
		timers:  make(map[string]*time.Timer),
		lastEvt: make(map[string]time.Time),
	}
	go w.loop()
	return w, nil
}

// Watch adds path (a directory) to the watch list.
func (w *Watcher) Watch(path string) error {
	return w.fw.Add(path)
}

// Events returns the channel on which WatchEvents are delivered.
func (w *Watcher) Events() <-chan WatchEvent {
	return w.events
}

// Close stops watching and releases all resources.
// It stops all pending debounce timers before closing the underlying watcher
// to prevent goroutine leaks.
func (w *Watcher) Close() error {
	// Stop all pending debounce timers so their goroutines don't fire after close.
	w.mu.Lock()
	for path, t := range w.timers {
		t.Stop()
		delete(w.timers, path)
	}
	w.closed = true
	w.mu.Unlock()

	err := w.fw.Close()
	<-w.done
	return err
}

// loop reads raw fsnotify events and converts them to WatchEvents,
// applying debouncing for write events.
func (w *Watcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleRaw(ev)
		case _, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			// Errors are silently dropped; the caller can re-Watch on error.
		}
	}
}

// handleRaw converts a raw fsnotify.Event to a WatchEvent and emits it.
func (w *Watcher) handleRaw(ev fsnotify.Event) {
	var evType EventType

	switch {
	case ev.Op&fsnotify.Create != 0:
		evType = EventCreate
	case ev.Op&fsnotify.Write != 0:
		evType = EventWrite
	case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		evType = EventRemove
	default:
		return
	}

	if evType == EventWrite {
		w.debounce(ev.Name)
		return
	}

	w.emit(WatchEvent{
		Type:      evType,
		Path:      ev.Name,
		Timestamp: time.Now(),
	})
}

// debounce delays write events for debounceDuration and collapses rapid saves.
func (w *Watcher) debounce(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}

	if t, ok := w.timers[path]; ok {
		t.Reset(debounceDuration)
		return
	}

	w.timers[path] = time.AfterFunc(debounceDuration, func() {
		w.mu.Lock()
		delete(w.timers, path)
		closed := w.closed
		w.mu.Unlock()

		if closed {
			return
		}

		w.emit(WatchEvent{
			Type:      EventWrite,
			Path:      path,
			Timestamp: time.Now(),
		})
	})
}

// emit sends a WatchEvent on the events channel (non-blocking; logs and drops if full).
func (w *Watcher) emit(evt WatchEvent) {
	select {
	case w.events <- evt:
	default:
		w.logger.Warn("watcher event dropped (buffer full)",
			zap.Stringer("type", evt.Type),
			zap.String("path", evt.Path),
		)
	}
}
