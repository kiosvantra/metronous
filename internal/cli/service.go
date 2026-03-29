package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/enduluc/metronous/internal/daemon"
)

// NewServiceCommand creates the `metronous service` cobra command group.
func NewServiceCommand() *cobra.Command {
	var dataDir string

	svcCmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the Metronous system service",
		Long: `Manage the Metronous daemon as a system service.

Metronous can run as a managed background service on Linux (systemd) and
macOS (Launchd). Use the sub-commands below to install, start, stop, query
the status of, or uninstall the service.

Platform: ` + service.Platform(),
	}

	svcCmd.PersistentFlags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")

	svcCmd.AddCommand(newServiceInstallCmd(&dataDir))
	svcCmd.AddCommand(newServiceStartCmd(&dataDir))
	svcCmd.AddCommand(newServiceStopCmd(&dataDir))
	svcCmd.AddCommand(newServiceStatusCmd(&dataDir))
	svcCmd.AddCommand(newServiceUninstallCmd(&dataDir))

	return svcCmd
}

// buildService creates a kardianos service object from the given data directory.
func buildService(dataDir string) (service.Service, error) {
	logger := zap.NewNop()
	prog := daemon.NewProgram(daemon.Config{DataDir: dataDir}, logger)
	cfg := daemon.ServiceConfig()

	// Embed the data-dir as a command-line argument so the daemon knows where
	// to store its databases when launched by the service manager.
	// The "server" subcommand (internal/cli/server.go) is the entry point that
	// the OS service manager (systemd / Launchd) will invoke.
	cfg.Arguments = []string{"server", "--data-dir", dataDir}

	svc, err := daemon.New(prog, cfg)
	if err != nil {
		return nil, fmt.Errorf("create service object: %w", err)
	}
	return svc, nil
}

func newServiceInstallCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the Metronous service (requires elevated permissions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(*dataDir, 0700); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}
			svc, err := buildService(*dataDir)
			if err != nil {
				return err
			}
			if err := svc.Install(); err != nil {
				return fmt.Errorf("install service: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "service installed (platform: %s)\n", daemon.Platform())
			return nil
		},
	}
}

func newServiceStartCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the Metronous service",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := buildService(*dataDir)
			if err != nil {
				return err
			}
			if err := svc.Start(); err != nil {
				return fmt.Errorf("start service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "service started")
			return nil
		},
	}
}

func newServiceStopCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the Metronous service",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := buildService(*dataDir)
			if err != nil {
				return err
			}
			if err := svc.Stop(); err != nil {
				return fmt.Errorf("stop service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "service stopped")
			return nil
		},
	}
}

func newServiceStatusCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check the Metronous service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := buildService(*dataDir)
			if err != nil {
				return err
			}
			status, err := svc.Status()
			if err != nil {
				// Status() can fail when the service is not installed; surface a
				// friendly message instead of a raw error.
				fmt.Fprintf(cmd.OutOrStdout(), "status: unknown (%v)\n", err)
				return nil
			}
			switch status {
			case service.StatusRunning:
				fmt.Fprintln(cmd.OutOrStdout(), "status: running")
			case service.StatusStopped:
				fmt.Fprintln(cmd.OutOrStdout(), "status: stopped")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "status: unknown")
			}
			return nil
		},
	}
}

func newServiceUninstallCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the Metronous service (requires elevated permissions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := buildService(*dataDir)
			if err != nil {
				return err
			}
			if err := svc.Uninstall(); err != nil {
				return fmt.Errorf("uninstall service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "service uninstalled")
			return nil
		},
	}
}

// serviceDataDir returns the default data directory path (used in tests).
func serviceDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/data"
	}
	return filepath.Join(home, ".metronous", "data")
}
