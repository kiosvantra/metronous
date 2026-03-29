package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/mcp"
	"github.com/enduluc/metronous/internal/store"
	sqlitestore "github.com/enduluc/metronous/internal/store/sqlite"
)

// newTestBenchmarkStoreForMCP creates an in-memory BenchmarkStore for MCP handler tests.
func newTestBenchmarkStoreForMCP(t *testing.T) *sqlitestore.BenchmarkStore {
	t.Helper()
	bs, err := sqlitestore.NewBenchmarkStore(":memory:")
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	t.Cleanup(func() { _ = bs.Close() })
	return bs
}

// insertBenchmarkRun inserts a benchmark run into the store for testing.
func insertBenchmarkRun(t *testing.T, ctx context.Context, bs store.BenchmarkStore, agentID string, verdict store.VerdictType) {
	t.Helper()
	run := store.BenchmarkRun{
		RunAt:            time.Now().UTC(),
		WindowDays:       7,
		AgentID:          agentID,
		Model:            "claude-sonnet-4",
		Accuracy:         0.92,
		P95LatencyMs:     15000,
		ToolSuccessRate:  0.95,
		ROIScore:         4.5,
		TotalCostUSD:     2.0,
		SampleSize:       100,
		Verdict:          verdict,
		RecommendedModel: "claude-haiku",
		DecisionReason:   "test reason",
		ArtifactPath:     "/tmp/artifact.json",
	}
	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

// callMCPTool sends a tools/call request and returns the response.
func callMCPTool(t *testing.T, bs store.BenchmarkStore, toolName string, args map[string]interface{}) mcp.Response {
	t.Helper()

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	rawReq := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + toolName + `","arguments":` + string(argsJSON) + `}}`

	out := &bytes.Buffer{}
	in := strings.NewReader(rawReq + "\n")
	srv := mcp.NewServer(in, out, nil)
	mcp.RegisterBenchmarkHandlers(srv, bs)

	_ = srv.ServeStdio(context.Background())

	var resp mcp.Response
	if out.Len() == 0 {
		t.Fatal("no response from server")
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, out.String())
	}
	return resp
}

// getResultText extracts the text content from a tools/call response.
func getResultText(t *testing.T, resp mcp.Response) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result, got nil")
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	contents, ok := result["content"].([]interface{})
	if !ok || len(contents) == 0 {
		t.Fatal("content field missing or empty")
	}
	firstItem, _ := contents[0].(map[string]interface{})
	text, _ := firstItem["text"].(string)
	return text
}

// --- Task 17: report tool tests ---

// TestReportToolReturnsLatestRuns verifies that report returns benchmark data.
func TestReportToolReturnsLatestRuns(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	insertBenchmarkRun(t, ctx, bs, "agent-1", store.VerdictKeep)
	insertBenchmarkRun(t, ctx, bs, "agent-2", store.VerdictSwitch)

	resp := callMCPTool(t, bs, "report", map[string]interface{}{})
	text := getResultText(t, resp)

	if !strings.Contains(text, "agent-1") {
		t.Errorf("report should contain agent-1, got: %s", text)
	}
	if !strings.Contains(text, "agent-2") {
		t.Errorf("report should contain agent-2, got: %s", text)
	}
	if !strings.Contains(text, "KEEP") {
		t.Errorf("report should contain KEEP verdict, got: %s", text)
	}
	if !strings.Contains(text, "SWITCH") {
		t.Errorf("report should contain SWITCH verdict, got: %s", text)
	}
}

// TestReportToolAppliesAgentFilter verifies agent_id filtering.
func TestReportToolAppliesAgentAndDateFilters(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	insertBenchmarkRun(t, ctx, bs, "alpha-agent", store.VerdictKeep)
	insertBenchmarkRun(t, ctx, bs, "beta-agent", store.VerdictSwitch)

	resp := callMCPTool(t, bs, "report", map[string]interface{}{
		"agent_id": "alpha-agent",
	})
	text := getResultText(t, resp)

	if !strings.Contains(text, "alpha-agent") {
		t.Errorf("report should contain alpha-agent, got: %s", text)
	}
	if strings.Contains(text, "beta-agent") {
		t.Errorf("report should NOT contain beta-agent when filtered, got: %s", text)
	}
}

