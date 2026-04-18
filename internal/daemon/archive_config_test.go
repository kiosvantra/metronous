package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestLoadArchiveConfigDefaultsToDisabled(t *testing.T) {
	dir := t.TempDir()
	prog := &Program{cfg: Config{DataDir: filepath.Join(dir, "data")}, logger: zap.NewNop()}
	cfg := prog.loadArchiveConfig()
	if cfg.Enabled {
		t.Fatalf("expected archive pipeline disabled by default")
	}
}

func TestLoadArchiveConfigReadsArchiveSection(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	data := []byte(`version: "1"
archive:
  enabled: true
  capture_full_payload: true
  redact_patterns:
    - "(?i)token"
  max_files_per_stage: 12
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	prog := &Program{cfg: Config{DataDir: filepath.Join(dir, "data")}, logger: zap.NewNop()}
	cfg := prog.loadArchiveConfig()
	if !cfg.Enabled {
		t.Fatalf("expected archive enabled")
	}
	if !cfg.CaptureFullPayload {
		t.Fatalf("expected capture_full_payload true")
	}
	if cfg.DefaultMaxFilesPerStage() != 12 {
		t.Fatalf("expected max_files_per_stage 12, got %d", cfg.DefaultMaxFilesPerStage())
	}
}
