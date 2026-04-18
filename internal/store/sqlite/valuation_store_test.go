package sqlite_test

import (
	"context"
	"testing"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestCuratedValuationSaveAndList_HappyPath(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	rec, err := bs.SaveCuratedValuation(ctx, store.CuratedValuationRecord{
		AgentID:       "agent-a",
		SessionID:     "session-1",
		CriteriaMet:   3,
		CriteriaTotal: 4,
		KillSwitch:    false,
		Note:          "manual review",
	})
	if err != nil {
		t.Fatalf("SaveCuratedValuation: %v", err)
	}
	if rec.Score != 0.75 {
		t.Fatalf("score mismatch: got %.2f want %.2f", rec.Score, 0.75)
	}

	rows, err := bs.ListCuratedValuations(ctx, "agent-a", 10)
	if err != nil {
		t.Fatalf("ListCuratedValuations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rows))
	}
	if rows[0].ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if rows[0].Note != "manual review" {
		t.Fatalf("note mismatch: got %q", rows[0].Note)
	}
}

func TestCuratedValuationSaveAndList_KillSwitchForcesZero(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	rec, err := bs.SaveCuratedValuation(ctx, store.CuratedValuationRecord{
		AgentID:       "agent-b",
		CriteriaMet:   10,
		CriteriaTotal: 10,
		KillSwitch:    true,
	})
	if err != nil {
		t.Fatalf("SaveCuratedValuation: %v", err)
	}
	if rec.Score != 0 {
		t.Fatalf("kill switch score mismatch: got %.2f want 0", rec.Score)
	}
}

func TestCuratedValuationSaveAndList_ZeroCriteriaTotalDeterministicZero(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStore(t)

	rec, err := bs.SaveCuratedValuation(ctx, store.CuratedValuationRecord{
		AgentID:       "agent-c",
		CriteriaMet:   1,
		CriteriaTotal: 0,
	})
	if err != nil {
		t.Fatalf("SaveCuratedValuation: %v", err)
	}
	if rec.Score != 0 {
		t.Fatalf("zero-total score mismatch: got %.2f want 0", rec.Score)
	}
}
