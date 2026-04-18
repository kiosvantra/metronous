package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/cli"
)

func runValuationCmd(t *testing.T, args []string) (string, string, error) {
	t.Helper()

	root := &cobra.Command{Use: "test"}
	root.AddCommand(cli.NewValuationCommand())

	var out bytes.Buffer
	var errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"valuation"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestValuationCommandRecordAndListJSON(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := runValuationCmd(t, []string{
		"record",
		"--data-dir", tmpDir,
		"--agent", "agent-json",
		"--session", "session-1",
		"--criteria-met", "3",
		"--criteria-total", "4",
	})
	if err != nil {
		t.Fatalf("valuation record command: %v", err)
	}

	output, _, err := runValuationCmd(t, []string{
		"list",
		"--data-dir", tmpDir,
		"--agent", "agent-json",
		"--format", "json",
	})
	if err != nil {
		t.Fatalf("valuation list command: %v", err)
	}

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &got); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 valuation row, got %d", len(got))
	}
	if got[0]["agent_id"] != "agent-json" {
		t.Fatalf("agent_id mismatch: got %v", got[0]["agent_id"])
	}
	if got[0]["score"] != 0.75 {
		t.Fatalf("score mismatch: got %v want 0.75", got[0]["score"])
	}
}

func TestValuationCommandKillSwitchForcedZero(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := runValuationCmd(t, []string{
		"record",
		"--data-dir", tmpDir,
		"--agent", "agent-kill",
		"--criteria-met", "4",
		"--criteria-total", "4",
		"--kill-switch",
	})
	if err != nil {
		t.Fatalf("valuation record command: %v", err)
	}

	output, _, err := runValuationCmd(t, []string{
		"list",
		"--data-dir", tmpDir,
		"--agent", "agent-kill",
		"--format", "json",
	})
	if err != nil {
		t.Fatalf("valuation list command: %v", err)
	}

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &got); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if got[0]["score"] != 0.0 {
		t.Fatalf("kill switch score mismatch: got %v", got[0]["score"])
	}
}

func TestValuationCommandZeroCriteriaTotalDeterministicZero(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := runValuationCmd(t, []string{
		"record",
		"--data-dir", tmpDir,
		"--agent", "agent-zero",
		"--criteria-met", "1",
		"--criteria-total", "0",
	})
	if err != nil {
		t.Fatalf("valuation record command: %v", err)
	}

	output, _, err := runValuationCmd(t, []string{
		"list",
		"--data-dir", tmpDir,
		"--agent", "agent-zero",
		"--format", "json",
	})
	if err != nil {
		t.Fatalf("valuation list command: %v", err)
	}

	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &got); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if got[0]["score"] != 0.0 {
		t.Fatalf("zero-total score mismatch: got %v", got[0]["score"])
	}
}
