// Package daemon provides the kardianos/service wrapper for running Metronous
// as a managed system service on Linux (systemd) and macOS (Launchd).
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kardianos/service"
	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/mcp"
	"github.com/enduluc/metronous/internal/store/sqlite"
	"github.com/enduluc/metronous/internal/tracking"
)

// Config holds the parameters needed to launch the Metronous daemon.
type Config struct {
	// DataDir is the directory where SQLite databases are stored.
	DataDir string
	// ConfigPath is an optional path to thresholds.json.
	ConfigPath string
}

// Program implements service.Interface and contains the daemon runtime.
type Program struct {
	cfg    Config
	logger *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewProgram creates a Program with the given config.
func NewProgram(cfg Config, logger *zap.Logger) *Program {
	return &Program{
		cfg:    cfg,
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Start is called by kardianos/service when the daemon starts.
// It must return quickly; actual work runs in a goroutine.
func (p *Program) Start(_ service.Service) error {
	return p.StartWithContext()
}

// StartWithContext launches the daemon goroutine. It is safe to call from tests.
func (p *Program) StartWithContext() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		if err := p.run(ctx); err != nil && err != context.Canceled {
			p.logger.Error("daemon run error", zap.Error(err))
		}
	}()

	return nil
}

// Stop is called by kardianos/service when the daemon must shut down.
func (p *Program) Stop(_ service.Service) error {
	return p.Shutdown()
}

// Shutdown cancels the daemon context and waits for clean exit.
// It is safe to call from tests.
func (p *Program) Shutdown() error {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

// run is the main daemon loop: it starts the event store, queue, and MCP server.
func (p *Program) run(ctx context.Context) error {
	if err := os.MkdirAll(p.cfg.DataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	trackingDBPath := filepath.Join(p.cfg.DataDir, "tracking.db")
	benchmarkDBPath := filepath.Join(p.cfg.DataDir, "benchmark.db")

	es, err := sqlite.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer func() {
		// Perform WAL checkpoint before closing to prevent unbounded WAL growth
		if err := es.Checkpoint(); err != nil {
			p.logger.Error("WAL checkpoint event store failed", zap.Error(err))
		}
		if closeErr := es.Close(); closeErr != nil {
			p.logger.Error("close event store", zap.Error(closeErr))
		}
	}()

	bs, err := sqlite.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark store: %w", err)
	}
	defer func() {
		// Perform WAL checkpoint before closing
		if err := bs.Checkpoint(); err != nil {
			p.logger.Error("WAL checkpoint benchmark store failed", zap.Error(err))
		}
		if closeErr := bs.Close(); closeErr != nil {
			p.logger.Error("close benchmark store", zap.Error(closeErr))
		}
	}()

	queue := tracking.NewEventQueue(es, tracking.DefaultBufferSize, p.logger)
	queue.Start()
	defer queue.Stop()

	srv := mcp.NewStdioServer(p.logger)
	mcp.RegisterIngestHandler(srv, func(innerCtx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngest(innerCtx, req, queue)
	})
	mcp.RegisterBenchmarkHandlers(srv, bs)

	p.logger.Info("metronous daemon starting",
		zap.String("data_dir", p.cfg.DataDir),
	)

	return srv.ServeStdio(ctx)
}

// ServiceConfig returns a kardianos/service configuration for Metronous.
func ServiceConfig() *service.Config {
	return &service.Config{
		Name:        "metronous",
		DisplayName: "Metronous Agent Intelligence Daemon",
		Description: "Monitors and calibrates AI agent performance via MCP.",
	}
}

// New constructs a kardianos service wrapping a Program.
func New(prog *Program, cfg *service.Config) (service.Service, error) {
	return service.New(prog, cfg)
}

// Platform returns a human-readable description of the current service platform.
func Platform() string {
	return service.Platform()
}

// Status returns the string form of the service status.
func Status(svc service.Service) string {
	status, err := svc.Status()
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	switch status {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
