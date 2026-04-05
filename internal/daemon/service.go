// Package daemon provides the kardianos/service wrapper for running Metronous
// as a managed system service on Linux (systemd) and macOS (Launchd).
//
// # Weekly Benchmark Scheduling
//
// The weekly benchmark is embedded directly in the daemon runtime via
// [scheduler.NewSchedulerWithContext]. By default it runs with the schedule
// "0 0 2 * * 1" (Monday 02:00 local time) but this can be overridden via the
// application config (scheduler.benchmark_schedule in config.yaml). It starts
// automatically whenever the daemon starts and is cancelled cleanly when the
// daemon shuts down. No external OS timer is required.
//
// The daemon is managed as a persistent service by the OS service manager on
// each supported platform, which ensures the benchmark fires even when no
// OpenCode client is open:
//
//   - Linux   : systemd user service — ~/.config/systemd/user/metronous.service
//     (enabled by `metronous install`; starts on login and at boot with
//     lingering enabled)
//
//   - macOS   : launchd user agent — ~/Library/LaunchAgents/com.metronous.daemon.plist
//     (written by `metronous install`; loaded automatically by launchd at
//     login; no cron or calendar interval needed — the daemon is kept alive)
//
//   - Windows : Windows Service Control Manager via kardianos/service
//     (installed by `metronous install`; runs as a Windows service using the
//     "server" subcommand with --daemon-mode)
//
// In all three cases the OS keeps the daemon process running in the background,
// and the embedded cron fires the weekly benchmark at the scheduled time.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kardianos/service"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/scheduler"
	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/tracking"
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

// loadThresholds reads thresholds from p.cfg.ConfigPath (if set) or from
// ~/.metronous/thresholds.json (the path written by `metronous init`).
// Falls back to compiled defaults on any error so the daemon always starts.
func (p *Program) loadThresholds() config.Thresholds {
	path := p.cfg.ConfigPath
	if path == "" {
		// Derive the default thresholds path from the data directory:
		// DataDir is typically ~/.metronous/data; thresholds live one level up.
		path = filepath.Join(filepath.Dir(p.cfg.DataDir), "thresholds.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		p.logger.Info("thresholds file not found, using defaults",
			zap.String("path", path),
			zap.Error(err),
		)
		return config.DefaultThresholdValues()
	}

	var thresholds config.Thresholds
	if err := json.Unmarshal(data, &thresholds); err != nil {
		p.logger.Warn("failed to parse thresholds file, using defaults",
			zap.String("path", path),
			zap.Error(err),
		)
		return config.DefaultThresholdValues()
	}

	p.logger.Info("loaded thresholds from file", zap.String("path", path))
	return thresholds
}

// schedulerConfig mirrors the subset of fields in config.yaml that are relevant
// for the embedded weekly benchmark scheduler.
type schedulerConfig struct {
	BenchmarkSchedule string `yaml:"benchmark_schedule"`
	WindowDays        int    `yaml:"window_days"`
}

// appConfig is a minimal representation of ~/.metronous/config.yaml.
type appConfig struct {
	Scheduler schedulerConfig `yaml:"scheduler"`
}

// loadSchedulerConfig reads scheduler settings from config.yaml located next to
// the data directory (typically ~/.metronous/config.yaml). On any error or
// missing fields it falls back to the safe defaults defined in the scheduler
// package so the daemon always starts with a known-good schedule.
func (p *Program) loadSchedulerConfig() (schedule string, windowDays int) {
	// Safe defaults used when config is missing or invalid.
	schedule = scheduler.DefaultWeeklySchedule
	windowDays = scheduler.DefaultWindowDays

	configPath := filepath.Join(filepath.Dir(p.cfg.DataDir), "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		// Config file is optional; log at info level and use defaults.
		p.logger.Info("config file not found, using scheduler defaults",
			zap.String("path", configPath),
			zap.Error(err),
		)
		return
	}

	var cfg appConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		p.logger.Warn("failed to parse config file, using scheduler defaults",
			zap.String("path", configPath),
			zap.Error(err),
		)
		return
	}

	if cfg.Scheduler.BenchmarkSchedule != "" {
		schedule = cfg.Scheduler.BenchmarkSchedule
	}
	if cfg.Scheduler.WindowDays > 0 {
		windowDays = cfg.Scheduler.WindowDays
	}

	// Validate the schedule string when it differs from the built-in default.
	if schedule != scheduler.DefaultWeeklySchedule {
		parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(schedule); err != nil {
			p.logger.Warn("invalid scheduler.benchmark_schedule in config; falling back to default",
				zap.String("path", configPath),
				zap.String("schedule", schedule),
				zap.Error(err),
			)
			schedule = scheduler.DefaultWeeklySchedule
		}
	}

	return
}

// run is the main daemon loop: it starts the event store, queue, MCP server,
// and the embedded weekly benchmark scheduler.
//
// The weekly benchmark (cron "0 0 2 * * 1", Monday 02:00 local time) runs
// inside this process — no external OS timer is needed. Cancelling ctx
// (via Shutdown) stops the scheduler and any in-progress benchmark job.
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

	// --- Weekly benchmark scheduler ---
	// Load thresholds (defaults on error so the daemon always starts).
	thresholds := p.loadThresholds()
	engine := decision.NewDecisionEngine(&thresholds)

	// artifactDir is placed alongside the databases.
	artifactDir := filepath.Join(filepath.Dir(p.cfg.DataDir), "artifacts")
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		// Non-fatal: log and continue; artifact generation will fail gracefully.
		p.logger.Warn("failed to create artifact dir", zap.String("path", artifactDir), zap.Error(err))
	}

	agentModelLookup := config.LoadDefaultAgentModelLookup(func(err error) {
		p.logger.Warn("could not load opencode.json for agent model lookup, using heuristic fallback",
			zap.Error(err))
	})
	benchRunner := runner.NewRunnerWithModelLookup(es, bs, engine, artifactDir, p.logger, agentModelLookup)

	scheduleExpr, windowDays := p.loadSchedulerConfig()
	sched := scheduler.NewSchedulerWithContext(ctx, benchRunner, windowDays, p.logger)
	if _, err := sched.RegisterWeeklyJob(scheduleExpr); err != nil {
		// Non-fatal: the scheduler is a background enhancement; MCP server must still start.
		p.logger.Error("failed to register weekly benchmark job",
			zap.String("schedule", scheduleExpr),
			zap.Error(err),
		)
	} else {
		sched.Start()
		defer func() {
			stopCtx := sched.Stop()
			// Wait for any in-progress job to finish (Stop returns a context that
			// is done when the cron engine has fully stopped).
			<-stopCtx.Done()
			p.logger.Info("scheduler stopped")
		}()
	}

	queue := tracking.NewEventQueue(es, tracking.DefaultBufferSize, p.logger)
	queue.Start()
	defer queue.Stop()

	srv := mcp.NewStdioServer(p.logger)
	srv.SetDataDir(p.cfg.DataDir)
	mcp.RegisterIngestHandler(srv, func(innerCtx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngest(innerCtx, req, queue)
	})
	mcp.RegisterBenchmarkHandlers(srv, bs)

	p.logger.Info("metronous daemon starting",
		zap.String("data_dir", p.cfg.DataDir),
		zap.String("weekly_schedule", scheduleExpr),
	)

	return srv.ServeDaemon(ctx)
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
