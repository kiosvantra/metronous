//go:build !linux && !windows

package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewInstallCommand returns a stub command that errors on non-Linux platforms.
func NewInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Metronous as a platform service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("metronous install is only supported on Linux; macOS is manual CLI only")
		},
	}
}
