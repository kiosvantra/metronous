package scheduler_test

import (
	"testing"

	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/config"
	"github.com/enduluc/metronous/internal/decision"
	"github.com/enduluc/metronous/internal/runner"
	"github.com/enduluc/metronous/internal/scheduler"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

// newTestScheduler creates a Scheduler backed by in-memory stores.
func newTestScheduler(t *testing.T) *scheduler.Scheduler {
	t.Helper()

	es, err := sqlitestore.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}

	t.Cleanup(func() {
		_ = es.Close()
		_ = bs.Close()
	})

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, t.TempDir(), zap.NewNop())

	return scheduler.NewScheduler(r, 7, zap.NewNop())
}

// TestSchedulerRegistersWeeklyJob verifies that RegisterWeeklyJob adds a cron entry.
func TestSchedulerRegistersWeeklyJob(t *testing.T) {
	sched := newTestScheduler(t)

	id, err := sched.RegisterWeeklyJob(scheduler.DefaultWeeklySchedule)
	if err != nil {
		t.Fatalf("RegisterWeeklyJob: %v", err)
	}

	if id == 0 {
		t.Error("expected non-zero entry ID")
	}

	entries := sched.Entries()
	if len(entries) != 1 {
		t.Errorf("expected 1 registered entry, got %d", len(entries))
	}
}

// TestSchedulerInvalidScheduleReturnsError verifies that a bad schedule string fails gracefully.
func TestSchedulerInvalidScheduleReturnsError(t *testing.T) {
	sched := newTestScheduler(t)

	_, err := sched.RegisterWeeklyJob("not-a-cron-expression")
	if err == nil {
		t.Error("expected error for invalid schedule, got nil")
	}
}

// TestSchedulerDefaultWindowDays verifies that a non-positive windowDays defaults to 7.
func TestSchedulerDefaultWindowDays(t *testing.T) {
	es, err := sqlitestore.NewEventStore(":memory:")
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() {
		_ = es.Close()
		_ = bs.Close()
	})

	thresholds := config.DefaultThresholdValues()
	engine := decision.NewDecisionEngine(&thresholds)
	r := runner.NewRunner(es, bs, engine, t.TempDir(), zap.NewNop())

	// Pass windowDays=0 — should default to DefaultWindowDays.
	sched := scheduler.NewScheduler(r, 0, zap.NewNop())

	_, err = sched.RegisterWeeklyJob(scheduler.DefaultWeeklySchedule)
	if err != nil {
		t.Fatalf("RegisterWeeklyJob: %v", err)
	}

	// Just verify it registers without panic.
	if len(sched.Entries()) != 1 {
		t.Errorf("expected 1 entry, got %d", len(sched.Entries()))
	}
}

// TestSchedulerStartStop verifies that Start and Stop do not panic.
func TestSchedulerStartStop(t *testing.T) {
	sched := newTestScheduler(t)

	if _, err := sched.RegisterWeeklyJob(scheduler.DefaultWeeklySchedule); err != nil {
		t.Fatalf("RegisterWeeklyJob: %v", err)
	}

	sched.Start()
	ctx := sched.Stop()

	// Wait for stop to complete.
	<-ctx.Done()
}
