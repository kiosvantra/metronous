package tui

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "0.0s"},
		{-5, "0.0s"},
		{1500, "1.5s"},
		{42300, "42.3s"},
		{59999, "60.0s"},
		{60000, "1m 0s"},
		{90000, "1m 30s"},
		{1455000, "24m 15s"},
		{3599999, "59m 59s"},
		{3600000, "1h 0m"},
		{5003000, "1h 23m"},
	}
	for _, c := range cases {
		got := formatDuration(c.ms)
		if got != c.want {
			t.Errorf("formatDuration(%.0f) = %q, want %q", c.ms, got, c.want)
		}
	}
}

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
