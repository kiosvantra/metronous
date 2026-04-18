package archive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

func TestPipelineCaptureBronze_DefaultDoesNotPersistFullPayload(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewPipeline(Config{Enabled: true, BaseDir: tmp})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	event := store.Event{
		AgentID:   "agent-a",
		SessionID: "session-a",
		EventType: "tool_call",
		Model:     "claude-sonnet-4-6",
		Timestamp: time.Now().UTC(),
	}
	args := map[string]interface{}{
		"prompt":  "hello",
		"api_key": "secret-key",
	}
	path, err := p.CaptureBronze(context.Background(), args, event)
	if err != nil {
		t.Fatalf("CaptureBronze: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var rec map[string]interface{}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := rec["full_payload"]; ok {
		t.Fatalf("full_payload should be omitted by default")
	}
}

func TestPipelineCaptureBronze_FullPayloadRedactsSensitiveValues(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewPipeline(Config{
		Enabled:            true,
		BaseDir:            tmp,
		CaptureFullPayload: true,
		RedactPatterns:     []string{"(?i)api[_-]?key", "(?i)password"},
	})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	event := store.Event{
		AgentID:   "agent-a",
		SessionID: "session-a",
		EventType: "tool_call",
		Model:     "claude-sonnet-4-6",
		Timestamp: time.Now().UTC(),
	}
	args := map[string]interface{}{
		"prompt":   "hello",
		"api_key":  "secret-key",
		"password": "top-secret",
	}
	path, err := p.CaptureBronze(context.Background(), args, event)
	if err != nil {
		t.Fatalf("CaptureBronze: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "secret-key") || strings.Contains(string(raw), "top-secret") {
		t.Fatalf("expected sensitive values to be redacted: %s", string(raw))
	}
}

func TestPipelinePromoteToSilver_BlockSensitivePatternsWhenConfigured(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewPipeline(Config{
		Enabled:            true,
		BaseDir:            tmp,
		CaptureFullPayload: true,
		BlockOnSensitive:   true,
		RedactPatterns:     []string{"(?i)api[_-]?key"},
	})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	event := store.Event{
		AgentID:   "agent-a",
		SessionID: "session-a",
		EventType: "tool_call",
		Model:     "claude-sonnet-4-6",
		Timestamp: time.Now().UTC(),
	}
	bronzePath, err := p.CaptureBronze(context.Background(), map[string]interface{}{"api_key": "secret-key"}, event)
	if err != nil {
		t.Fatalf("CaptureBronze: %v", err)
	}

	if _, err := p.Promote(context.Background(), bronzePath, StageSilver, nil); err == nil {
		t.Fatalf("expected promotion to fail when sensitive values are blocked")
	}
}

func TestPipelineRetentionPrunesByCountDeterministically(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewPipeline(Config{
		Enabled:            true,
		BaseDir:            tmp,
		CaptureFullPayload: true,
		MaxFilesPerStage:   map[Stage]int{StageBronze: 2},
	})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	event := store.Event{AgentID: "a", SessionID: "s", EventType: "start", Model: "m", Timestamp: time.Now().UTC()}
	for i := 0; i < 3; i++ {
		if _, err := p.CaptureBronze(context.Background(), map[string]interface{}{"idx": i}, event); err != nil {
			t.Fatalf("CaptureBronze(%d): %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	usage, err := p.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if usage.Stage(StageBronze).Files != 2 {
		t.Fatalf("expected 2 bronze files after retention prune, got %d", usage.Stage(StageBronze).Files)
	}
}

func TestPipelinePromoteAppliesSanitizerHook(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewPipeline(Config{Enabled: true, BaseDir: tmp, CaptureFullPayload: true})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	event := store.Event{AgentID: "a", SessionID: "s", EventType: "complete", Model: "m", Timestamp: time.Now().UTC()}
	bronzePath, err := p.CaptureBronze(context.Background(), map[string]interface{}{"note": "contains me"}, event)
	if err != nil {
		t.Fatalf("CaptureBronze: %v", err)
	}
	goldPath, err := p.Promote(context.Background(), bronzePath, StageGold, func(m map[string]interface{}) map[string]interface{} {
		m["note"] = "sanitized"
		return m
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	b, err := os.ReadFile(goldPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(b), "sanitized") {
		t.Fatalf("expected sanitizer result in promoted payload: %s", string(b))
	}
	if _, err := os.Stat(filepath.Join(tmp, string(StageGold))); err != nil {
		t.Fatalf("gold stage dir not created: %v", err)
	}
}
