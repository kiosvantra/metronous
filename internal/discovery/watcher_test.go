package discovery_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/discovery"
)

func TestWatcherEmitsCreateWriteRemove(t *testing.T) {
	dir := t.TempDir()

	w, err := discovery.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	testFile := filepath.Join(dir, "agent.json")

	// CREATE
	if err := os.WriteFile(testFile, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, w.Events(), discovery.EventCreate, testFile, 2*time.Second)

	// WRITE (debounced, so we wait a bit longer)
	if err := os.WriteFile(testFile, []byte(`{"name":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, w.Events(), discovery.EventWrite, testFile, 2*time.Second)

	// REMOVE
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, w.Events(), discovery.EventRemove, testFile, 2*time.Second)
}

func TestWatcherDebouncesDuplicateWrites(t *testing.T) {
	dir := t.TempDir()

	w, err := discovery.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	testFile := filepath.Join(dir, "agent.json")
	// Create first so we don't get spurious CREATE events.
	if err := os.WriteFile(testFile, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Drain the CREATE event.
	expectEvent(t, w.Events(), discovery.EventCreate, testFile, 2*time.Second)

	// Fire 5 rapid writes in quick succession.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(testFile, []byte(`{"i":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// We should receive exactly ONE write event (debounced).
	evt := waitEvent(t, w.Events(), 2*time.Second)
	if evt == nil {
		t.Fatal("expected a write event, got none")
	}
	if evt.Type != discovery.EventWrite {
		t.Errorf("expected WRITE, got %s", evt.Type)
	}

	// No second event should arrive within 500ms.
	select {
	case extra := <-w.Events():
		t.Errorf("expected no extra event after debounce, got %+v", extra)
	case <-time.After(500 * time.Millisecond):
		// good: no duplicate
	}
}

func TestWatcherClose(t *testing.T) {
	w, err := discovery.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// expectEvent waits for an event with the expected type and path.
func expectEvent(t *testing.T, ch <-chan discovery.WatchEvent, typ discovery.EventType, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case evt := <-ch:
			if evt.Type == typ && evt.Path == path {
				return
			}
			// Skip unrelated events (e.g. CHMOD on some platforms).
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Errorf("timeout waiting for %s event on %s", typ, path)
}

// waitEvent waits up to timeout for any event.
func waitEvent(t *testing.T, ch <-chan discovery.WatchEvent, timeout time.Duration) *discovery.WatchEvent {
	t.Helper()
	select {
	case evt := <-ch:
		return &evt
	case <-time.After(timeout):
		return nil
	}
}
