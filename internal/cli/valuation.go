package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// NewValuationCommand creates explicit local-only curated manual valuation commands.
func NewValuationCommand() *cobra.Command {
	var dataDir string

	cmd := &cobra.Command{
		Use:   "valuation",
		Short: "Record and query local curated manual valuations",
		Long: `Manage local curated manual valuation records.

This flow is explicit and local-only. It does not change default benchmark/report behavior
and does not export data anywhere.`,
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")

	cmd.AddCommand(newValuationRecordCmd(&dataDir))
	cmd.AddCommand(newValuationListCmd(&dataDir))
	return cmd
}

func newValuationRecordCmd(dataDir *string) *cobra.Command {
	var (
		agentID       string
		sessionID     string
		criteriaMet   int
		criteriaTotal int
		killSwitch    bool
		note          string
	)

	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record a local curated manual valuation",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(agentID) == "" {
				return fmt.Errorf("--agent is required")
			}
			bs, err := sqlitestore.NewBenchmarkStore(filepath.Join(*dataDir, "benchmark.db"))
			if err != nil {
				return fmt.Errorf("open benchmark.db: %w", err)
			}
			defer func() { _ = bs.Close() }()

			rec, err := bs.SaveCuratedValuation(context.Background(), store.CuratedValuationRecord{
				AgentID:       strings.TrimSpace(agentID),
				SessionID:     strings.TrimSpace(sessionID),
				CriteriaMet:   criteriaMet,
				CriteriaTotal: criteriaTotal,
				KillSwitch:    killSwitch,
				Note:          strings.TrimSpace(note),
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved valuation id=%s score=%.4f kill_switch=%t\n", rec.ID, rec.Score, rec.KillSwitch)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "", "Agent ID (required)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID (optional)")
	cmd.Flags().IntVar(&criteriaMet, "criteria-met", 0, "Number of criteria met")
	cmd.Flags().IntVar(&criteriaTotal, "criteria-total", 0, "Total number of criteria (N)")
	cmd.Flags().BoolVar(&killSwitch, "kill-switch", false, "Force valuation score to 0 regardless of criteria")
	cmd.Flags().StringVar(&note, "note", "", "Optional local note")
	return cmd
}

func newValuationListCmd(dataDir *string) *cobra.Command {
	var (
		agentID string
		format  string
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List local curated manual valuations",
		RunE: func(cmd *cobra.Command, args []string) error {
			bs, err := sqlitestore.NewBenchmarkStore(filepath.Join(*dataDir, "benchmark.db"))
			if err != nil {
				return fmt.Errorf("open benchmark.db: %w", err)
			}
			defer func() { _ = bs.Close() }()

			rows, err := bs.ListCuratedValuations(context.Background(), strings.TrimSpace(agentID), limit)
			if err != nil {
				return err
			}
			switch strings.ToLower(format) {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			default:
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "CREATED_AT\tAGENT\tSESSION\tMET\tTOTAL\tKILL\tSCORE\tNOTE")
				for _, row := range rows {
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%t\t%.4f\t%s\n",
						row.CreatedAt.Format(time.RFC3339),
						row.AgentID,
						row.SessionID,
						row.CriteriaMet,
						row.CriteriaTotal,
						row.KillSwitch,
						row.Score,
						row.Note,
					)
				}
				return w.Flush()
			}
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "", "Filter by agent ID (optional)")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to return")
	return cmd
}
