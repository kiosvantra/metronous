package cli

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/term"

	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/tui"
	"github.com/kiosvantra/metronous/internal/version"
)

// NewDashboardCommand creates the `metronous dashboard` cobra command.
func NewDashboardCommand() *cobra.Command {
	var dataDir string
	var configPath string

	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Open the interactive TUI dashboard",
		Long: `Open the Metronous interactive terminal dashboard.

The dashboard provides three tabs:
  [1] Tracking   — real-time event stream (auto-refreshes every 2s)
  [2] Benchmark  — historical benchmark run history
  [3] Config     — view and edit performance thresholds

Key bindings:
  1/2/3 or ←/→ : Switch tabs
  q or ctrl+c   : Quit
  ctrl+s        : Save thresholds (Config tab)
  ctrl+r        : Reload thresholds (Config tab)
  ↑/↓           : Navigate tables`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDashboard(dataDir, configPath)
		},
	}

	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")
	cmd.Flags().StringVar(&configPath, "config", defaultThresholdsPath(),
		"Path to thresholds.json")

	return cmd
}

// defaultThresholdsPath returns the default thresholds.json path.
func defaultThresholdsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/thresholds.json"
	}
	return filepath.Join(home, ".metronous", "thresholds.json")
}

// runDashboard initializes stores and launches the Bubble Tea TUI.
func runDashboard(dataDir, configPath string) error {
	// TTY detection: Bubble Tea requires an interactive terminal.
	if !isTerminal() {
		return fmt.Errorf("dashboard requires an interactive terminal (TTY); pipe/redirect detected")
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	trackingDBPath := filepath.Join(dataDir, "tracking.db")
	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")

	es, err := sqlite.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer func() {
		if closeErr := es.Close(); closeErr != nil {
			logger.Error("close event store", zap.Error(closeErr))
		}
	}()

	bs, err := sqlite.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark store: %w", err)
	}
	defer func() {
		if closeErr := bs.Close(); closeErr != nil {
			logger.Error("close benchmark store", zap.Error(closeErr))
		}
	}()

	workDir, _ := os.Getwd()
	version := version.Version
	model := tui.NewAppModel(es, bs, configPath, dataDir, workDir, version)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}
	return nil
}

// isTerminal returns true when both stdin and stdout are connected to a real TTY.
// Bubble Tea needs an interactive stdin for keyboard input and an interactive
// stdout for rendering. Uses golang.org/x/term for cross-platform detection
// (works on Linux and macOS).
func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
