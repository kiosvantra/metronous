//go:build !linux

package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewMCPShimCommand returns a stub command that errors on non-Linux platforms.
func NewMCPShimCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Stdio↔HTTP shim for the Metronous daemon (Linux only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("metronous mcp is not supported on this platform")
		},
	}
}
