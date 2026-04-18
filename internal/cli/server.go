// Package cli provides the Cobra subcommand implementations for Metronous.
package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/kiosvantra/metronous/internal/archive"
	"github.com/kiosvantra/metronous/internal/daemon"
	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/timeline"
	"github.com/kiosvantra/metronous/internal/tracking"
	"github.com/kiosvantra/metronous/internal/web"
)

// defaultDataDir returns the default ~/.metronous/data directory path.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/data"
	}
	return filepath.Join(home, ".metronous", "data")
}

type serverArchiveConfig struct {
	Enabled            bool     `yaml:"enabled"`
	CaptureFullPayload bool     `yaml:"capture_full_payload"`
	BlockOnSensitive   bool     `yaml:"block_on_sensitive"`
	RedactPatterns     []string `yaml:"redact_patterns"`
	MaxFilesPerStage   int      `yaml:"max_files_per_stage"`
	MaxBytesPerStage   int64    `yaml:"max_bytes_per_stage"`
	MaxAgeDays         int      `yaml:"max_age_days"`
}

type serverNetworkConfig struct {
	ListenAddress     string `yaml:"listen_address"`
	PublicBaseURL     string `yaml:"public_base_url"`
	EnableTimelineLAN bool   `yaml:"enable_timeline_lan"`
}

type serverAppConfig struct {
	Server  serverNetworkConfig `yaml:"server"`
	Archive serverArchiveConfig `yaml:"archive"`
}

func loadArchiveConfig(dataDir string, logger *zap.Logger) archive.Config {
	cfg := archive.Config{Enabled: false}
	appCfg, err := loadServerAppConfig(dataDir, logger)
	if err != nil {
		return cfg
	}

	cfg.Enabled = appCfg.Archive.Enabled
	cfg.BaseDir = filepath.Join(filepath.Dir(dataDir), "archive")
	cfg.CaptureFullPayload = appCfg.Archive.CaptureFullPayload
	cfg.BlockOnSensitive = appCfg.Archive.BlockOnSensitive
	cfg.RedactPatterns = append([]string(nil), appCfg.Archive.RedactPatterns...)
	if appCfg.Archive.MaxFilesPerStage > 0 {
		cfg.MaxFilesPerStage = map[archive.Stage]int{
			archive.StageBronze: appCfg.Archive.MaxFilesPerStage,
			archive.StageSilver: appCfg.Archive.MaxFilesPerStage,
			archive.StageGold:   appCfg.Archive.MaxFilesPerStage,
		}
	}
	if appCfg.Archive.MaxBytesPerStage > 0 {
		cfg.MaxBytesPerStage = map[archive.Stage]int64{
			archive.StageBronze: appCfg.Archive.MaxBytesPerStage,
			archive.StageSilver: appCfg.Archive.MaxBytesPerStage,
			archive.StageGold:   appCfg.Archive.MaxBytesPerStage,
		}
	}
	if appCfg.Archive.MaxAgeDays > 0 {
		maxAge := time.Duration(appCfg.Archive.MaxAgeDays) * 24 * time.Hour
		cfg.MaxAgePerStage = map[archive.Stage]time.Duration{
			archive.StageBronze: maxAge,
			archive.StageSilver: maxAge,
			archive.StageGold:   maxAge,
		}
	}
	return cfg
}

func loadServerAppConfig(dataDir string, logger *zap.Logger) (serverAppConfig, error) {
	configPath := filepath.Join(filepath.Dir(dataDir), "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return serverAppConfig{}, err
	}
	var appCfg serverAppConfig
	if err := yaml.Unmarshal(data, &appCfg); err != nil {
		if logger != nil {
			logger.Warn("failed to parse config.yaml", zap.Error(err))
		}
		return serverAppConfig{}, err
	}
	return appCfg, nil
}

func loadListenAddress(dataDir string, logger *zap.Logger) string {
	appCfg, err := loadServerAppConfig(dataDir, logger)
	if err != nil {
		return mcp.DefaultListenAddress
	}
	return mcp.SanitizeListenAddress(appCfg.Server.ListenAddress, appCfg.Server.EnableTimelineLAN)
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
	timelineDBPath := filepath.Join(dataDir, "timeline.db")
	listenAddress := loadListenAddress(dataDir, logger)

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

	ts, err := sqlite.NewTimelineStore(timelineDBPath)
	if err != nil {
		return fmt.Errorf("open timeline store: %w", err)
	}
	defer func() {
		if err := ts.Close(); err != nil {
			logger.Error("close timeline store", zap.Error(err))
		}
	}()

	// Start event queue.
	queue := tracking.NewEventQueue(es, tracking.DefaultBufferSize, logger)
	queue.Start()

	var archiver archive.EventArchiver
	archiveCfg := loadArchiveConfig(dataDir, logger)
	if archiveCfg.Enabled {
		pipeline, err := archive.NewPipeline(archiveCfg)
		if err != nil {
			logger.Warn("archive pipeline disabled due to init error", zap.Error(err))
		} else {
			archiver = pipeline
		}
	}

	// Create MCP server.
	srv := mcp.NewStdioServer(logger)
	// Set data-dir so the port file is instance-scoped (avoids collisions when
	// multiple metronous instances run with different data dirs).
	srv.SetDataDir(dataDir)
	srv.SetHTTPListenAddress(listenAddress)

	timelineService := timeline.NewService(ts, timeline.NewBroker())
	timelineHandler := timeline.NewHandler(timelineService, logger, srv.ResolveIngestToken)
	webServer := web.NewServer()
	srv.RegisterHTTPRoutes(func(mux *http.ServeMux) {
		timelineHandler.Register(mux)
		webServer.Register(mux)
	})

	// Register the ingest handler wired to the real queue.
	mcp.RegisterIngestHandler(srv, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngestWithArchive(ctx, req, queue, archiver)
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
	serveMode := "ServeWithHealth (stdio+http)"
	if daemonMode {
		serve = srv.ServeDaemon
		serveMode = "ServeDaemon (http-only)"
	}
	logger.Info("serve mode selected",
		zap.String("mode", serveMode),
		zap.Bool("daemonMode", daemonMode),
	)

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
