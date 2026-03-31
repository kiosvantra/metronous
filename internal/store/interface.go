// Package store defines the storage interfaces and data models for Metronous.
// All implementations are expected to be storage-agnostic (SQLite default,
// PostgreSQL optional in the future).
package store

import (
	"context"
	"encoding/json"
	"time"
)

// Event represents a single telemetry event ingested from an AI agent session.
// Fields are aligned with the MCP ingest tool schema.
type Event struct {
	// ID is a UUID v4 generated at ingest time.
	ID string

	// AgentID identifies the AI agent that produced this event.
	AgentID string

	// SessionID groups related events into a single agent session.
	SessionID string

	// EventType categorizes the event: start, tool_call, retry, complete, error.
	EventType string

	// Model is the LLM model identifier used during this event (e.g. "claude-sonnet-4-5").
	Model string

	// Timestamp is when the event occurred (UTC).
	Timestamp time.Time

	// DurationMs is the duration of the operation in milliseconds (nullable).
	DurationMs *int

	// PromptTokens is the number of input tokens consumed (nullable).
	PromptTokens *int

	// CompletionTokens is the number of output tokens generated (nullable).
	CompletionTokens *int

	// CostUSD is the estimated cost of the event in USD (nullable).
	CostUSD *float64

	// QualityScore is a 0.0–1.0 quality rating for this event (nullable).
	QualityScore *float64

	// ReworkCount is how many times this task was retried/reworked (nullable).
	ReworkCount *int

	// ToolName is the name of the tool called, if EventType == "tool_call" (nullable).
	ToolName *string

	// ToolSuccess indicates whether the tool call succeeded (nullable).
	ToolSuccess *bool

	// Metadata holds arbitrary JSON key-value pairs for extensibility.
	Metadata map[string]interface{}
}

// EventQuery defines filter criteria for querying stored events.
type EventQuery struct {
	// AgentID filters events by agent (empty = all agents).
	AgentID string

	// SessionID filters events by session (empty = all sessions).
	SessionID string

	// EventType filters events by type (empty = all types).
	EventType string

	// Since filters events on or after this timestamp (zero = no lower bound).
	Since time.Time

	// Until filters events before or on this timestamp (zero = no upper bound).
	Until time.Time

	// Limit caps the number of events returned (0 = no limit).
	Limit int

	// Offset skips the first N events in the result set (0 = no skip).
	// Combined with Limit, this enables sliding-window pagination.
	// The query orders by timestamp DESC, so offset=0 returns the newest events
	// and increasing offset moves toward older events.
	Offset int
}

// AgentSummary provides aggregated metrics for a single agent.
type AgentSummary struct {
	// AgentID identifies the agent.
	AgentID string

	// LastEventTs is the timestamp of the most recent event.
	LastEventTs time.Time

	// TotalEvents is the total number of events recorded.
	TotalEvents int

	// TotalCostUSD is the sum of all event costs in USD.
	TotalCostUSD float64

	// AvgQuality is the mean quality score across all rated events.
	AvgQuality float64
}

// SessionSummary holds the representative data for a single session,
// derived from the session's "complete" event (or the latest event if none).
// It is used by the Tracking TUI to show one collapsed row per session.
type SessionSummary struct {
	// SessionID is the unique session identifier.
	SessionID string

	// AgentID is the agent that produced this session.
	AgentID string

	// Model is the LLM model used (from the complete event).
	Model string

	// Timestamp is the time of the representative event (complete, or latest).
	Timestamp time.Time

	// PromptTokens is the total input tokens for the session (nullable).
	PromptTokens *int

	// CompletionTokens is the total output tokens for the session (nullable).
	CompletionTokens *int

	// CostUSD is the total cost for the session (nullable).
	CostUSD *float64

	// DurationMs is the duration of the session in milliseconds (nullable).
	// It is populated from the session's `complete` event when present.
	DurationMs *int
}

// SessionQuery defines filter criteria for querying sessions.
type SessionQuery struct {
	// AgentID filters sessions by agent (empty = all agents).
	AgentID string

	// Limit caps the number of sessions returned (0 = no limit).
	Limit int

	// Offset skips the first N sessions in the result set (0 = no skip).
	// Sessions are ordered by their most recent event timestamp DESC,
	// so offset=0 returns the newest sessions.
	Offset int
}

// EventStore is the primary storage interface for telemetry events.
// Implementations must be safe for concurrent reads, but writes should
// be funneled through the EventQueue (single-writer channel pattern).
type EventStore interface {
	// InsertEvent persists a single event and returns its ID.
	InsertEvent(ctx context.Context, event Event) (string, error)

	// QueryEvents retrieves events matching the supplied filter criteria.
	QueryEvents(ctx context.Context, query EventQuery) ([]Event, error)

	// CountEvents returns the total number of events matching the supplied filter criteria.
	// This is primarily used to implement UI pagination navigation (e.g. Home/End).
	CountEvents(ctx context.Context, query EventQuery) (int, error)

	// QuerySessions returns a page of SessionSummary rows, one per distinct session_id,
	// ordered by the session's most recent event timestamp DESC.
	// Each summary is populated from the session's "complete" event when present,
	// falling back to the latest event otherwise.
	QuerySessions(ctx context.Context, query SessionQuery) ([]SessionSummary, error)

	// GetSessionEvents returns all events for the given session_id, ordered
	// by timestamp ASC (chronological order for the expand view).
	GetSessionEvents(ctx context.Context, sessionID string) ([]Event, error)

	// GetAgentEvents returns all events for a specific agent since a given time.
	GetAgentEvents(ctx context.Context, agentID string, since time.Time) ([]Event, error)

	// GetAgentSummary returns aggregated metrics for the specified agent.
	GetAgentSummary(ctx context.Context, agentID string) (AgentSummary, error)

	// Close releases all resources held by the store.
	Close() error
}

