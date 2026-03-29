package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/mcp"
)

// --- Helpers ---

// buildServer creates a test MCP server backed by in-memory buffers.
func buildServer(t *testing.T) (*mcp.Server, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	srv := mcp.NewServer(strings.NewReader(""), out, nil)
	mcp.RegisterDefaultTools(srv)
	return srv, out
}

// sendRequest sends a JSON-RPC request string to the server and returns the decoded response.
func sendRequest(t *testing.T, srv *mcp.Server, rawJSON string) mcp.Response {
	t.Helper()
	out := &bytes.Buffer{}
	in := strings.NewReader(rawJSON + "\n")
	s := mcp.NewServer(in, out, nil)

	// Copy over registered tools from original server
	for _, tool := range srv.ListTools() {
		_ = tool
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // will process one line before ctx check
	_ = s.ServeStdio(ctx)

	// Parse output
	var resp mcp.Response
	if out.Len() == 0 {
		return resp
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response from %q: %v\nraw: %s", rawJSON, err, out.String())
	}
	return resp
}

// callServer sends a JSON-RPC request via a fresh server with given tools pre-registered.
func callServer(t *testing.T, tools []mcp.ToolDefinition, handlers map[string]mcp.ToolHandler, rawRequest string) mcp.Response {
	t.Helper()
	out := &bytes.Buffer{}
	in := strings.NewReader(rawRequest + "\n")
	srv := mcp.NewServer(in, out, nil)

	for _, tool := range tools {
		h := handlers[tool.Name]
		if h == nil {
			h = func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.ContentItem{mcp.TextContent("stub")}}, nil
			}
		}
		srv.RegisterTool(tool, h)
	}

	_ = srv.ServeStdio(context.Background())

	var resp mcp.Response
	if out.Len() == 0 {
		return resp
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, out.String())
	}
	return resp
}

// --- Tests ---

// TestServerRegistersIngestTool verifies that the ingest tool is registered and listed.
func TestServerRegistersIngestTool(t *testing.T) {
	srv := mcp.NewServer(strings.NewReader(""), &bytes.Buffer{}, nil)
	mcp.RegisterDefaultTools(srv)
	mcp.RegisterIngestHandler(srv, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.ContentItem{mcp.TextContent("ok")}}, nil
	})

	if !srv.HasTool("ingest") {
		t.Error("ingest tool not registered")
	}
	if !srv.HasTool("report") {
		t.Error("report tool not registered")
	}
	if !srv.HasTool("model_changes") {
		t.Error("model_changes tool not registered")
	}

	tools := srv.ListTools()
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	if !toolNames["ingest"] {
		t.Error("ingest not in ListTools()")
	}
}

// TestInitializeHandshake verifies the initialize method returns correct server info.
func TestInitializeHandshake(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`
	resp := callServer(t, nil, nil, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result, got nil")
	}

	// The result should contain protocolVersion.
	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["protocolVersion"] != mcp.MCPProtocolVersion {
		t.Errorf("protocolVersion: got %v, want %q", result["protocolVersion"], mcp.MCPProtocolVersion)
	}
}

// TestUnknownToolReturnsMethodNotFound verifies that calling an unknown tool returns -32601.
func TestUnknownToolReturnsMethodNotFound(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nonexistent_tool","arguments":{}}}`
	resp := callServer(t, nil, nil, req)

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if resp.Error.Code != mcp.ErrCodeMethodNotFound {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, mcp.ErrCodeMethodNotFound)
	}
	if !strings.Contains(resp.Error.Message, "nonexistent_tool") {
		t.Errorf("error message should mention tool name: %q", resp.Error.Message)
	}
}

// TestToolsListReturnsRegisteredTools verifies tools/list returns all registered tools.
func TestToolsListReturnsRegisteredTools(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	tools := []mcp.ToolDefinition{mcp.IngestToolDefinition, mcp.ReportToolDefinition}
	resp := callServer(t, tools, nil, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}

	toolList, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools field missing or wrong type: %T", result["tools"])
	}
	if len(toolList) != 2 {
		t.Errorf("expected 2 tools, got %d", len(toolList))
	}
}

