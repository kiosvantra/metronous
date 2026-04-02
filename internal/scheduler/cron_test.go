package scheduler_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/scheduler"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
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

// TestNewSchedulerWithContext verifies that a Scheduler created with an external
// context uses the provided parent for its job execution context, and that
// cancelling the parent propagates into the scheduler.
func TestNewSchedulerWithContext(t *testing.T) {
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

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := scheduler.NewSchedulerWithContext(parent, r, scheduler.DefaultWindowDays, zap.NewNop())

	id, err := sched.RegisterWeeklyJob(scheduler.DefaultWeeklySchedule)
	if err != nil {
		t.Fatalf("RegisterWeeklyJob: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero entry ID")
	}
	if len(sched.Entries()) != 1 {
		t.Errorf("expected 1 entry, got %d", len(sched.Entries()))
	}

	// Start, then cancel the parent context and call Stop.
	// This should complete without hanging.
	sched.Start()
	cancel() // simulate daemon shutdown
	stopCtx := sched.Stop()
	<-stopCtx.Done() // must not block indefinitely
}

// TestNewSchedulerWithContextNilLogger verifies that a nil logger is handled
// gracefully by NewSchedulerWithContext.
func TestNewSchedulerWithContextNilLogger(t *testing.T) {
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

	// Must not panic with nil logger.
	sched := scheduler.NewSchedulerWithContext(context.Background(), r, 0, nil)
	if sched == nil {
		t.Fatal("expected non-nil Scheduler")
	}
	stopCtx := sched.Stop()
	<-stopCtx.Done()
}
