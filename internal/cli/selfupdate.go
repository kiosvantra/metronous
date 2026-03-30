package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// NewSelfUpdateCommand creates the `metronous self-update` cobra command.
func NewSelfUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update Metronous to the latest version",
		Long: `Downloads and installs the latest version of Metronous from GitHub.

This command runs 'go install' to fetch the latest release and replace
the current binary.`,
		RunE: runSelfUpdate,
	}

	return cmd
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	fmt.Println("Checking for updates...")

	// Run go install to get the latest version
	updateCmd := exec.Command("go", "install", "github.com/kiosvantra/metronous/cmd/metronous@latest")
	updateCmd.Stdout = os.Stdout
	updateCmd.Stderr = os.Stderr
	updateCmd.Run()

	if updateCmd.Wait() != nil {
		return fmt.Errorf("failed to update Metronous")
	}

	fmt.Println("\nMetronous has been updated to the latest version.")

	// Show the new version
	versionCmd := exec.Command("metronous", "version")
	versionCmd.Stdout = os.Stdout
	versionCmd.Stderr = os.Stderr
	versionCmd.Run()

	return nil
}
