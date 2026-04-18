package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kiosvantra/metronous/internal/timeline"
)

const timelineSchema = `
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    metadata TEXT
);
CREATE INDEX IF NOT EXISTS idx_conversations_updated_at ON conversations(updated_at DESC);

CREATE TABLE IF NOT EXISTS timeline_messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    thread_id TEXT,
    agent_id TEXT NOT NULL,
    peer_agent_id TEXT,
    topic TEXT,
    body TEXT NOT NULL,
    summary TEXT,
    session_id TEXT,
    model TEXT,
    created_at INTEGER NOT NULL,
    metadata TEXT
);
CREATE INDEX IF NOT EXISTS idx_timeline_messages_conversation_created_at ON timeline_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_timeline_messages_thread_created_at ON timeline_messages(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_timeline_messages_agent_created_at ON timeline_messages(agent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS timeline_trace_refs (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    ref TEXT NOT NULL,
    uri TEXT,
    title TEXT,
    checksum TEXT,
    metadata TEXT
);
CREATE INDEX IF NOT EXISTS idx_timeline_trace_refs_message_id ON timeline_trace_refs(message_id);

CREATE TABLE IF NOT EXISTS task_handoffs (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    thread_id TEXT,
    from_agent_id TEXT NOT NULL,
    to_agent_id TEXT NOT NULL,
    task_key TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL,
    priority TEXT NOT NULL,
    trace_message_id TEXT,
    created_at INTEGER NOT NULL,
    metadata TEXT
);
CREATE INDEX IF NOT EXISTS idx_task_handoffs_conversation_created_at ON task_handoffs(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_handoffs_to_agent_status_created_at ON task_handoffs(to_agent_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS task_acks (
    id TEXT PRIMARY KEY,
    handoff_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    ack_agent_id TEXT NOT NULL,
    state TEXT NOT NULL,
    note TEXT,
    created_at INTEGER NOT NULL,
    metadata TEXT
);
CREATE INDEX IF NOT EXISTS idx_task_acks_handoff_created_at ON task_acks(handoff_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_acks_conversation_created_at ON task_acks(conversation_id, created_at DESC);
`

type TimelineStore struct {
	writeDB *sql.DB
	readDB  *sql.DB
	path    string
}

func NewTimelineStore(path string) (*TimelineStore, error) {
	writeDB, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open write connection: %w", err)
	}
	if err := ApplyTimelineMigrations(context.Background(), writeDB); err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	var readDB *sql.DB
	if path == ":memory:" {
		readDB = writeDB
	} else {
		readDB, err = openReadDB(path)
		if err != nil {
			_ = writeDB.Close()
			return nil, fmt.Errorf("open read connection: %w", err)
		}
	}
	return &TimelineStore{writeDB: writeDB, readDB: readDB, path: path}, nil
}

func ApplyTimelineMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, timelineSchema); err != nil {
		return fmt.Errorf("apply timeline schema: %w", err)
	}
	return nil
}

