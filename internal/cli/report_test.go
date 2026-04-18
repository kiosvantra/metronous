package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/kiosvantra/metronous/internal/archive"
	"github.com/kiosvantra/metronous/internal/cli"
	"github.com/kiosvantra/metronous/internal/store"
	sqlitestore "github.com/kiosvantra/metronous/internal/store/sqlite"
)

// setupReportTest creates a temporary benchmark.db with pre-populated runs and returns
// the data directory path and a cleanup function.
func setupReportTest(t *testing.T, runs []store.BenchmarkRun) string {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "benchmark.db")

	bs, err := sqlitestore.NewBenchmarkStore(dbPath)
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer func() { _ = bs.Close() }()

	ctx := context.Background()
	for _, r := range runs {
		if err := bs.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}

	return tmpDir
}

// sampleBenchmarkRun builds a BenchmarkRun for CLI test fixtures.
func sampleBenchmarkRun(agentID string, verdict store.VerdictType) store.BenchmarkRun {
	return store.BenchmarkRun{
		RunAt:            time.Now().UTC().Truncate(time.Millisecond),
		WindowDays:       7,
		AgentID:          agentID,
		Model:            "claude-sonnet-4",
		Accuracy:         0.92,
		P95LatencyMs:     15000,
		ToolSuccessRate:  0.95,
		ROIScore:         0.148,
		TotalCostUSD:     2.0,
		SampleSize:       100,
		Verdict:          verdict,
		RecommendedModel: "claude-haiku",
		DecisionReason:   "All thresholds passed",
	}
}

func setupSemanticReportTest(t *testing.T, events []store.Event) string {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "tracking.db")

	es, err := sqlitestore.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer func() { _ = es.Close() }()

	ctx := context.Background()
	for _, ev := range events {
		if _, err := es.InsertEvent(ctx, ev); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	return tmpDir
}

// runReportCmd executes the report command with given args, capturing stdout.
func runReportCmd(t *testing.T, args []string) (string, error) {
	t.Helper()

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdout = w

	// Build and execute command.
	cmd := cli.NewReportCommand()
	root := &cobra.Command{Use: "test"}
	root.AddCommand(cmd)
	root.SetArgs(append([]string{"report"}, args...))
	execErr := root.Execute()

	// Restore stdout and capture output.
	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	return buf.String(), execErr
}

// TestReportCommandOutputsLatestRun verifies the report command displays runs in table format.
func TestReportCommandOutputsLatestRun(t *testing.T) {
	runs := []store.BenchmarkRun{
		sampleBenchmarkRun("code-agent", store.VerdictKeep),
		sampleBenchmarkRun("ops-agent", store.VerdictSwitch),
	}
	tmpDir := setupReportTest(t, runs)

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}

	if !strings.Contains(output, "code-agent") {
		t.Errorf("output should contain code-agent, got:\n%s", output)
	}
	if !strings.Contains(output, "ops-agent") {
		t.Errorf("output should contain ops-agent, got:\n%s", output)
	}
	if !strings.Contains(output, "KEEP") {
		t.Errorf("output should contain KEEP verdict, got:\n%s", output)
	}
	if !strings.Contains(output, "SWITCH") {
		t.Errorf("output should contain SWITCH verdict, got:\n%s", output)
	}
}

// TestReportCommandFiltersByAgent verifies --agent flag filters results.
func TestReportCommandFiltersByAgent(t *testing.T) {
	runs := []store.BenchmarkRun{
		sampleBenchmarkRun("alpha-agent", store.VerdictKeep),
		sampleBenchmarkRun("beta-agent", store.VerdictSwitch),
	}
	tmpDir := setupReportTest(t, runs)

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir, "--agent", "alpha-agent"})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}

	if !strings.Contains(output, "alpha-agent") {
		t.Errorf("output should contain alpha-agent, got:\n%s", output)
	}
	if strings.Contains(output, "beta-agent") {
		t.Errorf("output should NOT contain beta-agent when filtered, got:\n%s", output)
	}
}

// TestReportCommandJSONFormat verifies --format=json outputs valid JSON.
func TestReportCommandJSONFormat(t *testing.T) {
	runs := []store.BenchmarkRun{
		sampleBenchmarkRun("json-agent", store.VerdictUrgentSwitch),
	}
	tmpDir := setupReportTest(t, runs)

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir, "--format", "json"})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}

	// Verify it's valid JSON.
	var result []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput:\n%s", err, output)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 JSON entry, got %d", len(result))
	}

	entry := result[0]
	if entry["agent_id"] != "json-agent" {
		t.Errorf("agent_id: got %v, want json-agent", entry["agent_id"])
	}
	if entry["verdict"] != "URGENT_SWITCH" {
		t.Errorf("verdict: got %v, want URGENT_SWITCH", entry["verdict"])
	}
}

// TestReportCommandNoRunsMessage verifies that empty DB shows a helpful message.
func TestReportCommandNoRunsMessage(t *testing.T) {
	tmpDir := setupReportTest(t, nil) // no runs

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}

	if !strings.Contains(output, "No benchmark runs") {
		t.Errorf("expected 'No benchmark runs' message, got:\n%s", output)
	}
}

