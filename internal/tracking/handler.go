package tracking

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enduluc/metronous/internal/mcp"
	"github.com/enduluc/metronous/internal/store"
)

// ValidEventTypes is the set of accepted event_type values for ingest requests.
var ValidEventTypes = map[string]bool{
	"start":     true,
	"tool_call": true,
	"retry":     true,
	"complete":  true,
	"error":     true,
}

// IngestRequest holds the validated fields from an ingest MCP tool call.
// All required fields are guaranteed non-empty after validation.
type IngestRequest struct {
	AgentID          string
	SessionID        string
	EventType        string
	Model            string
	Timestamp        time.Time
	DurationMs       *int
	PromptTokens     *int
	CompletionTokens *int
	CostUSD          *float64
	QualityScore     *float64
	ReworkCount      *int
	ToolName         *string
	ToolSuccess      *bool
	Metadata         map[string]interface{}
}

// ValidationError is returned when an ingest request has missing or invalid fields.
type ValidationError struct {
	Field   string
	Message string
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("validation error: field %q — %s", v.Field, v.Message)
}

// validateIngestRequest validates and normalizes the raw arguments from an MCP tool call.
// Returns a populated IngestRequest or a ValidationError.
func validateIngestRequest(args map[string]interface{}) (*IngestRequest, error) {
	req := &IngestRequest{}

	// --- Required fields ---

	agentID, err := requireString(args, "agent_id")
	if err != nil {
		return nil, err
	}
	req.AgentID = agentID

	sessionID, err := requireString(args, "session_id")
	if err != nil {
		return nil, err
	}
	req.SessionID = sessionID

	eventType, err := requireString(args, "event_type")
	if err != nil {
		return nil, err
	}
	if !ValidEventTypes[eventType] {
		validList := make([]string, 0, len(ValidEventTypes))
		for k := range ValidEventTypes {
			validList = append(validList, k)
		}
		return nil, &ValidationError{
			Field:   "event_type",
			Message: fmt.Sprintf("must be one of: %s", strings.Join(validList, ", ")),
		}
	}
	req.EventType = eventType

	model, err := requireString(args, "model")
	if err != nil {
		return nil, err
	}
	req.Model = model

	tsRaw, err := requireString(args, "timestamp")
	if err != nil {
		return nil, err
	}
	ts, parseErr := time.Parse(time.RFC3339, tsRaw)
	if parseErr != nil {
		return nil, &ValidationError{
			Field:   "timestamp",
			Message: "must be a valid RFC3339 / ISO 8601 timestamp (e.g. 2026-01-01T00:00:00Z)",
		}
	}
	if ts.IsZero() {
		return nil, &ValidationError{
			Field:   "timestamp",
			Message: "timestamp cannot be zero",
		}
	}
	req.Timestamp = ts.UTC()

	// --- Optional fields ---

	req.DurationMs = optionalInt(args, "duration_ms")
	req.PromptTokens = optionalInt(args, "prompt_tokens")
	req.CompletionTokens = optionalInt(args, "completion_tokens")
	req.CostUSD = optionalFloat(args, "cost_usd")
	req.QualityScore = optionalFloat(args, "quality_score")
	req.ReworkCount = optionalInt(args, "rework_count")

	if toolName, ok := args["tool_name"].(string); ok && toolName != "" {
		req.ToolName = &toolName
	}
	if toolSuccess, ok := args["tool_success"].(bool); ok {
		req.ToolSuccess = &toolSuccess
	}

	if meta, ok := args["metadata"].(map[string]interface{}); ok {
		req.Metadata = meta
	}

	return req, nil
}

// toStoreEvent converts a validated IngestRequest to a store.Event.
func toStoreEvent(req *IngestRequest) store.Event {
	return store.Event{
		AgentID:          req.AgentID,
		SessionID:        req.SessionID,
		EventType:        req.EventType,
		Model:            req.Model,
		Timestamp:        req.Timestamp,
		DurationMs:       req.DurationMs,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		CostUSD:          req.CostUSD,
		QualityScore:     req.QualityScore,
		ReworkCount:      req.ReworkCount,
		ToolName:         req.ToolName,
		ToolSuccess:      req.ToolSuccess,
		Metadata:         req.Metadata,
	}
}

// IngestHandler validates an MCP ingest tool call and enqueues the event.
// It implements the mcp.ToolHandler signature.
type IngestHandler struct {
	queue *EventQueue
}

// NewIngestHandler creates an IngestHandler backed by the given EventQueue.
func NewIngestHandler(queue *EventQueue) *IngestHandler {
	return &IngestHandler{queue: queue}
}

// Handle is the mcp.ToolHandler function for the ingest tool.
func (h *IngestHandler) Handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return HandleIngest(ctx, req, h.queue)
}

// HandleIngest is a standalone ingest handler function.
// Validates, normalizes, and enqueues the event for async persistence.
func HandleIngest(ctx context.Context, req mcp.CallToolRequest, queue *EventQueue) (*mcp.CallToolResult, error) {
	// Validate the request arguments.
	ingestReq, err := validateIngestRequest(req.Arguments)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.ContentItem{mcp.TextContent(err.Error())},
			IsError: true,
		}, nil
	}

	// Convert to store event.
	event := toStoreEvent(ingestReq)

	// Enqueue for async storage.
	if qErr := queue.Enqueue(event); qErr != nil {
		return &mcp.CallToolResult{
			Content: []mcp.ContentItem{mcp.TextContent("failed to enqueue event: " + qErr.Error())},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.ContentItem{
			mcp.TextContent(fmt.Sprintf("event ingested: agent=%s session=%s type=%s",
				ingestReq.AgentID, ingestReq.SessionID, ingestReq.EventType)),
		},
	}, nil
}

// HandleIngestDirect is a synchronous version of HandleIngest that writes directly
// to the EventStore without going through the queue. Useful for testing.
func HandleIngestDirect(ctx context.Context, req mcp.CallToolRequest, es store.EventStore) (*mcp.CallToolResult, error) {
	ingestReq, err := validateIngestRequest(req.Arguments)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.ContentItem{mcp.TextContent(err.Error())},
			IsError: true,
		}, nil
	}

	event := toStoreEvent(ingestReq)
	id, storeErr := es.InsertEvent(ctx, event)
	if storeErr != nil {
		return &mcp.CallToolResult{
			Content: []mcp.ContentItem{mcp.TextContent("store error: " + storeErr.Error())},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.ContentItem{
			mcp.TextContent(fmt.Sprintf("event persisted: id=%s agent=%s type=%s", id, ingestReq.AgentID, ingestReq.EventType)),
		},
	}, nil
}

// --- Field helpers ---

// requireString extracts a required string field from the arguments map.
func requireString(args map[string]interface{}, field string) (string, error) {
	v, ok := args[field]
	if !ok || v == nil {
		return "", &ValidationError{Field: field, Message: "is required"}
	}
	s, ok := v.(string)
	if !ok {
		return "", &ValidationError{Field: field, Message: "must be a string"}
	}
	if strings.TrimSpace(s) == "" {
		return "", &ValidationError{Field: field, Message: "must not be empty"}
	}
	return s, nil
}

// optionalInt extracts an optional integer field (JSON numbers come as float64).
func optionalInt(args map[string]interface{}, field string) *int {
	v, ok := args[field]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		return &n
	case int64:
		i := int(n)
		return &i
	}
	return nil
}

// optionalFloat extracts an optional float field.
func optionalFloat(args map[string]interface{}, field string) *float64 {
	v, ok := args[field]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		f := float64(n)
		return &f
	}
	return nil
}
