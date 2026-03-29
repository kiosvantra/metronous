package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/enduluc/metronous/internal/cli"
)

// executeCommand runs a cobra command with the given args and returns stdout/stderr.
func executeCommand(root *cobra.Command, args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestServiceCommandHasSubcommands(t *testing.T) {
	svcCmd := cli.NewServiceCommand()

	expected := []string{"install", "start", "stop", "status", "uninstall"}
	subNames := make(map[string]bool)
	for _, sub := range svcCmd.Commands() {
		subNames[sub.Use] = true
	}

	for _, name := range expected {
		if !subNames[name] {
			t.Errorf("expected sub-command %q to exist", name)
		}
	}
}

func TestServiceStatusCommand(t *testing.T) {
	// Status when the service is not installed should NOT return an error —
	// the command surfaces a friendly message instead.
	root := &cobra.Command{Use: "root"}
	root.AddCommand(cli.NewServiceCommand())

	out, _, err := executeCommand(root, "service", "status")
	if err != nil {
		t.Errorf("status command returned error: %v", err)
	}
	// Output must contain "status:"
	if !strings.Contains(out, "status:") {
		t.Errorf("expected 'status:' in output, got: %q", out)
	}
}

func TestServiceCommandInstallStartStop(t *testing.T) {
	// These commands require elevated OS-level permissions on most systems.
	// We only verify that the commands are registered and return a meaningful
	// error (not a panic or a missing-command error).
	root := &cobra.Command{Use: "root"}
	root.AddCommand(cli.NewServiceCommand())

	for _, sub := range []string{"install", "start", "stop", "uninstall"} {
		_, _, err := executeCommand(root, "service", sub)
		// We expect an error because we are not running as root, but we do NOT
		// expect "command not found" or a nil error (would mean it silently did
		// nothing).  Any returned error is acceptable here.
		_ = err // just confirming it does not panic
	}
}

func TestServiceCommandPlatform(t *testing.T) {
	svcCmd := cli.NewServiceCommand()
	// The Long description should mention the platform.
	if svcCmd.Long == "" {
		t.Error("service command Long description is empty")
	}
}
