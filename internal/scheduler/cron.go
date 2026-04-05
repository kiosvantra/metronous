// Package scheduler provides a cron-based job scheduler for Metronous.
// It wraps robfig/cron/v3 and exposes a simple API for registering
// the weekly benchmark job.
//
// # Scheduling Architecture
//
// The weekly benchmark (schedule "0 0 2 * * 1", Monday 02:00 local time) is
// embedded directly in the Metronous daemon runtime via [NewSchedulerWithContext].
// This means the schedule runs as long as the daemon process is alive — no
// separate OS timer (systemd.timer, launchd calendar interval, Windows Task
// Scheduler) is required.
//
// The daemon is managed as a long-lived system service on each platform:
//   - Linux   : systemd user service (~/.config/systemd/user/metronous.service)
//   - macOS   : launchd user agent   (~~/Library/LaunchAgents/com.metronous.daemon.plist)
//   - Windows : Windows Service Control Manager (SCM) via kardianos/service
//
// On daemon shutdown the context passed to [NewSchedulerWithContext] is
// cancelled, which stops any in-progress job cleanly and prevents new jobs
// from starting.
package scheduler

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/runner"
)

const (
	// DefaultWeeklySchedule runs at 2am every Monday (using 6-field cron with seconds).
	DefaultWeeklySchedule = "0 0 2 * * 1"

	// DefaultWindowDays is the default benchmark evaluation window.
	DefaultWindowDays = 7
)

// Scheduler wraps a cron.Cron instance and holds the benchmark runner reference.
type Scheduler struct {
	cron       *cron.Cron
	runner     *runner.Runner
	logger     *zap.Logger
	windowDays int
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewScheduler creates a Scheduler with the given runner and logger.
// The cron instance uses the second-precision parser required by DefaultWeeklySchedule.
// The Scheduler manages its own internal context; use [NewSchedulerWithContext]
// when the caller needs to propagate cancellation (e.g. daemon shutdown).
func NewScheduler(r *runner.Runner, windowDays int, logger *zap.Logger) *Scheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		runner:     r,
		logger:     logger,
		windowDays: windowDays,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// NewSchedulerWithContext creates a Scheduler whose job context is derived from
// the provided parent context. When parent is cancelled (e.g. on daemon
// shutdown), any in-progress weekly benchmark job will observe the
// cancellation and exit cleanly. Calling [Stop] also cancels the derived
// context and halts the underlying cron instance.
//
// Pass the same context that the daemon uses for its lifecycle so that
// benchmark jobs are cancelled when the daemon shuts down.
func NewSchedulerWithContext(parent context.Context, r *runner.Runner, windowDays int, logger *zap.Logger) *Scheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	ctx, cancel := context.WithCancel(parent)
	return &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		runner:     r,
		logger:     logger,
		windowDays: windowDays,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// RegisterWeeklyJob adds the benchmark job on the given cron schedule expression.
// The schedule must be a 6-field (with seconds) cron expression.
// Returns the job entry ID and any parse error.
func (s *Scheduler) RegisterWeeklyJob(schedule string) (cron.EntryID, error) {
	id, err := s.cron.AddFunc(schedule, func() {
		s.logger.Info("weekly benchmark triggered by scheduler",
			zap.String("schedule", schedule),
			zap.Int("window_days", s.windowDays),
		)

		ctx := s.ctx
		if err := s.runner.RunWeekly(ctx, s.windowDays); err != nil {
			s.logger.Error("weekly benchmark run failed", zap.Error(err))
		}
	})
	if err != nil {
		return 0, fmt.Errorf("register weekly job with schedule %q: %w", schedule, err)
	}
	s.logger.Info("registered weekly benchmark job",
		zap.String("schedule", schedule),
		zap.Int("entry_id", int(id)),
	)
	return id, nil
}

// Start begins the cron scheduler in the background.
func (s *Scheduler) Start() {
	s.cron.Start()
	s.logger.Info("scheduler started")
}

// Stop halts the cron scheduler, waiting for any running job to complete.
func (s *Scheduler) Stop() context.Context {
	s.cancel()
	return s.cron.Stop()
}

// Entries returns the list of registered cron entries (for inspection in tests).
func (s *Scheduler) Entries() []cron.Entry {
	return s.cron.Entries()
}
