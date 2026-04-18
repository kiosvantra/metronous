package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/store/sqlite"
)

// defaultMetronousHome returns the default ~/.metronous directory.
func defaultMetronousHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous"
	}
	return filepath.Join(home, ".metronous")
}

// NewInitCommand creates the `metronous init` cobra command.
func NewInitCommand() *cobra.Command {
	var metronousHome string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the Metronous home directory",
		Long: `Initialize Metronous by creating the required directory structure,
default configuration, threshold settings, and SQLite databases.

This command is idempotent — safe to run multiple times.

Created layout:
  ~/.metronous/
  ├── config.yaml        (default server configuration)
  ├── thresholds.json    (performance thresholds)
  ├── data/
  │   ├── tracking.db    (event storage)
  │   └── benchmark.db   (benchmark history, Phase 2)
  ├── agents/            (agent configs for auto-discovery)
  └── artifacts/         (decision report output)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(metronousHome)
		},
	}

	cmd.Flags().StringVar(&metronousHome, "home", defaultMetronousHome(),
		"Metronous home directory (default: ~/.metronous)")

	return cmd
}

// runInit creates the Metronous directory structure and default files.
func runInit(home string) error {
	dirs := []struct {
		path string
		perm os.FileMode
	}{
		{home, 0700},
		{filepath.Join(home, "data"), 0700},
		{filepath.Join(home, "agents"), 0700},
		{filepath.Join(home, "artifacts"), 0700},
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.perm); err != nil {
			return fmt.Errorf("create directory %q: %w", d.path, err)
		}
		// Ensure permissions are correct even if the directory already existed.
		if err := os.Chmod(d.path, d.perm); err != nil {
			return fmt.Errorf("chmod directory %q: %w", d.path, err)
		}
	}

	// Write default thresholds.json.
	thresholdsPath := filepath.Join(home, "thresholds.json")
	if _, err := os.Stat(thresholdsPath); os.IsNotExist(err) {
		defaults := config.DefaultThresholdValues()
		b, err := json.MarshalIndent(defaults, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal thresholds: %w", err)
		}
		if err := os.WriteFile(thresholdsPath, b, 0600); err != nil {
			return fmt.Errorf("write thresholds: %w", err)
		}
		fmt.Printf("created: %s\n", thresholdsPath)
	}

	// Write default config.yaml.
	configPath := filepath.Join(home, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configContent := fmt.Sprintf(`version: "1"

server:
  mcp_transport: stdio

database:
  driver: sqlite
  tracking_path: %s
  benchmark_path: %s

scheduler:
  benchmark_schedule: "0 0 2 * * 1"
  window_days: 7

archive:
  enabled: false
  capture_full_payload: false
  block_on_sensitive: false
  redact_patterns:
    - "(?i)api[_-]?key"
    - "(?i)authorization"
    - "(?i)password"
    - "(?i)secret"
    - "(?i)token"
  max_files_per_stage: 500
  max_bytes_per_stage: 104857600
  max_age_days: 30

log:
  level: info
  format: json
`,
			filepath.Join(home, "data", "tracking.db"),
			filepath.Join(home, "data", "benchmark.db"),
		)
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Printf("created: %s\n", configPath)
	}

	// Initialize tracking.db (creates schema).
	trackingDBPath := filepath.Join(home, "data", "tracking.db")
	es, err := sqlite.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("initialize tracking.db: %w", err)
	}
	_ = es.Close()
	fmt.Printf("initialized: %s\n", trackingDBPath)

	// Initialize benchmark.db (creates schema).
	benchmarkDBPath := filepath.Join(home, "data", "benchmark.db")
	bs, err := sqlite.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("initialize benchmark.db: %w", err)
	}
	_ = bs.Close()
	fmt.Printf("initialized: %s\n", benchmarkDBPath)

	fmt.Printf("\nMetronous initialized at: %s\n", home)
	fmt.Println("Run 'metronous server' to start the MCP server.")
	return nil
}