// TestReportToolDaysFilter verifies that the days filter excludes old runs.
func TestReportToolDaysFilter(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	// Insert a fresh run.
	insertBenchmarkRun(t, ctx, bs, "fresh-agent", store.VerdictKeep)

	// Insert an old run (15 days ago) by using direct SaveRun.
	oldRun := store.BenchmarkRun{
		RunAt:          time.Now().Add(-15 * 24 * time.Hour).UTC(),
		WindowDays:     7,
		AgentID:        "old-agent",
		Model:          "gpt-4",
		Verdict:        store.VerdictSwitch,
		DecisionReason: "stale",
	}
	if err := bs.SaveRun(ctx, oldRun); err != nil {
		t.Fatalf("SaveRun old: %v", err)
	}

	// Request only last 7 days.
	resp := callMCPTool(t, bs, "report", map[string]interface{}{
		"days": float64(7),
	})
	text := getResultText(t, resp)

	if !strings.Contains(text, "fresh-agent") {
		t.Errorf("report should contain fresh-agent, got: %s", text)
	}
	if strings.Contains(text, "old-agent") {
		t.Errorf("report should NOT contain old-agent (15 days old), got: %s", text)
	}
}

// TestReportToolEmptyReturnsMessage verifies that no runs returns a helpful message.
func TestReportToolEmptyReturnsMessage(t *testing.T) {
	bs := newTestBenchmarkStoreForMCP(t)

	resp := callMCPTool(t, bs, "report", map[string]interface{}{})
	text := getResultText(t, resp)

	if !strings.Contains(text, "No benchmark runs") {
		t.Errorf("expected 'No benchmark runs' message, got: %s", text)
	}
}

// --- Task 18: model_changes tool tests ---

// TestModelChangesReturnsPendingSwitches verifies only SWITCH/URGENT_SWITCH are returned.
func TestModelChangesReturnsPendingSwitches(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	insertBenchmarkRun(t, ctx, bs, "keep-agent", store.VerdictKeep)
	insertBenchmarkRun(t, ctx, bs, "switch-agent", store.VerdictSwitch)
	insertBenchmarkRun(t, ctx, bs, "urgent-agent", store.VerdictUrgentSwitch)

	resp := callMCPTool(t, bs, "model_changes", map[string]interface{}{})
	text := getResultText(t, resp)

	if !strings.Contains(text, "switch-agent") {
		t.Errorf("model_changes should contain switch-agent, got: %s", text)
	}
	if !strings.Contains(text, "urgent-agent") {
		t.Errorf("model_changes should contain urgent-agent, got: %s", text)
	}
	if strings.Contains(text, "keep-agent") {
		t.Errorf("model_changes should NOT contain keep-agent (KEEP verdict), got: %s", text)
	}
}

// TestModelChangesFiltersByAgent verifies agent_id filtering in model_changes.
func TestModelChangesFiltersByAgent(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	insertBenchmarkRun(t, ctx, bs, "agent-x", store.VerdictSwitch)
	insertBenchmarkRun(t, ctx, bs, "agent-y", store.VerdictUrgentSwitch)

	resp := callMCPTool(t, bs, "model_changes", map[string]interface{}{
		"agent_id": "agent-x",
	})
	text := getResultText(t, resp)

	if !strings.Contains(text, "agent-x") {
		t.Errorf("expected agent-x in filtered response, got: %s", text)
	}
	if strings.Contains(text, "agent-y") {
		t.Errorf("agent-y should be filtered out, got: %s", text)
	}
}

// TestModelChangesNoPendingReturnsMessage verifies message when no changes are pending.
func TestModelChangesNoPendingReturnsMessage(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	insertBenchmarkRun(t, ctx, bs, "happy-agent", store.VerdictKeep)

	resp := callMCPTool(t, bs, "model_changes", map[string]interface{}{})
	text := getResultText(t, resp)

	if !strings.Contains(text, "No pending model switches") {
		t.Errorf("expected 'No pending model switches' message, got: %s", text)
	}
}

// TestModelChangesIncludesRecommendedModel verifies recommended model is included.
func TestModelChangesIncludesRecommendedModel(t *testing.T) {
	ctx := context.Background()
	bs := newTestBenchmarkStoreForMCP(t)

	run := store.BenchmarkRun{
		RunAt:            time.Now().UTC(),
		WindowDays:       7,
		AgentID:          "code-agent",
		Model:            "claude-sonnet-4",
		Verdict:          store.VerdictSwitch,
		RecommendedModel: "claude-haiku",
		DecisionReason:   "Accuracy 0.82 below threshold 0.85",
		SampleSize:       100,
	}
	if err := bs.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	resp := callMCPTool(t, bs, "model_changes", map[string]interface{}{})
	text := getResultText(t, resp)

	if !strings.Contains(text, "claude-haiku") {
		t.Errorf("expected claude-haiku in response, got: %s", text)
	}
	if !strings.Contains(text, "claude-sonnet-4") {
		t.Errorf("expected claude-sonnet-4 in response, got: %s", text)
	}
}
