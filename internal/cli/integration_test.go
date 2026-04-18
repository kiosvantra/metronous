package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kiosvantra/metronous/internal/cli"
	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/kiosvantra/metronous/internal/store"
	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/tracking"
)

// integrationSetup runs `metronous init` in a temp home and returns the home path.
func integrationSetup(t *testing.T) string {
	t.Helper()
	tempHome := t.TempDir()
	cmd := cli.NewInitCommand()
	cmd.SetArgs([]string{"--home", tempHome})
	// Suppress output during init.
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return tempHome
}

// makeIngestJSON builds a newline-delimited JSON-RPC ingest request.
func makeIngestJSON(id int, args map[string]interface{}) string {
	params := map[string]interface{}{
		"name":      "ingest",
		"arguments": args,
	}
	paramsBytes, _ := json.Marshal(params)

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  json.RawMessage(paramsBytes),
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// validIngestArgs returns a minimal valid set of arguments for the ingest tool.
func validIngestArgs(agentID, sessionID string) map[string]interface{} {
	return map[string]interface{}{
		"agent_id":          agentID,
		"session_id":        sessionID,
		"event_type":        "tool_call",
		"model":             "claude-sonnet-4-5",
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
		"duration_ms":       float64(150),
		"prompt_tokens":     float64(100),
		"completion_tokens": float64(50),
		"cost_usd":          0.005,
		"quality_score":     0.90,
	}
}

// TestIngestEndToEndPersistsEvent is the primary end-to-end integration test.
// It: runs init → starts MCP server in goroutine → sends an ingest request →
// verifies persistence in SQLite → shuts down cleanly.
func TestIngestEndToEndPersistsEvent(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	// Open store.
	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	// Create queue and start.
	logger := zap.NewNop()
	queue := tracking.NewEventQueue(es, 100, logger)
	queue.Start()

	// Build an MCP server with in-memory buffers (stdin/stdout pipes).
	ingestArgs := validIngestArgs("agent-e2e", "session-e2e")
	requestLine := makeIngestJSON(1, ingestArgs)

	var outBuf bytes.Buffer
	srv := mcp.NewServer(strings.NewReader(requestLine+"\n"), &outBuf, logger)
	mcp.RegisterDefaultTools(srv)
	mcp.RegisterIngestHandler(srv, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngest(ctx, req, queue)
	})

	// Serve (reads requestLine, processes, EOF).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.ServeStdio(ctx); err != nil && err != context.DeadlineExceeded {
		t.Logf("ServeStdio: %v (expected on EOF)", err)
	}

	// Graceful shutdown: drain queue.
	queue.Stop()

	// Verify the response.
	if outBuf.Len() == 0 {
		t.Fatal("expected response from server, got none")
	}
	var resp mcp.Response
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, outBuf.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}

	// Decode the result to verify it's a success.
	resultBytes, _ := json.Marshal(resp.Result)
	var toolResult mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if toolResult.IsError {
		t.Fatalf("ingest returned isError=true: %v", toolResult.Content)
	}

	// Verify the event was persisted in SQLite.
	// Give a small grace period in case the queue writer is slightly behind.
	var events []store.Event
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err = es.QueryEvents(context.Background(), store.EventQuery{AgentID: "agent-e2e"})
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(events))
	}

	e := events[0]
	if e.AgentID != "agent-e2e" {
		t.Errorf("AgentID: got %q, want %q", e.AgentID, "agent-e2e")
	}
	if e.SessionID != "session-e2e" {
		t.Errorf("SessionID: got %q, want %q", e.SessionID, "session-e2e")
	}
	if e.EventType != "tool_call" {
		t.Errorf("EventType: got %q, want %q", e.EventType, "tool_call")
	}
	if e.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: got %q, want %q", e.Model, "claude-sonnet-4-5")
	}

	t.Logf("event persisted: id=%s agent=%s type=%s", e.ID, e.AgentID, e.EventType)
}

