package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/archive"
	"github.com/kiosvantra/metronous/internal/exporting"
	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// NewReportCommand creates the `metronous report` cobra command.
func NewReportCommand() *cobra.Command {
	var agentID string
	var format string
	var dataDir string

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Display the latest benchmark results",
		Long: `Display the latest benchmark runs for one or all agents.

Benchmark runs are stored in benchmark.db after each weekly run.
Use --format=json for machine-readable output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(dataDir, agentID, format)
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "",
		"Filter report to a specific agent ID (optional)")
	cmd.Flags().StringVar(&format, "format", "table",
		"Output format: table or json")
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(),
		"Directory for SQLite databases (default: ~/.metronous/data)")

	cmd.AddCommand(newReportSemanticCommand())
	cmd.AddCommand(newReportExportCommand())
	cmd.AddCommand(newReportArchiveUsageCommand())

	return cmd
}

// runReport opens the benchmark store and prints results to stdout.
func runReport(dataDir, agentID, format string) error {
	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")

	bs, err := sqlitestore.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark.db: %w", err)
	}
	defer func() { _ = bs.Close() }()

	ctx := context.Background()

	runs, err := bs.GetRuns(ctx, agentID, 0)
	if err != nil {
		return fmt.Errorf("query benchmark runs: %w", err)
	}

	if len(runs) == 0 {
		if agentID != "" {
			fmt.Fprintf(os.Stdout, "No benchmark runs found for agent %q.\n", agentID)
		} else {
			fmt.Fprintln(os.Stdout, "No benchmark runs found. Run 'metronous server' and wait for the weekly benchmark.")
		}
		return nil
	}

	switch strings.ToLower(format) {
	case "json":
		return printJSON(runs)
	default:
		return printTable(runs)
	}
}

// printTable renders benchmark runs in a human-friendly tabular format.
func printTable(runs []store.BenchmarkRun) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tVERDICT\tMODEL\tSAMPLES\tACCURACY\tP95(ms)\tTOOL_RATE\tROI\tRUN_AT")
	fmt.Fprintln(w, "─────\t───────\t─────\t───────\t────────\t───────\t─────────\t───\t──────")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%.2f\t%.0f\t%.2f\t%.2f\t%s\n",
			r.AgentID,
			r.Verdict,
			r.Model,
			r.SampleSize,
			r.Accuracy,
			r.P95LatencyMs,
			r.ToolSuccessRate,
			r.ROIScore,
			r.RunAt.Format("2006-01-02 15:04"),
		)
	}
	return w.Flush()
}

// reportJSON is the JSON output structure for a single run.
type reportJSON struct {
	AgentID          string  `json:"agent_id"`
	Verdict          string  `json:"verdict"`
	Model            string  `json:"model"`
	RecommendedModel string  `json:"recommended_model,omitempty"`
	SampleSize       int     `json:"sample_size"`
	Accuracy         float64 `json:"accuracy"`
	P95LatencyMs     float64 `json:"p95_latency_ms"`
	ToolSuccessRate  float64 `json:"tool_success_rate"`
	ROIScore         float64 `json:"roi_score"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	DecisionReason   string  `json:"decision_reason"`
	RunAt            string  `json:"run_at"`
	ArtifactPath     string  `json:"artifact_path,omitempty"`
}

