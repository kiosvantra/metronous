package cli_test

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/cli"
	"github.com/enduluc/metronous/internal/store"
	"github.com/enduluc/metronous/internal/store/sqlite"
	"github.com/enduluc/metronous/internal/tracking"
)

// makeTestEvent creates a minimal test event for server tests.
func makeTestEvent(agentID, sessionID, eventType string, durationMs int) store.Event {
	return store.Event{
		AgentID:    agentID,
		SessionID:  sessionID,
		EventType:  eventType,
		Model:      "claude-sonnet-4-5",
		Timestamp:  time.Now().UTC(),
		DurationMs: &durationMs,
	}
}

// TestServerCommandFlagsExist verifies the server command exposes the expected flags.
func TestServerCommandFlagsExist(t *testing.T) {
	cmd := cli.NewServerCommand()

	// Verify --data-dir flag exists.
	if f := cmd.Flags().Lookup("data-dir"); f == nil {
		t.Error("--data-dir flag not registered")
	}

	// Verify --daemon-mode flag exists.
	if f := cmd.Flags().Lookup("daemon-mode"); f == nil {
		t.Error("--daemon-mode flag not registered")
	}

	// Verify --config flag exists.
	if f := cmd.Flags().Lookup("config"); f == nil {
		t.Error("--config flag not registered")
	}
}

// TestServerCommandGracefulShutdown verifies that the graceful shutdown sequence
// drains pending events and closes connections without data loss.
func TestServerCommandGracefulShutdown(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tracking.db")

	// Open event store.
	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}

	// Create and start queue.
	logger := zap.NewNop()
	queue := tracking.NewEventQueue(es, tracking.DefaultBufferSize, logger)
	queue.Start()

	// Enqueue 100 events before shutdown.
	const eventCount = 100
	for i := 0; i < eventCount; i++ {
		ev := makeTestEvent("agent-shutdown", "session-1", "complete", i+1)
		if err := queue.Enqueue(ev); err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
	}

	// Simulate graceful shutdown: stop queue (drains), then close store.
	queue.Stop()
	if err := es.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Reopen store and verify all events were persisted.
	es2, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("reopen EventStore: %v", err)
	}
	defer es2.Close()

	summary, err := es2.GetAgentSummary(context.Background(), "agent-shutdown")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}
	if summary.TotalEvents != eventCount {
		t.Errorf("expected %d events after graceful shutdown, got %d", eventCount, summary.TotalEvents)
	}
}

// TestServerCommandHandlesSignals verifies that a signal properly cancels a context.
func TestServerCommandHandlesSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Capture SIGINT on a channel (mirrors runServer's signal handling pattern).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	shutdownComplete := make(chan struct{})

	// Goroutine that mimics the server's signal handler.
	go func() {
		<-sigCh
		cancel()
		close(shutdownComplete)
	}()

	// Send SIGINT to self after a short delay.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()

	// Wait for shutdown with timeout.
	select {
	case <-ctx.Done():
		// Expected — context was cancelled by signal handler.
		t.Log("context cancelled by signal handler")
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for signal-triggered shutdown")
	}

	// Ensure the signal handler goroutine finished.
	select {
	case <-shutdownComplete:
	case <-time.After(500 * time.Millisecond):
		// If SIGINT wasn't consumed, that's OK — context was still cancelled.
	}
}

// TestServerCommandQueueDrainsBeforeExit verifies no events are lost on shutdown.
func TestServerCommandQueueDrainsBeforeExit(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "drain_test.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}

	queue := tracking.NewEventQueue(es, 500, zap.NewNop())
	queue.Start()

	const eventCount = 250
	for i := 0; i < eventCount; i++ {
		ev := makeTestEvent("drain-agent", "drain-session", "tool_call", i+1)
		if err := queue.Enqueue(ev); err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
	}

	// Graceful shutdown: Stop() drains all queued events.
	shutdownStart := time.Now()
	queue.Stop()
	shutdownDuration := time.Since(shutdownStart)

	t.Logf("shutdown drained %d events in %v", eventCount, shutdownDuration)

	// Verify all events are in the store.
	if err := es.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	es2, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer es2.Close()

	summary, err := es2.GetAgentSummary(context.Background(), "drain-agent")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}
	if summary.TotalEvents != eventCount {
		t.Errorf("expected %d drained events, got %d", eventCount, summary.TotalEvents)
	}
}

// TestServerDaemonModeFlag verifies --daemon-mode flag is accepted and sets daemonMode.
func TestServerDaemonModeFlag(t *testing.T) {
	cmd := cli.NewServerCommand()
	// Verify the flag is accepted (no error parsing).
	if err := cmd.ParseFlags([]string{"--daemon-mode"}); err != nil {
		t.Errorf("--daemon-mode flag parse error: %v", err)
	}
	daemonModeFlag := cmd.Flags().Lookup("daemon-mode")
	if daemonModeFlag == nil {
		t.Fatal("--daemon-mode flag not found")
	}
	if daemonModeFlag.Value.String() != "true" {
		t.Errorf("--daemon-mode value: got %q, want %q", daemonModeFlag.Value.String(), "true")
	}
}

// TestServerConfigFlagAccepted verifies --config flag is accepted with a custom path.
func TestServerConfigFlagAccepted(t *testing.T) {
	cmd := cli.NewServerCommand()
	customConfig := "/tmp/custom-config.yaml"
	if err := cmd.ParseFlags([]string{"--config", customConfig}); err != nil {
		t.Errorf("--config flag parse error: %v", err)
	}
	configFlag := cmd.Flags().Lookup("config")
	if configFlag == nil {
		t.Fatal("--config flag not found")
	}
	if configFlag.Value.String() != customConfig {
		t.Errorf("--config value: got %q, want %q", configFlag.Value.String(), customConfig)
	}
}
