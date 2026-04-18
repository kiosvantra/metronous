package store_test

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestComputeCuratedValuationScore_HappyPath(t *testing.T) {
	score := store.ComputeCuratedValuationScore(3, 4, false)
	if score != 0.75 {
		t.Fatalf("score mismatch: got %.2f want %.2f", score, 0.75)
	}
}

func TestComputeCuratedValuationScore_KillSwitchAlwaysZero(t *testing.T) {
	score := store.ComputeCuratedValuationScore(4, 4, true)
	if score != 0 {
		t.Fatalf("kill switch score mismatch: got %.2f want 0", score)
	}
}

func TestComputeCuratedValuationScore_ZeroCriteriaTotalDeterministicZero(t *testing.T) {
	score := store.ComputeCuratedValuationScore(1, 0, false)
	if score != 0 {
		t.Fatalf("zero-total score mismatch: got %.2f want 0", score)
	}
}

func TestComputeCuratedValuationScore_EdgeInputsAreClamped(t *testing.T) {
	if got := store.ComputeCuratedValuationScore(-2, 5, false); got != 0 {
		t.Fatalf("negative met should clamp to 0, got %.2f", got)
	}
	if got := store.ComputeCuratedValuationScore(9, 5, false); got != 1 {
		t.Fatalf("met > total should clamp to 1.0, got %.2f", got)
	}
}
