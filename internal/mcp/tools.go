package mcp

import "context"

// IngestToolDefinition is the MCP tool definition for the ingest endpoint.
// It receives telemetry events from AI agent plugins and stores them in SQLite.
var IngestToolDefinition = ToolDefinition{
	Name:        "ingest",
	Description: "Ingest a telemetry event from an AI agent session.",
	InputSchema: InputSchema{
		Type: "object",
		Properties: map[string]Property{
			"agent_id": {
				Type:        "string",
				Description: "Unique identifier for the AI agent (required).",
			},
			"session_id": {
				Type:        "string",
				Description: "Session identifier grouping related events (required).",
			},
			"event_type": {
				Type:        "string",
				Description: "Type of event: start, tool_call, retry, complete, error (required).",
			},
			"model": {
				Type:        "string",
				Description: "LLM model identifier used for this event (required).",
			},
			"timestamp": {
				Type:        "string",
				Format:      "date-time",
				Description: "ISO 8601 UTC timestamp of the event (required).",
			},
			"duration_ms": {
				Type:        "integer",
				Description: "Duration of the operation in milliseconds (optional).",
			},
			"prompt_tokens": {
				Type:        "integer",
				Description: "Number of input tokens consumed (optional).",
			},
			"completion_tokens": {
				Type:        "integer",
				Description: "Number of output tokens generated (optional).",
			},
			"cost_usd": {
				Type:        "number",
				Description: "Estimated cost of the event in USD (optional).",
			},
			"quality_score": {
				Type:        "number",
				Description: "Task quality score between 0.0 and 1.0 (optional).",
			},
			"rework_count": {
				Type:        "integer",
				Description: "Number of retry/rework iterations (optional).",
			},
			"tool_name": {
				Type:        "string",
				Description: "Name of the tool called, for event_type=tool_call (optional).",
			},
			"tool_success": {
				Type:        "boolean",
				Description: "Whether the tool call succeeded (optional).",
			},
			"metadata": {
				Type:        "object",
				Description: "Arbitrary key-value pairs for extensibility (optional).",
			},
		},
		Required: []string{"agent_id", "session_id", "event_type", "model", "timestamp"},
	},
}

// ReportToolDefinition is the MCP tool stub for generating benchmark reports.
var ReportToolDefinition = ToolDefinition{
	Name:        "report",
	Description: "Retrieve the latest benchmark report for one or all agents.",
	InputSchema: InputSchema{
		Type: "object",
		Properties: map[string]Property{
			"agent_id": {
				Type:        "string",
				Description: "Filter report to a specific agent (optional, omit for all agents).",
			},
			"since": {
				Type:        "string",
				Format:      "date-time",
				Description: "Return runs after this date (optional).",
			},
		},
	},
}

// ModelChangesToolDefinition is the MCP tool stub for listing pending model switches.
var ModelChangesToolDefinition = ToolDefinition{
	Name:        "model_changes",
	Description: "List pending SWITCH and URGENT_SWITCH verdicts for agents.",
	InputSchema: InputSchema{
		Type: "object",
		Properties: map[string]Property{
			"agent_id": {
				Type:        "string",
				Description: "Filter changes to a specific agent (optional).",
			},
		},
	},
}

// stubHandler returns a not-yet-implemented response for stub tools.
func stubHandler(_ context.Context, req CallToolRequest) (*CallToolResult, error) {
	return &CallToolResult{
		Content: []ContentItem{
			TextContent("tool '" + req.Name + "' is not yet implemented in this version"),
		},
	}, nil
}

// RegisterDefaultTools registers all standard Metronous MCP tools on the server,
// using stub handlers for tools not yet backed by storage.
// Call this before ServeStdio to expose tool metadata to MCP clients.
func RegisterDefaultTools(s *Server) {
	// report and model_changes use stubs until Phase 2 implementation.
	s.RegisterTool(ReportToolDefinition, stubHandler)
	s.RegisterTool(ModelChangesToolDefinition, stubHandler)
	// ingest is registered separately via RegisterIngestHandler so it
	// can be wired to a real handler.
}

// RegisterIngestHandler registers the ingest tool with the given handler function.
func RegisterIngestHandler(s *Server, handler ToolHandler) {
	s.RegisterTool(IngestToolDefinition, handler)
}
