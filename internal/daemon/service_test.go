package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/daemon"
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
