// Package commands provides the Cobra CLI command definitions for Metronous.
package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/cli"
	"github.com/kiosvantra/metronous/internal/version"
)

// rootCmd is the base command for the Metronous CLI.
var rootCmd = &cobra.Command{
	Use:   "metronous",
	Short: "Metronous — Autonomous Intelligence and Calibration System for AI Agents",
	Long: `Metronous monitors, tracks, and calibrates AI agent performance.

It ingests telemetry events via MCP, aggregates weekly benchmarks,
and automatically recommends or applies model changes based on
configurable thresholds.

Usage:
  metronous [command]

Run 'metronous [command] --help' for more information about a command.`,
	Version: version.Version,
}

// Execute runs the root command. This is the entry point for the CLI.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("metronous version %s\n", version.Version))

	// Register subcommands.
	rootCmd.AddCommand(cli.NewInitCommand())
	rootCmd.AddCommand(cli.NewServerCommand())
	rootCmd.AddCommand(cli.NewReportCommand())

	// Phase 3 commands.
	rootCmd.AddCommand(cli.NewServiceCommand())
	rootCmd.AddCommand(cli.NewApplyModelChangeCommand())
	rootCmd.AddCommand(cli.NewDashboardCommand())

	// Systemd user service + MCP shim commands.
	rootCmd.AddCommand(cli.NewInstallCommand())
	rootCmd.AddCommand(cli.NewMCPShimCommand())
	rootCmd.AddCommand(cli.NewSelfUpdateCommand())
	rootCmd.AddCommand(cli.NewBenchmarkCommand())
	rootCmd.AddCommand(cli.NewWebCommand())
}