// TestPingReturnsEmpty verifies the ping method returns an empty object.
func TestPingReturnsEmpty(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":4,"method":"ping"}`
	resp := callServer(t, nil, nil, req)

	if resp.Error != nil {
		t.Fatalf("ping error: %+v", resp.Error)
	}
}

// TestUnknownMethodReturnsMethodNotFound verifies unknown methods return -32601.
func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":5,"method":"not/a/real/method"}`
	resp := callServer(t, nil, nil, req)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != mcp.ErrCodeMethodNotFound {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, mcp.ErrCodeMethodNotFound)
	}
}

// TestToolCallSuccess verifies a registered tool handler is called and result returned.
func TestToolCallSuccess(t *testing.T) {
	called := false
	handler := func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{
			Content: []mcp.ContentItem{mcp.TextContent("success")},
		}, nil
	}

	tools := []mcp.ToolDefinition{mcp.IngestToolDefinition}
	handlers := map[string]mcp.ToolHandler{"ingest": handler}

	req := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"ingest","arguments":{"agent_id":"a1","session_id":"s1","event_type":"start","model":"m1","timestamp":"2026-01-01T00:00:00Z"}}}`
	resp := callServer(t, tools, handlers, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !called {
		t.Error("handler was not called")
	}
}

// TestToolCallHandlerError verifies that handler errors produce isError result (not JSON-RPC error).
func TestToolCallHandlerError(t *testing.T) {
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("handler internal error")
	}

	tools := []mcp.ToolDefinition{mcp.IngestToolDefinition}
	handlers := map[string]mcp.ToolHandler{"ingest": handler}

	req := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ingest","arguments":{}}}`
	resp := callServer(t, tools, handlers, req)

	// Per MCP spec, tool execution errors should be returned as successful JSON-RPC
	// responses with isError=true in the content.
	if resp.Error != nil {
		t.Errorf("unexpected JSON-RPC error (should be tool-level error): %+v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if result["isError"] != true {
		t.Errorf("expected isError=true for handler error, got: %v", result["isError"])
	}
}

// TestParseErrorOnInvalidJSON verifies parse error is returned for malformed JSON.
func TestParseErrorOnInvalidJSON(t *testing.T) {
	out := &bytes.Buffer{}
	in := strings.NewReader("{invalid json}\n")
	srv := mcp.NewServer(in, out, nil)
	_ = srv.ServeStdio(context.Background())

	if out.Len() == 0 {
		t.Fatal("expected error response for invalid JSON")
	}
	var resp mcp.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error in response")
	}
	if resp.Error.Code != mcp.ErrCodeParseError {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, mcp.ErrCodeParseError)
	}
}

// TestNotificationNoResponse verifies notifications don't produce a response.
func TestNotificationNoResponse(t *testing.T) {
	out := &bytes.Buffer{}
	// Notification has no "id" field
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	srv := mcp.NewServer(in, out, nil)
	_ = srv.ServeStdio(context.Background())

	if out.Len() != 0 {
		t.Errorf("notification should produce no response, got: %s", out.String())
	}
}

