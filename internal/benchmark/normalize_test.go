package benchmark_test

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/store"
)

func TestNormalizeModelName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already prefixed anthropic",
			input: "anthropic/claude-opus-4-6",
			want:  "anthropic/claude-opus-4-6",
		},
		{
			name:  "missing anthropic prefix for claude",
			input: "claude-opus-4-6",
			want:  "anthropic/claude-opus-4-6",
		},
		{
			name:  "missing anthropic prefix for claude sonnet",
			input: "claude-sonnet-4-5",
			want:  "anthropic/claude-sonnet-4-5",
		},
		{
			name:  "missing anthropic prefix for claude haiku",
			input: "claude-haiku-4-5",
			want:  "anthropic/claude-haiku-4-5",
		},
		{
			name:  "already prefixed openai",
			input: "openai/gpt-5.4",
			want:  "openai/gpt-5.4",
		},
		{
			name:  "missing openai prefix for gpt",
			input: "gpt-5.4",
			want:  "openai/gpt-5.4",
		},
		{
			name:  "missing openai prefix for o-series",
			input: "o3-pro",
			want:  "openai/o3-pro",
		},
		{
			name:  "already prefixed opencode",
			input: "opencode/mimo-v2-omni-free",
			want:  "opencode/mimo-v2-omni-free",
		},
		{
			name:  "unknown model passes through",
			input: "some-custom-model",
			want:  "some-custom-model",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "already prefixed google",
			input: "google/gemini-2.5-pro",
			want:  "google/gemini-2.5-pro",
		},
		{
			name:  "missing google prefix for gemini",
			input: "gemini-2.5-pro",
			want:  "google/gemini-2.5-pro",
		},
		{
			name:  "unknown with existing slash",
			input: "custom-provider/custom-model",
			want:  "custom-provider/custom-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := benchmark.NormalizeModelName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGroupEventsByModel_NormalizesNames(t *testing.T) {
	events := []store.Event{
		{Model: "anthropic/claude-opus-4-6", AgentID: "test"},
		{Model: "claude-opus-4-6", AgentID: "test"},       // should merge with above
		{Model: "anthropic/claude-opus-4-6", AgentID: "test"},
		{Model: "openai/gpt-5.4", AgentID: "test"},
		{Model: "gpt-5.4", AgentID: "test"},               // should merge with above
	}

	groups := benchmark.GroupEventsByModel(events)

	// Should have exactly 2 groups after normalization, not 4
	if len(groups) != 2 {
		t.Errorf("expected 2 groups after normalization, got %d", len(groups))
		for k, v := range groups {
			t.Logf("  group %q: %d events", k, len(v))
		}
	}

	// The anthropic group should have 3 events
	anthropicGroup, ok := groups["anthropic/claude-opus-4-6"]
	if !ok {
		t.Fatal("expected group 'anthropic/claude-opus-4-6' to exist")
	}
	if len(anthropicGroup) != 3 {
		t.Errorf("anthropic group: got %d events, want 3", len(anthropicGroup))
	}

	// The openai group should have 2 events
	openaiGroup, ok := groups["openai/gpt-5.4"]
	if !ok {
		t.Fatal("expected group 'openai/gpt-5.4' to exist")
	}
	if len(openaiGroup) != 2 {
		t.Errorf("openai group: got %d events, want 2", len(openaiGroup))
	}
}
