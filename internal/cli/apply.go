package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/discovery"
)

// NewApplyModelChangeCommand creates the `metronous apply-model-change` cobra command.
func NewApplyModelChangeCommand() *cobra.Command {
	var agentID string
	var newModel string
	var agentsDir string

	cmd := &cobra.Command{
		Use:   "apply-model-change",
		Short: "Update the model for a registered agent and reload its config",
		Long: `Update the LLM model specified in an agent's opencode.json and
reload the agent registry so the change takes effect immediately.

The change is also written to the audit log.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return fmt.Errorf("--agent is required")
			}
			if newModel == "" {
				return fmt.Errorf("--model is required")
			}

			logger, err := zap.NewProduction()
			if err != nil {
				return fmt.Errorf("init logger: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			if agentsDir == "" {
				agentsDir = discovery.DefaultAgentsDir()
			}

			reg := discovery.NewRegistry()
			if loadErr := reg.LoadFromDisk(agentsDir); loadErr != nil {
				return fmt.Errorf("load agents from disk: %w", loadErr)
			}

			if err := discovery.ApplyModelChange(reg, agentID, newModel, logger); err != nil {
				return fmt.Errorf("apply model change: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "agent %q model updated to %q\n", agentID, newModel)

			// Write an audit entry to stderr as well.
			fmt.Fprintf(os.Stderr, "[audit] agent=%s model=%s\n", agentID, newModel)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "", "Agent ID to update (required)")
	cmd.Flags().StringVar(&newModel, "model", "", "New model identifier (required)")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "",
		"Agents directory to scan (default: ~/.opencode/agents/)")

	return cmd
}
