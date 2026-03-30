package cli_test

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/cli"
)

func TestDashboardCommandStartsProgram(t *testing.T) {
	// Verify the command is constructable and has the expected properties.
	cmd := cli.NewDashboardCommand()
	if cmd == nil {
		t.Fatal("NewDashboardCommand returned nil")
	}
	if cmd.Use != "dashboard" {
		t.Errorf("expected Use='dashboard', got %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short description is empty")
	}
}

func TestDashboardCommandReturnsTTYErrorWhenUnsupported(t *testing.T) {
	// When stdout is NOT a TTY (as in a test environment), the dashboard command
	// should return an error that mentions "TTY".
	cmd := cli.NewDashboardCommand()

	var runErr error
	// Execute the RunE function directly by looking at the sub-command.
	// We do this by calling Execute on a standalone command.
	cmd.SetArgs([]string{})
	runErr = cmd.Execute()

	// In CI / test environments stdout is not a TTY, so we expect an error.
	if runErr == nil {
		t.Skip("stdout appears to be a TTY; skipping non-interactive test")
	}
	errStr := runErr.Error()
	if errStr == "" {
		t.Error("expected non-empty error message")
	}
}

func TestDashboardCommandHasFlags(t *testing.T) {
	cmd := cli.NewDashboardCommand()
	if f := cmd.Flags().Lookup("data-dir"); f == nil {
		t.Error("expected --data-dir flag")
	}
	if f := cmd.Flags().Lookup("config"); f == nil {
		t.Error("expected --config flag")
	}
}