// VerdictType classifies the decision engine's recommendation for an agent.
type VerdictType string

const (
	// VerdictKeep means the agent's current model meets all thresholds.
	VerdictKeep VerdictType = "KEEP"

	// VerdictSwitch means one or more soft thresholds are breached.
	VerdictSwitch VerdictType = "SWITCH"

	// VerdictUrgentSwitch means a critical threshold is breached.
	VerdictUrgentSwitch VerdictType = "URGENT_SWITCH"

	// VerdictInsufficientData means there are fewer than 50 events in the window.
	VerdictInsufficientData VerdictType = "INSUFFICIENT_DATA"
)

// BenchmarkRun holds all metrics and the verdict for a single weekly benchmark run.
type BenchmarkRun struct {
	// ID is a UUID v4 generated at save time.
	ID string

	// RunAt is when this benchmark was computed (UTC).
	RunAt time.Time

	// WindowDays is the number of days in the evaluation window (default 7).
	WindowDays int

	// AgentID identifies the agent that was benchmarked.
	AgentID string

	// Model is the LLM model the agent was using during the window.
	Model string

	// Accuracy is the ratio of non-error events to total events (0.0–1.0).
	Accuracy float64

	// AvgLatencyMs is the mean duration across all events in the window.
	AvgLatencyMs float64

	// P50LatencyMs is the 50th-percentile latency in milliseconds.
	P50LatencyMs float64

	// P95LatencyMs is the 95th-percentile latency in milliseconds.
	P95LatencyMs float64

	// P99LatencyMs is the 99th-percentile latency in milliseconds.
	P99LatencyMs float64

	// ToolSuccessRate is the fraction of tool_call events that succeeded (0.0–1.0).
	ToolSuccessRate float64

	// ROIScore is a composite quality/cost ratio (higher is better).
	ROIScore float64

	// TotalCostUSD is the total cost of all events in the window.
	TotalCostUSD float64

	// SampleSize is the number of events used to compute these metrics.
	SampleSize int

	// Verdict is the decision engine's recommendation.
	Verdict VerdictType

	// RecommendedModel is the suggested replacement model (empty for KEEP/INSUFFICIENT_DATA).
	RecommendedModel string

	// DecisionReason is a human-readable explanation of the verdict.
	DecisionReason string

	// ArtifactPath is the path to the generated decision artifact JSON file.
	ArtifactPath string

	// AvgQualityScore is the mean quality_score across all rated events in the window.
	AvgQualityScore float64
}

// BenchmarkQuery defines filter criteria for querying benchmark runs.
type BenchmarkQuery struct {
	// AgentID filters runs by agent (empty = all agents).
	AgentID string

	// Limit caps the number of runs returned (0 = no limit).
	Limit int

	// Offset skips the first N runs in the result set (0 = no skip).
	// The query orders by run_at DESC, so offset=0 returns the newest runs
	// and increasing offset moves toward older runs.
	Offset int
}

// BenchmarkStore is the storage interface for benchmark runs.
// Implementations must be safe for concurrent reads. Writes should
// follow the same single-writer pattern used by EventStore.
type BenchmarkStore interface {
	// SaveRun persists a benchmark run. If run.ID is empty, a UUID is generated.
	SaveRun(ctx context.Context, run BenchmarkRun) error

	// GetRuns returns up to limit benchmark runs for the given agent, ordered by
	// run_at DESC. Pass limit=0 for no cap.
	GetRuns(ctx context.Context, agentID string, limit int) ([]BenchmarkRun, error)

	// QueryRuns retrieves benchmark runs matching the supplied filter criteria.
	// Supports Offset/Limit for sliding-window pagination.
	QueryRuns(ctx context.Context, query BenchmarkQuery) ([]BenchmarkRun, error)

	// CountRuns returns the total number of benchmark runs matching the supplied filter.
	// Used for pagination navigation (e.g. Home/End).
	CountRuns(ctx context.Context, query BenchmarkQuery) (int, error)

	// GetLatestRun returns the most recent benchmark run for the agent, or nil if none.
	GetLatestRun(ctx context.Context, agentID string) (*BenchmarkRun, error)

	// ListAgents returns the distinct agent IDs that have at least one run.
	ListAgents(ctx context.Context) ([]string, error)

	// GetVerdictTrend returns the last N weekly verdicts for the given agent,
	// ordered oldest first. Returns an empty slice if no runs exist.
	GetVerdictTrend(ctx context.Context, agentID string, weeks int) ([]string, error)

	// Close releases all resources held by the store.
	Close() error
}

// MetadataFromJSON deserializes a JSON string into a metadata map.
// Returns nil if the input is empty or invalid JSON.
func MetadataFromJSON(raw string) map[string]interface{} {
	if raw == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// MetadataToJSON serializes a metadata map to a JSON string.
// Returns empty string if the map is nil or serialization fails.
func MetadataToJSON(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
