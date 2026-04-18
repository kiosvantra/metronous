package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/timeline"
)

func newTimelineStore(t *testing.T) *sqlite.TimelineStore {
	t.Helper()
	store, err := sqlite.NewTimelineStore(":memory:")
	if err != nil {
		t.Fatalf("NewTimelineStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestApplyTimelineMigrations(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := sqlite.ApplyTimelineMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyTimelineMigrations: %v", err)
	}
	for _, table := range []string{"conversations", "timeline_messages", "timeline_trace_refs", "task_handoffs", "task_acks"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	if err := sqlite.ApplyTimelineMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyTimelineMigrations second run: %v", err)
	}
}

func TestTimelineStorePersistsMessagesHandoffsAndAcks(t *testing.T) {
	ctx := context.Background()
	store := newTimelineStore(t)
	createdAt := time.Date(2026, 4, 18, 19, 30, 0, 0, time.UTC)
	if err := store.EnsureConversation(ctx, timeline.Conversation{ID: "conv-1", Title: "IGRIS ↔ BERU", Kind: "igris_beru", Status: timeline.ConversationStatusActive, CreatedAt: createdAt, UpdatedAt: createdAt}); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	msg, err := store.InsertMessage(ctx, timeline.Message{
		ConversationID: "conv-1",
		ThreadID:       "thread-1",
		AgentID:        "IGRIS",
		PeerAgentID:    "BERU",
		Topic:          "plan",
		Body:           "Need a vertical slice.",
		Summary:        "Kickoff",
		SessionID:      "sess-1",
		Model:          "claude-sonnet-4-6",
		CreatedAt:      createdAt,
		TraceRefs:      []timeline.TraceRef{{Kind: "report", Ref: "rpt-1", Title: "Plan"}},
		Metadata:       map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	handoff, err := store.InsertHandoff(ctx, timeline.TaskHandoff{
		ConversationID: "conv-1",
		ThreadID:       "thread-1",
		FromAgentID:    "IGRIS",
		ToAgentID:      "BERU",
		TaskKey:        "portal-mvp",
		Title:          "Build portal",
		Body:           "Add timeline, persistence, realtime.",
		Priority:       timeline.PriorityHigh,
		CreatedAt:      createdAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	if _, err := store.InsertAck(ctx, timeline.TaskAck{
		ConversationID: "conv-1",
		HandoffID:      handoff.ID,
		AckAgentID:     "BERU",
		State:          timeline.AckStateAccepted,
		Note:           "On it.",
		CreatedAt:      createdAt.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertAck: %v", err)
	}

	messages, err := store.ListMessages(ctx, timeline.MessageQuery{ConversationID: "conv-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].ID != msg.ID || len(messages[0].TraceRefs) != 1 {
		t.Fatalf("message round-trip mismatch: %+v", messages[0])
	}

	items, err := store.ListTimelineItems(ctx, timeline.TimelineQuery{ConversationID: "conv-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListTimelineItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 timeline items, got %d", len(items))
	}
	if items[0].Kind != timeline.KindMessage || items[1].Kind != timeline.KindHandoff || items[2].Kind != timeline.KindAck {
		t.Fatalf("unexpected timeline ordering: %+v", items)
	}

	conversations, err := store.ListConversations(ctx)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(conversations) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(conversations))
	}
	if !conversations[0].LastActivityAt.Equal(createdAt.Add(2 * time.Minute)) {
		t.Fatalf("last activity mismatch: %s", conversations[0].LastActivityAt)
	}
}

func TestTimelineStoreAckCompletedMarksHandoffCompleted(t *testing.T) {
	ctx := context.Background()
	store := newTimelineStore(t)
	now := time.Now().UTC()
	if err := store.EnsureConversation(ctx, timeline.Conversation{ID: "conv-1", Title: "Test", Kind: "igris_beru", Status: timeline.ConversationStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	handoff, err := store.InsertHandoff(ctx, timeline.TaskHandoff{ConversationID: "conv-1", FromAgentID: "IGRIS", ToAgentID: "BERU", TaskKey: "task-1", Title: "Title", Body: "Body", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertAck(ctx, timeline.TaskAck{ConversationID: "conv-1", HandoffID: handoff.ID, AckAgentID: "BERU", State: timeline.AckStateCompleted, CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListTimelineItems(ctx, timeline.TimelineQuery{ConversationID: "conv-1", Kind: timeline.KindHandoff, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 handoff, got %d", len(items))
	}
	if items[0].Priority != timeline.PriorityNormal {
		t.Fatalf("expected default priority normal, got %s", items[0].Priority)
	}
}

func TestTimelineStoreRejectsAckWithoutExistingHandoff(t *testing.T) {
	ctx := context.Background()
	store := newTimelineStore(t)
	now := time.Now().UTC()
	if err := store.EnsureConversation(ctx, timeline.Conversation{ID: "conv-1", Title: "Test", Kind: "igris_beru", Status: timeline.ConversationStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertAck(ctx, timeline.TaskAck{ConversationID: "conv-1", HandoffID: "missing", AckAgentID: "BERU", State: timeline.AckStateAccepted, CreatedAt: now}); err == nil {
		t.Fatal("expected missing handoff error")
	}
	items, err := store.ListTimelineItems(ctx, timeline.TimelineQuery{ConversationID: "conv-1", Kind: timeline.KindAck, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no ack items, got %d", len(items))
	}
}

func TestTimelineStoreRejectsAckWithWrongConversation(t *testing.T) {
	ctx := context.Background()
	store := newTimelineStore(t)
	now := time.Now().UTC()
	for _, id := range []string{"conv-1", "conv-2"} {
		if err := store.EnsureConversation(ctx, timeline.Conversation{ID: id, Title: "Test", Kind: "igris_beru", Status: timeline.ConversationStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	handoff, err := store.InsertHandoff(ctx, timeline.TaskHandoff{ConversationID: "conv-1", FromAgentID: "IGRIS", ToAgentID: "BERU", TaskKey: "task-1", Title: "Title", Body: "Body", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertAck(ctx, timeline.TaskAck{ConversationID: "conv-2", HandoffID: handoff.ID, AckAgentID: "BERU", State: timeline.AckStateAccepted, CreatedAt: now}); err == nil {
		t.Fatal("expected conversation mismatch error")
	}
	items, err := store.ListTimelineItems(ctx, timeline.TimelineQuery{ConversationID: "conv-2", Kind: timeline.KindAck, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no ack items, got %d", len(items))
	}
}

func TestTimelineStoreWithRealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timeline.db")
	store, err := sqlite.NewTimelineStore(path)
	if err != nil {
		t.Fatalf("NewTimelineStore(real file): %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.EnsureConversation(ctx, timeline.Conversation{ID: "conv-1", Title: "Test", Kind: "igris_beru", Status: timeline.ConversationStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertMessage(ctx, timeline.Message{ConversationID: "conv-1", AgentID: "IGRIS", Body: "hello", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.NewTimelineStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	items, err := store.ListTimelineItems(ctx, timeline.TimelineQuery{ConversationID: "conv-1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after reopen, got %d", len(items))
	}
}