// TestIngestInvalidPayloadLeavesStoreUnchanged verifies that invalid requests
// don't write anything to the database.
func TestIngestInvalidPayloadLeavesStoreUnchanged(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	queue := tracking.NewEventQueue(es, 100, zap.NewNop())
	queue.Start()

	invalidRequests := []struct {
		name string
		args map[string]interface{}
	}{
		{
			name: "missing_agent_id",
			args: map[string]interface{}{
				"session_id": "s1",
				"event_type": "tool_call",
				"model":      "gpt-4",
				"timestamp":  time.Now().UTC().Format(time.RFC3339),
			},
		},
		{
			name: "invalid_timestamp",
			args: map[string]interface{}{
				"agent_id":   "agent-bad-ts",
				"session_id": "s1",
				"event_type": "tool_call",
				"model":      "gpt-4",
				"timestamp":  "not-a-valid-timestamp",
			},
		},
		{
			name: "invalid_event_type",
			args: map[string]interface{}{
				"agent_id":   "agent-bad-type",
				"session_id": "s1",
				"event_type": "unknown_event",
				"model":      "gpt-4",
				"timestamp":  time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	for i, tc := range invalidRequests {
		t.Run(tc.name, func(t *testing.T) {
			requestLine := makeIngestJSON(i+1, tc.args)
			var outBuf bytes.Buffer
			srv := mcp.NewServer(strings.NewReader(requestLine+"\n"), &outBuf, zap.NewNop())
			mcp.RegisterDefaultTools(srv)
			mcp.RegisterIngestHandler(srv, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return tracking.HandleIngest(ctx, req, queue)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.ServeStdio(ctx)

			// Verify the response indicates an error.
			if outBuf.Len() > 0 {
				var resp mcp.Response
				if err := json.Unmarshal(outBuf.Bytes(), &resp); err == nil {
					resultBytes, _ := json.Marshal(resp.Result)
					var toolResult mcp.CallToolResult
					if jsonErr := json.Unmarshal(resultBytes, &toolResult); jsonErr == nil {
						if !toolResult.IsError {
							t.Errorf("expected isError=true for %s", tc.name)
						}
					}
				}
			}
		})
	}

	// Drain the queue to ensure all async writes have completed.
	queue.Stop()

	// Verify NO valid events were persisted (all requests were invalid).
	events, err := es.QueryEvents(context.Background(), store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("invalid payloads should leave store unchanged, got %d events", len(events))
	}
}

// TestIngestMalformedJSONReturnsParseError verifies malformed JSON is handled.
func TestIngestMalformedJSONReturnsParseError(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	malformedJSON := `{"jsonrpc": "2.0", "id": 1, "method": "tools/call", invalid json}`

	var outBuf bytes.Buffer
	srv := mcp.NewServer(strings.NewReader(malformedJSON+"\n"), &outBuf, zap.NewNop())
	mcp.RegisterDefaultTools(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.ServeStdio(ctx)

	if outBuf.Len() == 0 {
		t.Fatal("expected error response for malformed JSON, got none")
	}

	var resp mcp.Response
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, outBuf.String())
	}

	if resp.Error == nil {
		t.Fatal("expected error in response for malformed JSON")
	}
	if resp.Error.Code != mcp.ErrCodeParseError {
		t.Errorf("error code: got %d, want %d (parse error)", resp.Error.Code, mcp.ErrCodeParseError)
	}

	// Verify store is empty.
	events, err := es.QueryEvents(context.Background(), store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("malformed JSON should not write to store, got %d events", len(events))
	}
}

// TestIngestHighThroughput verifies 1000 events in rapid succession all persist correctly.
func TestIngestHighThroughput(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	queue := tracking.NewEventQueue(es, 2000, zap.NewNop())
	queue.Start()

	const totalEvents = 1000
	start := time.Now()

	// Build all requests as a newline-delimited stream.
	var sb strings.Builder
	for i := 0; i < totalEvents; i++ {
		args := validIngestArgs(
			fmt.Sprintf("agent-throughput"),
			fmt.Sprintf("session-%d", i%10),
		)
		// Vary timestamps slightly to avoid collisions.
		ts := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		args["timestamp"] = ts.Format(time.RFC3339)
		line := makeIngestJSON(i+1, args)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	var outBuf bytes.Buffer
	srv := mcp.NewServer(strings.NewReader(sb.String()), &outBuf, zap.NewNop())
	mcp.RegisterDefaultTools(srv)
	mcp.RegisterIngestHandler(srv, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tracking.HandleIngest(ctx, req, queue)
	})

	timeout := 30 * time.Second
	if runtime.GOOS == "windows" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.ServeStdio(ctx); err != nil && err != context.DeadlineExceeded {
		t.Logf("ServeStdio done: %v", err)
	}

	// Graceful shutdown drains the queue.
	queue.Stop()

	elapsed := time.Since(start)
	t.Logf("ingested %d events in %v (%.0f events/sec)",
		totalEvents, elapsed, float64(totalEvents)/elapsed.Seconds())

	// Verify ALL events are in the store.
	events, err := es.QueryEvents(context.Background(), store.EventQuery{AgentID: "agent-throughput"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}

	if len(events) != totalEvents {
		t.Errorf("expected %d persisted events, got %d", totalEvents, len(events))
	}

	// Verify agent summary.
	summary, err := es.GetAgentSummary(context.Background(), "agent-throughput")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}
	if summary.TotalEvents != totalEvents {
		t.Errorf("summary TotalEvents: got %d, want %d", summary.TotalEvents, totalEvents)
	}
}

// TestIngestConcurrentMultipleAgents verifies concurrent ingestion from multiple agents.
func TestIngestConcurrentMultipleAgents(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	queue := tracking.NewEventQueue(es, 500, zap.NewNop())
	queue.Start()

	const numAgents = 10
	const eventsPerAgent = 50
	var wg sync.WaitGroup

	for a := 0; a < numAgents; a++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()
			agentName := fmt.Sprintf("agent-%02d", agentID)
			sessionName := fmt.Sprintf("session-%02d", agentID)

			for i := 0; i < eventsPerAgent; i++ {
				ev := store.Event{
					AgentID:   agentName,
					SessionID: sessionName,
					EventType: "tool_call",
					Model:     "claude-sonnet-4-5",
					Timestamp: time.Now().UTC(),
				}
				if err := queue.Enqueue(ev); err != nil {
					t.Errorf("agent %s: Enqueue[%d]: %v", agentName, i, err)
					return
				}
			}
		}(a)
	}

	wg.Wait()
	queue.Stop()

	// Verify each agent has the correct event count.
	for a := 0; a < numAgents; a++ {
		agentName := fmt.Sprintf("agent-%02d", a)
		summary, err := es.GetAgentSummary(context.Background(), agentName)
		if err != nil {
			t.Errorf("GetAgentSummary(%s): %v", agentName, err)
			continue
		}
		if summary.TotalEvents != eventsPerAgent {
			t.Errorf("agent %s: expected %d events, got %d", agentName, eventsPerAgent, summary.TotalEvents)
		}
	}

	// Verify total.
	allEvents, err := es.QueryEvents(context.Background(), store.EventQuery{})
	if err != nil {
		t.Fatalf("QueryEvents all: %v", err)
	}
	expected := numAgents * eventsPerAgent
	if len(allEvents) != expected {
		t.Errorf("total events: expected %d, got %d", expected, len(allEvents))
	}
}

// TestIngestQueryViaReportTool verifies MCP report tool returns summary data.
func TestIngestQueryViaReportTool(t *testing.T) {
	tempHome := integrationSetup(t)
	dbPath := filepath.Join(tempHome, "data", "tracking.db")

	es, err := sqlite.NewEventStore(dbPath)
	if err != nil {
		t.Fatalf("NewEventStore: %v", err)
	}
	defer es.Close()

	ctx := context.Background()

	// Insert some events directly (bypass MCP for setup).
	for i := 0; i < 5; i++ {
		cost := 0.01 * float64(i+1)
		quality := 0.80 + 0.04*float64(i)
		dur := 100 + i
		ev := store.Event{
			AgentID:      "agent-report",
			SessionID:    "session-report",
			EventType:    "complete",
			Model:        "claude-sonnet-4-5",
			Timestamp:    time.Now().UTC(),
			CostUSD:      &cost,
			QualityScore: &quality,
			DurationMs:   &dur,
		}
		if _, err := es.InsertEvent(ctx, ev); err != nil {
			t.Fatalf("InsertEvent[%d]: %v", i, err)
		}
	}

	// Verify through store API (the report tool itself is a stub in Phase 1).
	summary, err := es.GetAgentSummary(ctx, "agent-report")
	if err != nil {
		t.Fatalf("GetAgentSummary: %v", err)
	}

	if summary.TotalEvents != 5 {
		t.Errorf("TotalEvents: got %d, want 5", summary.TotalEvents)
	}
	// Costs are cumulative per session; agent summary stores MAX(cost_usd) per session.
	if summary.TotalCostUSD < 0.049 || summary.TotalCostUSD > 0.051 {
		t.Errorf("TotalCostUSD: got %.4f, want ~0.05", summary.TotalCostUSD)
	}

	// Verify MCP report tool exists and returns stub response.
	reportReq := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"agent_id":"agent-report"}}}`
	var outBuf bytes.Buffer
	srv := mcp.NewServer(strings.NewReader(reportReq+"\n"), &outBuf, zap.NewNop())
	mcp.RegisterDefaultTools(srv)

	srvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.ServeStdio(srvCtx)

	if outBuf.Len() == 0 {
		t.Fatal("expected response from report tool")
	}
	var resp mcp.Response
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("decode report response: %v", err)
	}
	// In Phase 1, report is a stub — it should return successfully but with stub message.
	if resp.Error != nil {
		t.Errorf("report tool error: %+v", resp.Error)
	}

	t.Logf("report tool response: %s", outBuf.String())
}
