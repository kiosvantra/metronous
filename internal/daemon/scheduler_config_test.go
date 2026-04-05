package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/scheduler"
)

func TestLoadSchedulerConfigDefaultsWhenConfigMissing(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	prog := &Program{
		cfg:    Config{DataDir: dataDir},
		logger: zap.NewNop(),
	}

	schedule, windowDays := prog.loadSchedulerConfig()
	if schedule != scheduler.DefaultWeeklySchedule {
		t.Fatalf("expected default schedule %q, got %q", scheduler.DefaultWeeklySchedule, schedule)
	}
	if windowDays != scheduler.DefaultWindowDays {
		t.Fatalf("expected default windowDays %d, got %d", scheduler.DefaultWindowDays, windowDays)
	}
}

func TestLoadSchedulerConfigReadsSchedulerSection(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")

	configContent := []byte(`version: "1"

scheduler:
  benchmark_schedule: "0 30 3 * * 2"
  window_days: 14
`)
	if err := os.WriteFile(configPath, configContent, 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	prog := &Program{
		cfg:    Config{DataDir: dataDir},
		logger: zap.NewNop(),
	}

	schedule, windowDays := prog.loadSchedulerConfig()
	if schedule != "0 30 3 * * 2" {
		t.Fatalf("expected schedule from config, got %q", schedule)
	}
	if windowDays != 14 {
		t.Fatalf("expected windowDays 14 from config, got %d", windowDays)
	}
}

func TestLoadSchedulerConfigFallsBackOnInvalidSchedule(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")

	configContent := []byte(`version: "1"

scheduler:
  benchmark_schedule: "not-a-cron-expression"
  window_days: 10
`)
	if err := os.WriteFile(configPath, configContent, 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	prog := &Program{
		cfg:    Config{DataDir: dataDir},
		logger: zap.NewNop(),
	}

	schedule, windowDays := prog.loadSchedulerConfig()
	if schedule != scheduler.DefaultWeeklySchedule {
		t.Fatalf("expected fallback to default schedule %q, got %q", scheduler.DefaultWeeklySchedule, schedule)
	}
	if windowDays != 10 {
		t.Fatalf("expected windowDays 10 from config, got %d", windowDays)
	}
}