// TestReportCommandAgentNoRunsMessage verifies message when agent filter finds nothing.
func TestReportCommandAgentNoRunsMessage(t *testing.T) {
	tmpDir := setupReportTest(t, nil)

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir, "--agent", "nonexistent"})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}

	if !strings.Contains(output, "No benchmark runs found for agent") {
		t.Errorf("expected agent-specific no-runs message, got:\n%s", output)
	}
}

func TestReportCommandBackwardCompatibilityUnaffectedByValuationData(t *testing.T) {
	runs := []store.BenchmarkRun{
		sampleBenchmarkRun("stable-agent", store.VerdictKeep),
	}
	tmpDir := setupReportTest(t, runs)

	bs, err := sqlitestore.NewBenchmarkStore(filepath.Join(tmpDir, "benchmark.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer func() { _ = bs.Close() }()

	if _, err := bs.SaveCuratedValuation(context.Background(), store.CuratedValuationRecord{
		AgentID:       "stable-agent",
		CriteriaMet:   0,
		CriteriaTotal: 1,
		KillSwitch:    true,
	}); err != nil {
		t.Fatalf("SaveCuratedValuation: %v", err)
	}

	output, err := runReportCmd(t, []string{"--data-dir", tmpDir})
	if err != nil {
		t.Fatalf("report command: %v", err)
	}
	if !strings.Contains(output, "stable-agent") {
		t.Fatalf("expected benchmark output unchanged, got:\n%s", output)
	}
	if strings.Contains(strings.ToLower(output), "valuation") {
		t.Fatalf("report output unexpectedly changed by valuation records:\n%s", output)
	}
}

func TestReportSemanticCommandSummarizesTaggedAndUntagged(t *testing.T) {
	durationA := 100
	durationB := 200
	costA := 0.10
	costB := 0.20
	qualityA := 0.90
	qualityB := 0.80

	events := []store.Event{
		{
			AgentID:      "sdd-agent",
			SessionID:    "sess-1",
			EventType:    "complete",
			Model:        "claude-sonnet-4-5",
			Timestamp:    time.Now().UTC(),
			DurationMs:   &durationA,
			CostUSD:      &costA,
			QualityScore: &qualityA,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "design",
			},
		},
		{
			AgentID:      "sdd-agent",
			SessionID:    "sess-2",
			EventType:    "complete",
			Model:        "claude-sonnet-4-5",
			Timestamp:    time.Now().UTC().Add(time.Second),
			DurationMs:   &durationB,
			CostUSD:      &costB,
			QualityScore: &qualityB,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "implement",
			},
		},
		{
			AgentID:    "sdd-agent",
			SessionID:  "sess-3",
			EventType:  "complete",
			Model:      "claude-sonnet-4-5",
			Timestamp:  time.Now().UTC().Add(2 * time.Second),
			DurationMs: &durationA,
			CostUSD:    &costA,
			Metadata:   nil,
		},
	}
	tmpDir := setupSemanticReportTest(t, events)

	output, err := runReportCmd(t, []string{"semantic", "--data-dir", tmpDir})
	if err != nil {
		t.Fatalf("report semantic command: %v", err)
	}

	if !strings.Contains(output, "design") {
		t.Errorf("output should contain design phase, got:\n%s", output)
	}
	if !strings.Contains(output, "implement") {
		t.Errorf("output should contain implement phase, got:\n%s", output)
	}
	if !strings.Contains(output, "untagged") {
		t.Errorf("output should contain untagged phase, got:\n%s", output)
	}
}

func TestReportSemanticCommandJSONIncludesMissingTagsAsUntagged(t *testing.T) {
	duration := 120
	cost := 0.15

	events := []store.Event{
		{
			AgentID:    "phase-agent",
			SessionID:  "sess-1",
			EventType:  "tool_call",
			Model:      "claude-sonnet-4-5",
			Timestamp:  time.Now().UTC(),
			DurationMs: &duration,
			CostUSD:    &cost,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "verify",
			},
		},
		{
			AgentID:    "phase-agent",
			SessionID:  "sess-2",
			EventType:  "tool_call",
			Model:      "claude-sonnet-4-5",
			Timestamp:  time.Now().UTC().Add(time.Second),
			DurationMs: &duration,
			CostUSD:    &cost,
		},
	}
	tmpDir := setupSemanticReportTest(t, events)

	output, err := runReportCmd(t, []string{"semantic", "--data-dir", tmpDir, "--agent", "phase-agent", "--format", "json"})
	if err != nil {
		t.Fatalf("report semantic command: %v", err)
	}

	var result []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput:\n%s", err, output)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 summary rows, got %d", len(result))
	}

	phases := map[string]bool{}
	for _, row := range result {
		phase, _ := row["phase"].(string)
		phases[phase] = true
	}
	if !phases["verify"] {
		t.Fatalf("expected verify phase in JSON output, got %+v", result)
	}
	if !phases["untagged"] {
		t.Fatalf("expected untagged phase in JSON output, got %+v", result)
	}
}

