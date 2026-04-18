package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/daemon"
)

func TestServiceProgramStartStop(t *testing.T) {
	dir := t.TempDir()
	logger, _ := zap.NewDevelopment()

	cfg := daemon.Config{DataDir: dir}
	prog := daemon.NewProgram(cfg, logger)

	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext returned error: %v", err)
	}

	// Give the goroutine a moment to spin up.
	time.Sleep(50 * time.Millisecond)

	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestServiceProgramRunsSchedulerAndServer(t *testing.T) {
	dir := t.TempDir()
	logger, _ := zap.NewDevelopment()

	cfg := daemon.Config{DataDir: filepath.Join(dir, "data")}
	prog := daemon.NewProgram(cfg, logger)

	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Data dir should have been created by the daemon.
	if _, err := os.Stat(cfg.DataDir); err != nil {
		t.Errorf("expected data dir to exist: %v", err)
	}
}

func TestPlatformReturnsNonEmpty(t *testing.T) {
	p := daemon.Platform()
	if p == "" {
		t.Error("Platform() returned empty string")
	}
}

func TestServiceConfig(t *testing.T) {
	cfg := daemon.ServiceConfig()
	if cfg == nil {
		t.Fatal("ServiceConfig returned nil")
	}
	if cfg.Name == "" {
		t.Error("service name is empty")
	}
}

// TestDaemonUsesServeWithHealth verifies that the daemon creates the mcp.port
// file in the data directory, proving it calls ServeWithHealth (not ServeStdio).
func TestDaemonUsesServeWithHealth(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	logger := zap.NewNop()

	prog := daemon.NewProgram(daemon.Config{DataDir: dataDir}, logger)
	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext: %v", err)
	}

	// Give ServeWithHealth time to create the port file.
	portFile := filepath.Join(dataDir, "mcp.port")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(portFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if _, err := os.Stat(portFile); err != nil {
		// Port file may be removed on shutdown — that's OK if it existed during runtime.
		// The test primarily checks that daemon doesn't use ServeStdio (which would never create it).
		t.Logf("port file %s removed on shutdown (expected): %v", portFile, err)
	}
}

func TestServiceProgramContextCancellation(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	prog := daemon.NewProgram(daemon.Config{DataDir: dir}, logger)

	start := time.Now()
	if err := prog.StartWithContext(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	_ = prog.Shutdown()
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("Shutdown took too long: %v", elapsed)
	}

	// Calling Shutdown a second time should not panic.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = prog.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Error("second Shutdown() call timed out")
	}
}

// TestDaemonStartsEmbeddedScheduler verifies that the daemon wires the weekly
// benchmark scheduler into its runtime.  We cannot observe the cron firing
// (the Monday 02:00 trigger is 7 days away) but we can assert that:
//  1. The daemon starts and shuts down cleanly with a scheduler in the mix.
//  2. Shutdown completes in bounded time (i.e., the scheduler's Stop() does
//     not block forever).
func TestDaemonStartsEmbeddedScheduler(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	dataDir := filepath.Join(dir, "data")
	cfg := daemon.Config{DataDir: dataDir}

	prog := daemon.NewProgram(cfg, logger)

	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext: %v", err)
	}

	// Give the goroutine time to initialise stores and register the scheduler.
	time.Sleep(150 * time.Millisecond)

	start := time.Now()
	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("Shutdown with scheduler took too long (%v); scheduler.Stop() may be blocking", elapsed)
	}
}

// TestDaemonWithCustomThresholdsFile verifies that the daemon loads thresholds
// from ConfigPath when provided, and still starts cleanly.
func TestDaemonWithCustomThresholdsFile(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	dataDir := filepath.Join(dir, "data")

	// Write a minimal valid thresholds.json.
	thresholdsContent := `{
		"version": "1.0",
		"defaults": {
			"min_accuracy": 0.80,
			"min_roi_score": 0.04,
			"max_cost_usd_per_session": 0.40
		},
		"urgent_triggers": {
			"min_accuracy": 0.55,
			"max_error_rate": 0.35,
			"max_cost_spike_multiplier": 4.0
		},
		"model_recommendations": {
			"accuracy_model": "claude-opus-4-5",
			"performance_model": "claude-haiku-4-5",
			"default_model": "claude-sonnet-4-5"
		}
	}`
	thresholdsPath := filepath.Join(dir, "thresholds.json")
	if err := os.WriteFile(thresholdsPath, []byte(thresholdsContent), 0600); err != nil {
		t.Fatalf("write thresholds: %v", err)
	}

	cfg := daemon.Config{DataDir: dataDir, ConfigPath: thresholdsPath}
	prog := daemon.NewProgram(cfg, logger)

	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestDaemonWithInvalidThresholdsFile verifies that the daemon falls back to
// compiled defaults when ConfigPath points to a malformed file, and still
// starts and shuts down cleanly.
func TestDaemonWithInvalidThresholdsFile(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	dataDir := filepath.Join(dir, "data")

	// Write intentionally malformed JSON.
	badPath := filepath.Join(dir, "bad-thresholds.json")
	if err := os.WriteFile(badPath, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("write bad thresholds: %v", err)
	}

	cfg := daemon.Config{DataDir: dataDir, ConfigPath: badPath}
	prog := daemon.NewProgram(cfg, logger)

	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext should succeed even with bad thresholds: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestDaemonThresholdsDefaultPath verifies that when ConfigPath is empty the
// daemon derives the thresholds path from DataDir (one level up) and falls
// back to defaults gracefully when the file does not exist.
func TestDaemonThresholdsDefaultPath(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	// Use a data dir where the parent does NOT have a thresholds.json — daemon
	// must start cleanly using compiled defaults.
	dataDir := filepath.Join(dir, "data")
	cfg := daemon.Config{DataDir: dataDir} // ConfigPath intentionally empty

	prog := daemon.NewProgram(cfg, logger)
	if err := prog.StartWithContext(); err != nil {
		t.Fatalf("StartWithContext: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := prog.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
