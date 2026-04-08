package tui

import (
	"context"
	"sort"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

const benchmarkRunStatusPageSize = 50

type benchmarkRunStatus struct {
	lastWeeklyRunAt            time.Time
	lastIntraweekRunAt         time.Time
	lastWeeklyAttemptAt        time.Time
	lastIntraweekAttemptAt     time.Time
	lastWeeklyAttemptStatus    store.BenchmarkAttemptStatus
	lastIntraweekAttemptStatus store.BenchmarkAttemptStatus
}

type benchmarkAttemptStateReader interface {
	GetBenchmarkAttemptStates(context.Context) ([]store.BenchmarkAttemptState, error)
}

func loadBenchmarkRunStatus(ctx context.Context, bs store.BenchmarkStore) (benchmarkRunStatus, error) {
	if bs == nil {
		return benchmarkRunStatus{}, nil
	}

	status := benchmarkRunStatus{}
	for offset := 0; ; offset += benchmarkRunStatusPageSize {
		runs, err := bs.QueryRuns(ctx, store.BenchmarkQuery{Limit: benchmarkRunStatusPageSize, Offset: offset})
		if err != nil {
			return benchmarkRunStatus{}, err
		}
		if len(runs) == 0 {
			break
		}

		for _, run := range runs {
			switch run.RunKind {
			case store.RunKindWeekly, "":
				if status.lastWeeklyRunAt.IsZero() {
					status.lastWeeklyRunAt = run.RunAt
				}
			case store.RunKindIntraweek:
				if status.lastIntraweekRunAt.IsZero() {
					status.lastIntraweekRunAt = run.RunAt
				}
			}
		}
		if !status.lastWeeklyRunAt.IsZero() && !status.lastIntraweekRunAt.IsZero() {
			break
		}

		if len(runs) < benchmarkRunStatusPageSize {
			break
		}
	}

	if reader, ok := bs.(benchmarkAttemptStateReader); ok {
		states, err := reader.GetBenchmarkAttemptStates(ctx)
		if err != nil {
			return benchmarkRunStatus{}, err
		}
		sort.SliceStable(states, func(i, j int) bool { return states[i].RunKind < states[j].RunKind })
		for _, state := range states {
			switch state.RunKind {
			case store.RunKindWeekly, "":
				status.lastWeeklyAttemptAt = state.LastAttemptAt
				status.lastWeeklyAttemptStatus = state.LastAttemptStatus
			case store.RunKindIntraweek:
				status.lastIntraweekAttemptAt = state.LastAttemptAt
				status.lastIntraweekAttemptStatus = state.LastAttemptStatus
			}
		}
	}

	return status, nil
}

func formatBenchmarkRunStatus(ts time.Time) string {
	if ts.IsZero() {
		return "never"
	}
	return ts.Local().Format("2006-01-02 15:04")
}

func formatBenchmarkAttemptStatus(ts time.Time, status store.BenchmarkAttemptStatus) string {
	if ts.IsZero() {
		return "never"
	}
	formatted := formatBenchmarkRunStatus(ts)
	if status == store.BenchmarkAttemptRunning || status == store.BenchmarkAttemptFailed {
		return formatted + " (" + string(status) + ")"
	}
	return formatted
}