func TestReportExportRequiresExplicitOptIn(t *testing.T) {
	tmpDir := setupReportTest(t, []store.BenchmarkRun{sampleBenchmarkRun("agent-1", store.VerdictKeep)})

	outPath := filepath.Join(tmpDir, "shared", "export.json")
	_, err := runReportCmd(t, []string{"export", "--data-dir", tmpDir, "--out", outPath})
	if err == nil {
		t.Fatalf("expected command to fail without explicit opt-in")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "opt-in") {
		t.Fatalf("expected explicit opt-in error, got: %v", err)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Fatalf("export file should not exist when opt-in is missing")
	}
}

func TestReportExportWritesSanitizedContractWhenOptedIn(t *testing.T) {
	tmpDir := setupReportTest(t, []store.BenchmarkRun{sampleBenchmarkRun("agent-sensitive", store.VerdictSwitch)})

	dur := 80
	cost := 0.45
	quality := 0.9
	trackingDir := setupSemanticReportTest(t, []store.Event{
		{
			AgentID:      "agent-sensitive",
			SessionID:    "session-sensitive-123",
			EventType:    "complete",
			Model:        "claude-sonnet-4-5",
			Timestamp:    time.Now().UTC(),
			DurationMs:   &dur,
			CostUSD:      &cost,
			QualityScore: &quality,
			Metadata: map[string]interface{}{
				store.SemanticPhaseMetaKey: "verify",
				"api_key":                  "***",
			},
		},
	})
	if err := os.Rename(filepath.Join(trackingDir, "tracking.db"), filepath.Join(tmpDir, "tracking.db")); err != nil {
		t.Fatalf("move tracking.db fixture: %v", err)
	}

	outPath := filepath.Join(tmpDir, "exports", "contract.json")
	output, err := runReportCmd(t, []string{"export", "--data-dir", tmpDir, "--out", outPath, "--allow-export"})
	if err != nil {
		t.Fatalf("report export command: %v\nstdout: %s", err, output)
	}

	raw, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("read export file: %v", readErr)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("export file is not valid JSON: %v\n%s", err, string(raw))
	}
	if parsed["schema_version"] == "" {
		t.Fatalf("missing schema_version in export contract")
	}
	runs, ok := parsed["benchmark_runs"].([]interface{})
	if !ok || len(runs) != 1 {
		t.Fatalf("expected one benchmark run in export, got %+v", parsed["benchmark_runs"])
	}
	runEntry, _ := runs[0].(map[string]interface{})
	if runEntry["agent_id"] == "agent-sensitive" {
		t.Fatalf("expected agent_id to be sanitized in export contract")
	}
	if _, exists := runEntry["decision_reason"]; exists {
		t.Fatalf("decision_reason must not be included in export contract")
	}
}

func TestReportExportDryRunPreviewsAndDoesNotWriteFile(t *testing.T) {
	tmpDir := setupReportTest(t, []store.BenchmarkRun{sampleBenchmarkRun("agent-sensitive", store.VerdictSwitch)})
	trackingDir := setupSemanticReportTest(t, nil)
	if err := os.Rename(filepath.Join(trackingDir, "tracking.db"), filepath.Join(tmpDir, "tracking.db")); err != nil {
		t.Fatalf("move tracking.db fixture: %v", err)
	}

	outPath := filepath.Join(tmpDir, "exports", "contract.json")
	output, err := runReportCmd(t, []string{"export", "--data-dir", tmpDir, "--out", outPath, "--allow-export", "--dry-run"})
	if err != nil {
		t.Fatalf("report export dry-run command: %v\nstdout: %s", err, output)
	}
	if !strings.Contains(output, "dry-run preview") {
		t.Fatalf("expected dry-run preview marker in output, got: %s", output)
	}
	if !strings.Contains(output, "\"schema_version\"") {
		t.Fatalf("expected contract json in dry-run output, got: %s", output)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run must not write export file, statErr=%v", statErr)
	}
}

func TestReportArchiveUsageCommandShowsStageMetrics(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	p, err := archive.NewPipeline(archive.Config{Enabled: true, BaseDir: filepath.Join(home, "archive"), CaptureFullPayload: true})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	ev := store.Event{AgentID: "agent-a", SessionID: "session-a", EventType: "tool_call", Model: "claude", Timestamp: time.Now().UTC()}
	if _, err := p.CaptureBronze(context.Background(), map[string]interface{}{"prompt": "hello"}, ev); err != nil {
		t.Fatalf("CaptureBronze: %v", err)
	}

	output, err := runReportCmd(t, []string{"archive-usage", "--data-dir", dataDir})
	if err != nil {
		t.Fatalf("archive-usage command: %v", err)
	}
	if !strings.Contains(output, "bronze") {
		t.Fatalf("expected bronze row in output, got:\n%s", output)
	}
	if !strings.Contains(output, "silver") || !strings.Contains(output, "gold") {
		t.Fatalf("expected silver and gold rows in output, got:\n%s", output)
	}
}
