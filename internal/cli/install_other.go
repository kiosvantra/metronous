//go:build !linux

package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewInstallCommand returns a stub command that errors on non-Linux platforms.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as a systemd user service (Linux only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("systemd service installation is only supported on Linux")
		},
	}
}
