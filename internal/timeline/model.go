package timeline

import "time"

type ConversationStatus string

type TimelineKind string

type HandoffStatus string

type AckState string

type Priority string

const (
	ConversationStatusActive   ConversationStatus = "active"
	ConversationStatusClosed   ConversationStatus = "closed"
	ConversationStatusArchived ConversationStatus = "archived"

	KindMessage TimelineKind = "message"
	KindHandoff TimelineKind = "handoff"
	KindAck     TimelineKind = "ack"

	HandoffStatusPending   HandoffStatus = "pending"
	HandoffStatusAcked     HandoffStatus = "acked"
	HandoffStatusCompleted HandoffStatus = "completed"
	HandoffStatusCancelled HandoffStatus = "cancelled"

	AckStateAccepted   AckState = "accepted"
	AckStateRejected   AckState = "rejected"
	AckStateInProgress AckState = "in_progress"
	AckStateCompleted  AckState = "completed"

	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

type Conversation struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Kind      string             `json:"kind"`
	Status    ConversationStatus `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Metadata  map[string]any     `json:"metadata,omitempty"`
}

type ConversationSummary struct {
	ID             string             `json:"id"`
	Title          string             `json:"title"`
	Kind           string             `json:"kind"`
	Status         ConversationStatus `json:"status"`
	LastActivityAt time.Time          `json:"last_activity_at"`
}

type TraceRef struct {
	ID       string         `json:"id,omitempty"`
	Kind     string         `json:"kind"`
	Ref      string         `json:"ref"`
	URI      string         `json:"uri,omitempty"`
	Title    string         `json:"title,omitempty"`
	Checksum string         `json:"checksum,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Message struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	ThreadID       string         `json:"thread_id,omitempty"`
	AgentID        string         `json:"agent_id"`
	PeerAgentID    string         `json:"peer_agent_id,omitempty"`
	Topic          string         `json:"topic,omitempty"`
	Body           string         `json:"body"`
	Summary        string         `json:"summary,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	TraceRefs      []TraceRef     `json:"trace_refs,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type TaskHandoff struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	ThreadID       string         `json:"thread_id,omitempty"`
	FromAgentID    string         `json:"from_agent_id"`
	ToAgentID      string         `json:"to_agent_id"`
	TaskKey        string         `json:"task_key"`
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	Status         HandoffStatus  `json:"status"`
	Priority       Priority       `json:"priority"`
	TraceMessageID string         `json:"trace_message_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type TaskAck struct {
	ID             string         `json:"id"`
	HandoffID      string         `json:"handoff_id"`
	ConversationID string         `json:"conversation_id"`
	AckAgentID     string         `json:"ack_agent_id"`
	State          AckState       `json:"state"`
	Note           string         `json:"note,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type TimelineItem struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	ThreadID       string         `json:"thread_id,omitempty"`
	Kind           TimelineKind   `json:"kind"`
	AgentID        string         `json:"agent_id"`
	PeerAgentID    string         `json:"peer_agent_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Body           string         `json:"body"`
	Summary        string         `json:"summary,omitempty"`
	Topic          string         `json:"topic,omitempty"`
	TaskKey        string         `json:"task_key,omitempty"`
	HandoffID      string         `json:"handoff_id,omitempty"`
	AckState       AckState       `json:"ack_state,omitempty"`
	Priority       Priority       `json:"priority,omitempty"`
	TraceRefs      []TraceRef     `json:"trace_refs,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type IngestRequest struct {
	Kind           TimelineKind   `json:"kind"`
	ConversationID string         `json:"conversation_id"`
	Conversation   *Conversation  `json:"conversation,omitempty"`
	ThreadID       string         `json:"thread_id,omitempty"`
	AgentID        string         `json:"agent_id,omitempty"`
	PeerAgentID    string         `json:"peer_agent_id,omitempty"`
	Topic          string         `json:"topic,omitempty"`
	Body           string         `json:"body,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	TraceRefs      []TraceRef     `json:"trace_refs,omitempty"`
	FromAgentID    string         `json:"from_agent_id,omitempty"`
	ToAgentID      string         `json:"to_agent_id,omitempty"`
	TaskKey        string         `json:"task_key,omitempty"`
	Title          string         `json:"title,omitempty"`
	Priority       Priority       `json:"priority,omitempty"`
	TraceMessageID string         `json:"trace_message_id,omitempty"`
	HandoffID      string         `json:"handoff_id,omitempty"`
	AckAgentID     string         `json:"ack_agent_id,omitempty"`
	State          AckState       `json:"state,omitempty"`
	Note           string         `json:"note,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type MessageQuery struct {
	ConversationID string
	ThreadID       string
	AgentID        string
	Limit          int
	Before         time.Time
}

type TimelineQuery struct {
	ConversationID string
	Kind           TimelineKind
	Limit          int
	Before         time.Time
}

type TimelineEvent struct {
	ConversationID string       `json:"conversation_id"`
	Item           TimelineItem `json:"item"`
}

type StreamSnapshot struct {
	ConversationID string         `json:"conversation_id"`
	Items          []TimelineItem `json:"items"`
}
