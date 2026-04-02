package tui

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestComputeSavings(t *testing.T) {
	pricing := map[string]float64{
		"m1":   10,
		"m2":   7,
		"free": 0,
	}

	t.Run("keep verdict", func(t *testing.T) {
		_, s := computeSavings("m1", "m2", store.VerdictKeep, pricing)
		if s != "-" {
			t.Fatalf("expected '-', got %q", s)
		}
	})

	t.Run("switch missing recommended model", func(t *testing.T) {
		_, s := computeSavings("m1", "", store.VerdictSwitch, pricing)
		if s != "-" {
			t.Fatalf("expected '-', got %q", s)
		}
	})

	t.Run("pricing unknown", func(t *testing.T) {
		_, s := computeSavings("unknown", "m2", store.VerdictSwitch, pricing)
		if s != "?" {
			t.Fatalf("expected '?', got %q", s)
		}
	})

	t.Run("free current model", func(t *testing.T) {
		_, s := computeSavings("free", "m2", store.VerdictSwitch, pricing)
		if s != "-" {
			t.Fatalf("expected '-', got %q", s)
		}
	})

	t.Run("negative savings (recommended more expensive)", func(t *testing.T) {
		_, s := computeSavings("m2", "m1", store.VerdictSwitch, pricing)
		if s != "-" {
			t.Fatalf("expected '-', got %q", s)
		}
	})

	t.Run("valid positive savings", func(t *testing.T) {
		val, s := computeSavings("m1", "m2", store.VerdictSwitch, pricing)
		if s == "-" || s == "?" {
			t.Fatalf("expected savings string, got %q", s)
		}
		if val <= 0 {
			t.Fatalf("expected positive savings value, got %.2f", val)
		}
		if s != "~30%" {
			t.Fatalf("expected '~30%%', got %q", s)
		}
	})
}