// TestServeWithHealthEndpoint verifies that ServeWithHealth starts an HTTP
// health server on a dynamic port and returns a 200 JSON response.
func TestServeWithHealthEndpoint(t *testing.T) {
	// Use a context-cancelled server so ServeStdio blocks until we are done.
	// An empty reader would EOF immediately and trigger the deferred removePortFile
	// before the test can read it.
	pr, pw := io.Pipe()
	defer pw.Close()

	out := &bytes.Buffer{}
	srv := mcp.NewServer(pr, out, nil)

	// Use a temp dir so the port file is instance-scoped and doesn't interfere
	// with other running processes.
	dataDir := t.TempDir()
	srv.SetDataDir(dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ServeWithHealth blocks until EOF or context cancel; run in goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeWithHealth(ctx)
	}()

	// Give the HTTP server a moment to start.
	time.Sleep(200 * time.Millisecond)

	// Read the port file (now a method on *Server, instance-scoped to data-dir).
	port, err := srv.ReadPortFile()
	if err != nil {
		t.Fatalf("ReadPortFile: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("invalid port from port file: %d", port)
	}

	// Hit the /health endpoint.
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v\nraw: %s", err, body)
	}
	if payload["status"] != "ok" {
		t.Errorf("status: got %v, want ok", payload["status"])
	}
	if payload["name"] != mcp.ServerName {
		t.Errorf("name: got %v, want %s", payload["name"], mcp.ServerName)
	}
	if payload["version"] != mcp.ServerVersion {
		t.Errorf("version: got %v, want %s", payload["version"], mcp.ServerVersion)
	}

	// Cancel the context to trigger graceful shutdown (closes the pipe).
	cancel()
	pw.Close()

	// Wait for ServeWithHealth to return.
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("ServeWithHealth returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("ServeWithHealth did not return after context cancel")
	}
}

// TestHTTPIngestEndpoint verifies that POST /ingest calls the registered ingest handler.
func TestHTTPIngestEndpoint(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	out := &bytes.Buffer{}
	srv := mcp.NewServer(pr, out, nil)

	dataDir := t.TempDir()
	srv.SetDataDir(dataDir)

	// Register ingest handler that records the arguments received.
	var gotArgs map[string]interface{}
	mcp.RegisterIngestHandler(srv, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		gotArgs = req.Arguments
		return &mcp.CallToolResult{Content: []mcp.ContentItem{mcp.TextContent("ok")}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeWithHealth(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	port, err := srv.ReadPortFile()
	if err != nil {
		t.Fatalf("ReadPortFile: %v", err)
	}

	payload := map[string]interface{}{
		"agent_id":   "test-agent",
		"session_id": "sess-1",
		"event_type": "start",
		"model":      "claude-3",
		"timestamp":  "2026-01-01T00:00:00Z",
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("http://127.0.0.1:%d/ingest", port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /ingest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if gotArgs == nil {
		t.Fatal("handler was not called")
	}
	if gotArgs["agent_id"] != "test-agent" {
		t.Errorf("agent_id: got %v, want test-agent", gotArgs["agent_id"])
	}
	if gotArgs["event_type"] != "start" {
		t.Errorf("event_type: got %v, want start", gotArgs["event_type"])
	}

	cancel()
	pw.Close()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("ServeWithHealth returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("ServeWithHealth did not return after context cancel")
	}
}

// TestHTTPIngestMethodNotAllowed verifies that GET /ingest returns 405.
func TestHTTPIngestMethodNotAllowed(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	out := &bytes.Buffer{}
	srv := mcp.NewServer(pr, out, nil)
	dataDir := t.TempDir()
	srv.SetDataDir(dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeWithHealth(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	port, err := srv.ReadPortFile()
	if err != nil {
		t.Fatalf("ReadPortFile: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/ingest", port)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET /ingest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}

	cancel()
	pw.Close()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("ServeWithHealth returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("ServeWithHealth did not return after context cancel")
	}
}

// TestIngestToolDefinitionHasRequiredFields verifies the ingest tool schema.
func TestIngestToolDefinitionHasRequiredFields(t *testing.T) {
	def := mcp.IngestToolDefinition

	required := []string{"agent_id", "session_id", "event_type", "model", "timestamp"}
	requiredSet := make(map[string]bool)
	for _, r := range def.InputSchema.Required {
		requiredSet[r] = true
	}

	for _, field := range required {
		if !requiredSet[field] {
			t.Errorf("required field %q not in InputSchema.Required", field)
		}
	}

	// All required fields must also be in properties.
	for _, field := range required {
		if _, ok := def.InputSchema.Properties[field]; !ok {
			t.Errorf("required field %q not in InputSchema.Properties", field)
		}
	}

	fmt.Println("IngestTool required fields validated:", required)
}