func (s *TimelineStore) EnsureConversation(ctx context.Context, conversation timeline.Conversation) error {
	metadata := nullableString(mustJSON(conversation.Metadata))
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO conversations (id, title, kind, status, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			kind = excluded.kind,
			status = excluded.status,
			updated_at = MAX(conversations.updated_at, excluded.updated_at),
			metadata = COALESCE(excluded.metadata, conversations.metadata)
	`, conversation.ID, conversation.Title, conversation.Kind, conversation.Status, conversation.CreatedAt.UTC().UnixMilli(), conversation.UpdatedAt.UTC().UnixMilli(), metadata)
	if err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}
	return nil
}

func (s *TimelineStore) InsertMessage(ctx context.Context, msg timeline.Message) (timeline.Message, error) {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return timeline.Message{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO timeline_messages (id, conversation_id, thread_id, agent_id, peer_agent_id, topic, body, summary, session_id, model, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, msg.ID, msg.ConversationID, nullableString(msg.ThreadID), msg.AgentID, nullableString(msg.PeerAgentID), nullableString(msg.Topic), msg.Body, nullableString(msg.Summary), nullableString(msg.SessionID), nullableString(msg.Model), msg.CreatedAt.UTC().UnixMilli(), nullableString(mustJSON(msg.Metadata)))
	if err != nil {
		_ = tx.Rollback()
		return timeline.Message{}, fmt.Errorf("insert message: %w", err)
	}
	for i := range msg.TraceRefs {
		if msg.TraceRefs[i].ID == "" {
			msg.TraceRefs[i].ID = uuid.NewString()
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO timeline_trace_refs (id, message_id, kind, ref, uri, title, checksum, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, msg.TraceRefs[i].ID, msg.ID, msg.TraceRefs[i].Kind, msg.TraceRefs[i].Ref, nullableString(msg.TraceRefs[i].URI), nullableString(msg.TraceRefs[i].Title), nullableString(msg.TraceRefs[i].Checksum), nullableString(mustJSON(msg.TraceRefs[i].Metadata)))
		if err != nil {
			_ = tx.Rollback()
			return timeline.Message{}, fmt.Errorf("insert trace ref: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at = MAX(updated_at, ?) WHERE id = ?`, msg.CreatedAt.UTC().UnixMilli(), msg.ConversationID); err != nil {
		_ = tx.Rollback()
		return timeline.Message{}, fmt.Errorf("touch conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return timeline.Message{}, fmt.Errorf("commit message: %w", err)
	}
	return msg, nil
}

func (s *TimelineStore) InsertHandoff(ctx context.Context, handoff timeline.TaskHandoff) (timeline.TaskHandoff, error) {
	if handoff.ID == "" {
		handoff.ID = uuid.NewString()
	}
	if handoff.CreatedAt.IsZero() {
		handoff.CreatedAt = time.Now().UTC()
	}
	if handoff.Status == "" {
		handoff.Status = timeline.HandoffStatusPending
	}
	if handoff.Priority == "" {
		handoff.Priority = timeline.PriorityNormal
	}
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO task_handoffs (id, conversation_id, thread_id, from_agent_id, to_agent_id, task_key, title, body, status, priority, trace_message_id, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, handoff.ID, handoff.ConversationID, nullableString(handoff.ThreadID), handoff.FromAgentID, handoff.ToAgentID, handoff.TaskKey, handoff.Title, handoff.Body, handoff.Status, handoff.Priority, nullableString(handoff.TraceMessageID), handoff.CreatedAt.UTC().UnixMilli(), nullableString(mustJSON(handoff.Metadata)))
	if err != nil {
		return timeline.TaskHandoff{}, fmt.Errorf("insert handoff: %w", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, `UPDATE conversations SET updated_at = MAX(updated_at, ?) WHERE id = ?`, handoff.CreatedAt.UTC().UnixMilli(), handoff.ConversationID); err != nil {
		return timeline.TaskHandoff{}, fmt.Errorf("touch conversation: %w", err)
	}
	return handoff, nil
}

func (s *TimelineStore) InsertAck(ctx context.Context, ack timeline.TaskAck) (timeline.TaskAck, error) {
	if ack.ID == "" {
		ack.ID = uuid.NewString()
	}
	if ack.CreatedAt.IsZero() {
		ack.CreatedAt = time.Now().UTC()
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return timeline.TaskAck{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO task_acks (id, handoff_id, conversation_id, ack_agent_id, state, note, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, ack.ID, ack.HandoffID, ack.ConversationID, ack.AckAgentID, ack.State, nullableString(ack.Note), ack.CreatedAt.UTC().UnixMilli(), nullableString(mustJSON(ack.Metadata)))
	if err != nil {
		_ = tx.Rollback()
		return timeline.TaskAck{}, fmt.Errorf("insert ack: %w", err)
	}
	status := timeline.HandoffStatusPending
	switch ack.State {
	case timeline.AckStateAccepted, timeline.AckStateInProgress:
		status = timeline.HandoffStatusAcked
	case timeline.AckStateCompleted:
		status = timeline.HandoffStatusCompleted
	case timeline.AckStateRejected:
		status = timeline.HandoffStatusPending
	}
	if _, err = tx.ExecContext(ctx, `UPDATE task_handoffs SET status = ? WHERE id = ?`, status, ack.HandoffID); err != nil {
		_ = tx.Rollback()
		return timeline.TaskAck{}, fmt.Errorf("update handoff status: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at = MAX(updated_at, ?) WHERE id = ?`, ack.CreatedAt.UTC().UnixMilli(), ack.ConversationID); err != nil {
		_ = tx.Rollback()
		return timeline.TaskAck{}, fmt.Errorf("touch conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return timeline.TaskAck{}, fmt.Errorf("commit ack: %w", err)
	}
	return ack, nil
}

func (s *TimelineStore) ListConversations(ctx context.Context) ([]timeline.ConversationSummary, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id, title, kind, status, updated_at
		FROM conversations
		ORDER BY updated_at DESC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []timeline.ConversationSummary
	for rows.Next() {
		var item timeline.ConversationSummary
		var updatedAt int64
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.Status, &updatedAt); err != nil {
			return nil, err
		}
		item.LastActivityAt = time.UnixMilli(updatedAt).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *TimelineStore) ListMessages(ctx context.Context, query timeline.MessageQuery) ([]timeline.Message, error) {
	conditions := []string{"conversation_id = ?"}
	args := []any{query.ConversationID}
	if query.ThreadID != "" {
		conditions = append(conditions, "thread_id = ?")
		args = append(args, query.ThreadID)
	}
	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}
	if !query.Before.IsZero() {
		conditions = append(conditions, "created_at < ?")
		args = append(args, query.Before.UTC().UnixMilli())
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, conversation_id, thread_id, agent_id, peer_agent_id, topic, body, summary, session_id, model, created_at, metadata FROM timeline_messages WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []timeline.Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		refs, err := s.listTraceRefs(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].TraceRefs = refs
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *TimelineStore) ListTimelineItems(ctx context.Context, query timeline.TimelineQuery) ([]timeline.TimelineItem, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	var merged []timeline.TimelineItem
	if query.Kind == "" || query.Kind == timeline.KindMessage {
		messages, err := s.ListMessages(ctx, timeline.MessageQuery{ConversationID: query.ConversationID, Limit: limit, Before: query.Before})
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			merged = append(merged, timeline.TimelineItem{ID: msg.ID, ConversationID: msg.ConversationID, ThreadID: msg.ThreadID, Kind: timeline.KindMessage, AgentID: msg.AgentID, PeerAgentID: msg.PeerAgentID, CreatedAt: msg.CreatedAt, Body: msg.Body, Summary: msg.Summary, Topic: msg.Topic, TraceRefs: msg.TraceRefs, Metadata: msg.Metadata})
		}
	}
	if query.Kind == "" || query.Kind == timeline.KindHandoff {
		handoffs, err := s.listHandoffItems(ctx, query.ConversationID, query.Before, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, handoffs...)
	}
	if query.Kind == "" || query.Kind == timeline.KindAck {
		acks, err := s.listAckItems(ctx, query.ConversationID, query.Before, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, acks...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	if len(merged) > limit {
		merged = merged[len(merged)-limit:]
	}
	return merged, nil
}

func (s *TimelineStore) listHandoffItems(ctx context.Context, conversationID string, before time.Time, limit int) ([]timeline.TimelineItem, error) {
	q := `SELECT id, conversation_id, thread_id, from_agent_id, to_agent_id, task_key, title, body, priority, created_at, metadata FROM task_handoffs WHERE conversation_id = ?`
	args := []any{conversationID}
	if !before.IsZero() {
		q += ` AND created_at < ?`
		args = append(args, before.UTC().UnixMilli())
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []timeline.TimelineItem
	for rows.Next() {
		var item timeline.TimelineItem
		var threadID, metadata sql.NullString
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.ConversationID, &threadID, &item.AgentID, &item.PeerAgentID, &item.TaskKey, &item.Summary, &item.Body, &item.Priority, &createdAt, &metadata); err != nil {
			return nil, err
		}
		item.Kind = timeline.KindHandoff
		item.HandoffID = item.ID
		item.ThreadID = threadID.String
		item.CreatedAt = time.UnixMilli(createdAt).UTC()
		item.Metadata = parseJSONMap(metadata.String)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *TimelineStore) listAckItems(ctx context.Context, conversationID string, before time.Time, limit int) ([]timeline.TimelineItem, error) {
	q := `SELECT id, conversation_id, handoff_id, ack_agent_id, state, note, created_at, metadata FROM task_acks WHERE conversation_id = ?`
	args := []any{conversationID}
	if !before.IsZero() {
		q += ` AND created_at < ?`
		args = append(args, before.UTC().UnixMilli())
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []timeline.TimelineItem
	for rows.Next() {
		var item timeline.TimelineItem
		var note, metadata sql.NullString
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.HandoffID, &item.AgentID, &item.AckState, &note, &createdAt, &metadata); err != nil {
			return nil, err
		}
		item.Kind = timeline.KindAck
		item.Body = note.String
		item.CreatedAt = time.UnixMilli(createdAt).UTC()
		item.Metadata = parseJSONMap(metadata.String)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *TimelineStore) listTraceRefs(ctx context.Context, messageID string) ([]timeline.TraceRef, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id, kind, ref, uri, title, checksum, metadata FROM timeline_trace_refs WHERE message_id = ? ORDER BY rowid ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []timeline.TraceRef
	for rows.Next() {
		var item timeline.TraceRef
		var uri, title, checksum, metadata sql.NullString
		if err := rows.Scan(&item.ID, &item.Kind, &item.Ref, &uri, &title, &checksum, &metadata); err != nil {
			return nil, err
		}
		item.URI = uri.String
		item.Title = title.String
		item.Checksum = checksum.String
		item.Metadata = parseJSONMap(metadata.String)
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanMessage(scanner interface{ Scan(dest ...any) error }) (timeline.Message, error) {
	var msg timeline.Message
	var threadID, peerAgentID, topic, summary, sessionID, model, metadata sql.NullString
	var createdAt int64
	if err := scanner.Scan(&msg.ID, &msg.ConversationID, &threadID, &msg.AgentID, &peerAgentID, &topic, &msg.Body, &summary, &sessionID, &model, &createdAt, &metadata); err != nil {
		return timeline.Message{}, err
	}
	msg.ThreadID = threadID.String
	msg.PeerAgentID = peerAgentID.String
	msg.Topic = topic.String
	msg.Summary = summary.String
	msg.SessionID = sessionID.String
	msg.Model = model.String
	msg.CreatedAt = time.UnixMilli(createdAt).UTC()
	msg.Metadata = parseJSONMap(metadata.String)
	return msg, nil
}

func mustJSON(value map[string]any) string {
	if len(value) == 0 {
		return ""
	}
	b, _ := json.Marshal(value)
	return string(b)
}

func parseJSONMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{"_raw": raw}
	}
	return out
}

func (s *TimelineStore) Close() error {
	if s.readDB != nil && s.readDB != s.writeDB {
		if err := s.readDB.Close(); err != nil {
			_ = s.writeDB.Close()
			return err
		}
	}
	if s.writeDB != nil {
		return s.writeDB.Close()
	}
	return nil
}

func (s *TimelineStore) Checkpoint() error {
	_, err := s.writeDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)
	return err
}
