// Package scheduler provides a cron-based job scheduler for Metronous.
// It wraps robfig/cron/v3 and exposes a simple API for registering
// the weekly benchmark job.
package scheduler

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/runner"
)

const (
	// DefaultWeeklySchedule runs at 2am every Sunday (using 6-field cron with seconds).
	DefaultWeeklySchedule = "0 0 2 * * 0"

	// DefaultWindowDays is the default benchmark evaluation window.
	DefaultWindowDays = 7
)

// Scheduler wraps a cron.Cron instance and holds the benchmark runner reference.
type Scheduler struct {
	cron       *cron.Cron
	runner     *runner.Runner
	logger     *zap.Logger
	windowDays int
}

// NewScheduler creates a Scheduler with the given runner and logger.
// The cron instance uses the second-precision parser required by DefaultWeeklySchedule.
func NewScheduler(r *runner.Runner, windowDays int, logger *zap.Logger) *Scheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	return &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		runner:     r,
		logger:     logger,
		windowDays: windowDays,
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

		ctx := context.Background()
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
	return s.cron.Stop()
}

// Entries returns the list of registered cron entries (for inspection in tests).
func (s *Scheduler) Entries() []cron.Entry {
	return s.cron.Entries()
}