// printJSON renders benchmark runs as a JSON array.
func printJSON(runs []store.BenchmarkRun) error {
	out := make([]reportJSON, 0, len(runs))
	for _, r := range runs {
		out = append(out, reportJSON{
			AgentID:          r.AgentID,
			Verdict:          string(r.Verdict),
			Model:            r.Model,
			RecommendedModel: r.RecommendedModel,
			SampleSize:       r.SampleSize,
			Accuracy:         r.Accuracy,
			P95LatencyMs:     r.P95LatencyMs,
			ToolSuccessRate:  r.ToolSuccessRate,
			ROIScore:         r.ROIScore,
			TotalCostUSD:     r.TotalCostUSD,
			DecisionReason:   r.DecisionReason,
			RunAt:            r.RunAt.Format("2006-01-02T15:04:05Z"),
			ArtifactPath:     r.ArtifactPath,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

var ErrExportOptInRequired = errors.New("export is disabled by default; pass --allow-export to explicitly opt-in")

func newReportExportCommand() *cobra.Command {
	var agentID string
	var dataDir string
	var outPath string
	var allowExport bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export sanitized benchmark + telemetry summary to a local JSON contract",
		Long: `Export a sanitized, shareable JSON contract from local metronous data.

Safety defaults:
  - Export is disabled by default.
  - No network transmission is performed.
  - You must explicitly opt-in using --allow-export.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReportExport(dataDir, agentID, outPath, allowExport, dryRun)
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "", "Filter export to a specific agent ID (optional)")
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(), "Directory for SQLite databases (default: ~/.metronous/data)")
	cmd.Flags().StringVar(&outPath, "out", "", "Output path for exported JSON contract (required)")
	cmd.Flags().BoolVar(&allowExport, "allow-export", false, "Explicit opt-in required to write export output")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview sanitized sharing contract without writing output")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

func runReportExport(dataDir, agentID, outPath string, allowExport, dryRun bool) error {
	if exporting.ExportDisabledByDefault() && !allowExport {
		_ = appendSharingAudit(dataDir, "blocked", "opt_in_required")
		return ErrExportOptInRequired
	}

	benchmarkDBPath := filepath.Join(dataDir, "benchmark.db")
	trackingDBPath := filepath.Join(dataDir, "tracking.db")

	bs, err := sqlitestore.NewBenchmarkStore(benchmarkDBPath)
	if err != nil {
		return fmt.Errorf("open benchmark.db: %w", err)
	}
	defer func() { _ = bs.Close() }()

	es, err := sqlitestore.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open tracking.db: %w", err)
	}
	defer func() { _ = es.Close() }()

	ctx := context.Background()
	runs, err := bs.GetRuns(ctx, agentID, 0)
	if err != nil {
		return fmt.Errorf("query benchmark runs: %w", err)
	}
	events, err := es.QueryEvents(ctx, store.EventQuery{AgentID: agentID})
	if err != nil {
		return fmt.Errorf("query tracking events: %w", err)
	}

	contract := exporting.BuildContract(time.Now().UTC(), runs, events, agentID)
	if err := exporting.ValidateContract(contract); err != nil {
		_ = appendSharingAudit(dataDir, "rejected", "contract_validation_failed")
		return fmt.Errorf("validate export contract: %w", err)
	}

	if dryRun {
		fmt.Fprintln(os.Stdout, "dry-run preview (no file written, no network send):")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(contract); err != nil {
			return fmt.Errorf("render dry-run preview: %w", err)
		}
		_ = appendSharingAudit(dataDir, "preview", "dry_run")
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create export file: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(contract); err != nil {
		return fmt.Errorf("write export contract: %w", err)
	}
	_ = appendSharingAudit(dataDir, "written", outPath)
	fmt.Fprintf(os.Stdout, "Export written: %s\n", outPath)
	return nil
}

func appendSharingAudit(dataDir, action, detail string) error {
	auditPath := filepath.Join(dataDir, "sharing_audit.log")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	payload := map[string]string{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"action":    action,
		"detail":    detail,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func newReportArchiveUsageCommand() *cobra.Command {
	var dataDir string
	var format string

	cmd := &cobra.Command{
		Use:   "archive-usage",
		Short: "Show local bronze/silver/gold archive usage",
		Long: `Display local archive usage metrics for bronze/silver/gold stages.

This command is fully local and does not perform any network operations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runArchiveUsageReport(dataDir, format)
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(), "Directory for SQLite databases (default: ~/.metronous/data)")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	return cmd
}

func runArchiveUsageReport(dataDir, format string) error {
	archiveDir := filepath.Join(filepath.Dir(dataDir), "archive")
	usage, err := archive.UsageForBaseDir(archiveDir)
	if err != nil {
		return fmt.Errorf("read archive usage: %w", err)
	}

	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(usage)
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "STAGE\tFILES\tBYTES\tOLDEST\tNEWEST")
		fmt.Fprintln(w, "─────\t─────\t─────\t──────\t──────")
		for _, stage := range []archive.Stage{archive.StageBronze, archive.StageSilver, archive.StageGold} {
			u := usage.Stage(stage)
			fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\n", stage, u.Files, u.Bytes, emptyDash(u.Oldest), emptyDash(u.Newest))
		}
		return w.Flush()
	}
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func newReportSemanticCommand() *cobra.Command {
	var agentID string
	var format string
	var dataDir string

	cmd := &cobra.Command{
		Use:   "semantic",
		Short: "Summarize local telemetry by semantic phase",
		Long: `Summarize local telemetry from tracking.db by semantic phase tag (sdd_phase).

This command is fully local/offline and reads only your local SQLite database.
No telemetry is sent over the network.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSemanticReport(dataDir, agentID, format)
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "", "Filter report to a specific agent ID (optional)")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	cmd.Flags().StringVar(&dataDir, "data-dir", defaultDataDir(), "Directory for SQLite databases (default: ~/.metronous/data)")
	return cmd
}

type semanticPhaseSummary struct {
	Phase         string  `json:"phase"`
	Events        int     `json:"events"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	AvgQuality    float64 `json:"avg_quality"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

func runSemanticReport(dataDir, agentID, format string) error {
	trackingDBPath := filepath.Join(dataDir, "tracking.db")
	es, err := sqlitestore.NewEventStore(trackingDBPath)
	if err != nil {
		return fmt.Errorf("open tracking.db: %w", err)
	}
	defer func() { _ = es.Close() }()

	events, err := es.QueryEvents(context.Background(), store.EventQuery{AgentID: agentID})
	if err != nil {
		return fmt.Errorf("query tracking events: %w", err)
	}

	summaries := buildSemanticPhaseSummaries(events)
	if len(summaries) == 0 {
		if agentID != "" {
			fmt.Fprintf(os.Stdout, "No tracking events found for agent %q.\n", agentID)
		} else {
			fmt.Fprintln(os.Stdout, "No tracking events found.")
		}
		return nil
	}

	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summaries)
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PHASE\tEVENTS\tAVG_DURATION_MS\tAVG_QUALITY\tTOTAL_COST_USD")
		fmt.Fprintln(w, "─────\t──────\t───────────────\t───────────\t──────────────")
		for _, row := range summaries {
			fmt.Fprintf(w, "%s\t%d\t%.2f\t%.3f\t%.4f\n", row.Phase, row.Events, row.AvgDurationMs, row.AvgQuality, row.TotalCostUSD)
		}
		return w.Flush()
	}
}

func buildSemanticPhaseSummaries(events []store.Event) []semanticPhaseSummary {
	type agg struct {
		events      int
		durationSum float64
		durationN   int
		qualitySum  float64
		qualityN    int
		costSum     float64
	}

	byPhase := make(map[string]*agg)
	for _, ev := range events {
		phase := semanticPhaseFromMetadata(ev.Metadata)
		if _, ok := byPhase[phase]; !ok {
			byPhase[phase] = &agg{}
		}
		a := byPhase[phase]
		a.events++
		if ev.DurationMs != nil {
			a.durationSum += float64(*ev.DurationMs)
			a.durationN++
		}
		if ev.QualityScore != nil {
			a.qualitySum += *ev.QualityScore
			a.qualityN++
		}
		if ev.CostUSD != nil {
			a.costSum += *ev.CostUSD
		}
	}

	phases := make([]string, 0, len(byPhase))
	for phase := range byPhase {
		phases = append(phases, phase)
	}
	sort.Slice(phases, func(i, j int) bool {
		return semanticPhaseSortKey(phases[i]) < semanticPhaseSortKey(phases[j])
	})

	out := make([]semanticPhaseSummary, 0, len(phases))
	for _, phase := range phases {
		a := byPhase[phase]
		row := semanticPhaseSummary{Phase: phase, Events: a.events, TotalCostUSD: a.costSum}
		if a.durationN > 0 {
			row.AvgDurationMs = a.durationSum / float64(a.durationN)
		}
		if a.qualityN > 0 {
			row.AvgQuality = a.qualitySum / float64(a.qualityN)
		}
		out = append(out, row)
	}
	return out
}

func semanticPhaseFromMetadata(metadata map[string]interface{}) string {
	if metadata == nil {
		return "untagged"
	}
	raw, ok := metadata[store.SemanticPhaseMetaKey]
	if !ok {
		return "untagged"
	}
	phase, ok := raw.(string)
	if !ok {
		return "untagged"
	}
	phase = strings.ToLower(strings.TrimSpace(phase))
	if phase == "" {
		return "untagged"
	}
	return phase
}

func semanticPhaseSortKey(phase string) string {
	order := map[string]int{
		"propose":   0,
		"spec":      1,
		"design":    2,
		"implement": 3,
		"verify":    4,
		"untagged":  5,
	}
	if rank, ok := order[phase]; ok {
		return fmt.Sprintf("%02d-%s", rank, phase)
	}
	return fmt.Sprintf("99-%s", phase)
}
