package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/web"
)

// NewWebCommand creates the `metronous web` cobra command.
func NewWebCommand() *cobra.Command {
	var dataDir string
	var port int

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start the web dashboard",
		Long:  "Start a web-based dashboard for viewing benchmark results in your browser.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWeb(dataDir, port)
		},
	}

	home, _ := os.UserHomeDir()
	defaultDataDir := filepath.Join(home, ".metronous", "data")
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir, "Path to Metronous data directory")
	cmd.Flags().IntVar(&port, "port", 9100, "Port for the web dashboard")

	return cmd
}

func runWeb(dataDir string, port int) error {
	logger, _ := zap.NewProduction()
	defer logger.Sync() //nolint:errcheck

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")
	bs, err := sqlite.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark store: %w", err)
	}
	defer func() {
		if cerr := bs.Close(); cerr != nil {
			logger.Error("close benchmark store", zap.Error(cerr))
		}
	}()

	trackingDBPath := filepath.Join(dataDir, "tracking.db")
	es, err := sqlite.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer func() {
		if cerr := es.Close(); cerr != nil {
			logger.Error("close event store", zap.Error(cerr))
		}
	}()

	workDir, _ := os.Getwd()
	return web.StartServer(bs, es, workDir, port)
}
