package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// NewBenchmarkCommand creates the `metronous benchmark` cobra command.
func NewBenchmarkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run or manage benchmarks",
		Long:  `Manage benchmark runs.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run a benchmark immediately",
		Long: `Runs a benchmark immediately using the current data.

This command triggers an on-demand benchmark run that analyzes
recent agent performance and generates decisions.`,
		RunE: runBenchmarkRun,
	})

	return cmd
}

func runBenchmarkRun(cmd *cobra.Command, args []string) error {
	// Use go run to execute the benchmark directly from source
	runCmd := exec.Command("go", "run", "./cmd/run-benchmark")
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	runCmd.Dir = findProjectRoot()

	if err := runCmd.Run(); err != nil {
		return fmt.Errorf("benchmark run failed: %w", err)
	}

	return nil
}

func findProjectRoot() string {
	// Try to find go.mod in current directory or parent
	for i := 0; i < 5; i++ {
		if _, err := os.Stat("go.mod"); err == nil {
			cwd, _ := os.Getwd()
			return cwd
		}
		os.Chdir("..")
	}
	// Fallback to current directory
	cwd, _ := os.Getwd()
	return cwd
}
