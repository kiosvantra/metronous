// Package cli provides the Cobra subcommand implementations for Metronous.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/daemon"
	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/tracking"
)

// defaultDataDir returns the default ~/.metronous/data directory path.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/data"
	}
	return filepath.Join(home, ".metronous", "data")
}

// NewServerCommand creates the `metronous server` cobra command.
func NewServerCommand() *cobra.Command {
	var dataDir string
	var daemonMode bool
	var configPath string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the Metronous MCP server on stdio",
		Long: `Start the Metronous MCP server listening on stdio.

The server receives telemetry events from AI agent plugins via the
Model Context Protocol and persists them to SQLite.

Signals SIGINT and SIGTERM trigger graceful shutdown, draining
any pending events before exit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// configPath is reserved for future use.
			return runServer(dataDir, daemonMode)
		},
	}

	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")
	cmd.Flags().BoolVar(&daemonMode, "daemon-mode", false,
		"Run in HTTP-only daemon mode (no stdio); used by the systemd unit file")
	cmd.Flags().StringVar(&configPath, "config", "",
		"Path to config file (reserved for future use)")

	return cmd
}

// runAsService delegates the full server lifecycle to kardianos/service,
// which handles platform-specific service manager protocols (Windows SCM,
// macOS Launchd, Linux systemd). The daemon.Program implements
// service.Interface (Start/Stop) and runs the MCP server in a goroutine.
func runAsService(dataDir string) error {
	logger, _ := zap.NewProduction()
	prog := daemon.NewProgram(daemon.Config{DataDir: dataDir}, logger)
	cfg := daemon.ServiceConfig()
	svc, err := daemon.New(prog, cfg)
	if err != nil {
		return fmt.Errorf("create service wrapper: %w", err)
	}
	return svc.Run()
}

// runServer initializes the event store, queue, and MCP server, then serves.
// When daemonMode is true, it runs HTTP-only (no stdio) — used by systemd unit.
//
// When running as a managed service (not interactive), it delegates to
// runAsService which uses kardianos/service to handle the platform-specific
// service control protocol (Windows SCM, macOS Launchd).
func runServer(dataDir string, daemonMode bool) error {
	// Managed service detection: when the process was NOT started from an
	// interactive terminal (Windows SCM, macOS Launchd, Linux systemd),
	// delegate to kardianos/service so the platform's service manager
	// receives proper lifecycle signals. This check is independent of
	// the --daemon-mode flag because the service manager sets its own
	// Arguments (see cli/service.go) which may or may not include it.
	if !service.Interactive() {
		return runAsService(dataDir)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Ensure the data directory exists.
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory %q: %w", dataDir, err)
	}

	trackingDBPath := filepath.Join(dataDir, "tracking.db")
	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")

	// Open event store.
	es, err := sqlite.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer func() {
		if err := es.Close(); err != nil {
			logger.Error("close event store", zap.Error(err))
		}
	}()

	// Open benchmark store.
	bs, err := sqlite.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark store: %w", err)
	}
	defer func() {
		if err := bs.Close(); err != nil {
			logger.Error("close benchmark store", zap.Error(err))
		}
	}()

	// Start event queue.
	queue := tracking.NewEventQueue(es, tracking.DefaultBufferSize, logger)
	queue.Start()

	// Create MCP server.
	srv := mcp.NewStdioServer(logger)
	// Set data-dir so the port file is instance-scoped (avoids collisions when
	// multiple metronous instances run with different data dirs).
	srv.SetDataDir(dataDir)

	// Register the ingest handler wired to the real queue.
	mcp.RegisterIngestHandler(srv, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngest(ctx, req, queue)
	})

	// Register report and model_changes with real benchmark store handlers.
	mcp.RegisterBenchmarkHandlers(srv, bs)

	// Set up graceful shutdown on SIGINT/SIGTERM.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", zap.String("signal", sig.String()))
		cancel()
	}()

	logger.Info("metronous MCP server starting",
		zap.String("transport", "stdio+http-health"),
		zap.String("data_dir", dataDir),
	)

	// In daemon mode (--daemon-mode flag, set by systemd unit file) use HTTP-only
	// transport so the process doesn't exit on stdin EOF (/dev/null under systemd).
	serve := srv.ServeWithHealth
	if daemonMode {
		serve = srv.ServeDaemon
	}

	if err := serve(ctx); err != nil && err != context.Canceled {
		logger.Error("server error", zap.Error(err))
		return err
	}

	// Graceful shutdown: flush WAL and drain queue.
	logger.Info("flushing WAL checkpoint...")
	if err := es.Checkpoint(); err != nil {
		logger.Warn("WAL checkpoint failed", zap.Error(err))
	}
	logger.Info("draining event queue...")
	queue.Stop()
	logger.Info("shutdown complete")

	return nil
}
