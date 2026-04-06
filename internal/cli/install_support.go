package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type fileBackup struct {
	path   string
	data   []byte
	exists bool
}

func backupFile(path string) (*fileBackup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &fileBackup{path: path, exists: false}, nil
		}
		return nil, err
	}
	return &fileBackup{path: path, data: data, exists: true}, nil
}

func (b *fileBackup) restore(mode os.FileMode) error {
	if b == nil {
		return nil
	}
	if !b.exists {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(b.path, b.data, mode)
}

func combineRollback(primary error, errs ...error) error {
	parts := []string{}
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return primary
	}
	return fmt.Errorf("%w (rollback errors: %s)", primary, strings.Join(parts, "; "))
}

type installOptions struct {
	dryRun bool
	yes    bool
}

// reviewInstallPlan prints a preview of planned file mutations when dry-run is
// enabled, or prompts for confirmation before applying them.
func reviewInstallPlan(cmd *cobra.Command, plan []string, dryRun, yes bool) (bool, error) {
	out := cmd.OutOrStdout()
	if dryRun {
		fmt.Fprintln(out, "Dry run: no files will be written.")
		for _, line := range plan {
			fmt.Fprintln(out, line)
		}
		return true, nil
	}

	if yes {
		return false, nil
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("confirmation required; rerun with --yes or --dry-run")
	}

	fmt.Fprintln(out, "Planned changes:")
	for _, line := range plan {
		fmt.Fprintln(out, line)
	}
	fmt.Fprint(out, "Apply these changes? [y/N] ")

	reader := bufio.NewReader(cmd.InOrStdin())
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return false, fmt.Errorf("aborted by user")
	}

	return false, nil
}
