package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReviewInstallPlanDryRun(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	skip, err := reviewInstallPlan(cmd, []string{"- update ~/.config/opencode/opencode.json"}, true, false)
	if err != nil {
		t.Fatalf("reviewInstallPlan dry-run: %v", err)
	}
	if !skip {
		t.Fatal("expected dry-run to skip writes")
	}
	if got := out.String(); !strings.Contains(got, "Dry run: no files will be written.") {
		t.Fatalf("missing dry-run header in output: %q", got)
	}
}

func TestReviewInstallPlanYesBypassesPrompt(t *testing.T) {
	cmd := &cobra.Command{}

	skip, err := reviewInstallPlan(cmd, []string{"- update ~/.config/opencode/opencode.json"}, false, true)
	if err != nil {
		t.Fatalf("reviewInstallPlan yes: %v", err)
	}
	if skip {
		t.Fatal("expected --yes to continue without skipping")
	}
}
