package timeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store interface {
	EnsureConversation(ctx context.Context, conversation Conversation) error
	InsertMessage(ctx context.Context, msg Message) (Message, error)
	InsertHandoff(ctx context.Context, handoff TaskHandoff) (TaskHandoff, error)
	InsertAck(ctx context.Context, ack TaskAck) (TaskAck, error)
	GetHandoff(ctx context.Context, handoffID string) (TaskHandoff, error)
	ListConversations(ctx context.Context) ([]ConversationSummary, error)
	ListMessages(ctx context.Context, query MessageQuery) ([]Message, error)
	ListTimelineItems(ctx context.Context, query TimelineQuery) ([]TimelineItem, error)
}

type brokerSubscriber struct {
	mu     sync.Mutex
	ch     chan TimelineEvent
	closed bool
}

type Broker struct {
	mu   sync.RWMutex
	subs map[string]map[*brokerSubscriber]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[string]map[*brokerSubscriber]struct{})}
}

func (b *Broker) Subscribe(conversationID string, buffer int) (<-chan TimelineEvent, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	sub := &brokerSubscriber{ch: make(chan TimelineEvent, buffer)}
	b.mu.Lock()
	if b.subs[conversationID] == nil {
		b.subs[conversationID] = make(map[*brokerSubscriber]struct{})
	}
	b.subs[conversationID][sub] = struct{}{}
	b.mu.Unlock()
	return sub.ch, func() {
		b.mu.Lock()
		if subs := b.subs[conversationID]; subs != nil {
			delete(subs, sub)
			if len(subs) == 0 {
				delete(b.subs, conversationID)
			}
		}
		b.mu.Unlock()

		sub.mu.Lock()
		defer sub.mu.Unlock()
		if sub.closed {
			return
		}
		sub.closed = true
		close(sub.ch)
	}
}

func (b *Broker) Publish(event TimelineEvent) {
	b.mu.RLock()
	subs := b.subs[event.ConversationID]
	targets := make([]*brokerSubscriber, 0, len(subs))
	for sub := range subs {
		targets = append(targets, sub)
	}
	b.mu.RUnlock()
	for _, sub := range targets {
		sub.mu.Lock()
		if sub.closed {
			sub.mu.Unlock()
			continue
		}
		select {
		case sub.ch <- event:
		default:
		}
		sub.mu.Unlock()
	}
}

type Service struct {
	store  Store
	broker *Broker
}

func NewService(store Store, broker *Broker) *Service {
	if broker == nil {
		broker = NewBroker()
	}
	return &Service{store: store, broker: broker}
}

func (s *Service) Broker() *Broker { return s.broker }

func (s *Service) ListConversations(ctx context.Context) ([]ConversationSummary, error) {
	return s.store.ListConversations(ctx)
}

func (s *Service) ListMessages(ctx context.Context, query MessageQuery) ([]Message, error) {
	if strings.TrimSpace(query.ConversationID) == "" {
		return nil, errors.New("conversation_id is required")
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	return s.store.ListMessages(ctx, query)
}

func (s *Service) ListItems(ctx context.Context, query TimelineQuery) ([]TimelineItem, error) {
	if strings.TrimSpace(query.ConversationID) == "" {
		return nil, errors.New("conversation_id is required")
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	return s.store.ListTimelineItems(ctx, query)
}

func (s *Service) Ingest(ctx context.Context, req IngestRequest) (TimelineItem, error) {
	if strings.TrimSpace(req.ConversationID) == "" {
		return TimelineItem{}, errors.New("conversation_id is required")
	}
	createdAt := req.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	conversation := deriveConversation(req, createdAt)
	if err := s.store.EnsureConversation(ctx, conversation); err != nil {
		return TimelineItem{}, fmt.Errorf("ensure conversation: %w", err)
	}

	var item TimelineItem
	var err error
	switch req.Kind {
	case KindMessage:
		item, err = s.ingestMessage(ctx, req, createdAt)
	case KindHandoff:
		item, err = s.ingestHandoff(ctx, req, createdAt)
	case KindAck:
		item, err = s.ingestAck(ctx, req, createdAt)
	default:
		return TimelineItem{}, fmt.Errorf("unsupported kind %q", req.Kind)
	}
	if err != nil {
		return TimelineItem{}, err
	}
	s.broker.Publish(TimelineEvent{ConversationID: req.ConversationID, Item: item})
	return item, nil
}

func (s *Service) Snapshot(ctx context.Context, conversationID string, limit int) (StreamSnapshot, error) {
	items, err := s.ListItems(ctx, TimelineQuery{ConversationID: conversationID, Limit: limit})
	if err != nil {
		return StreamSnapshot{}, err
	}
	return StreamSnapshot{ConversationID: conversationID, Items: items}, nil
}

func (s *Service) ingestMessage(ctx context.Context, req IngestRequest, createdAt time.Time) (TimelineItem, error) {
	if strings.TrimSpace(req.AgentID) == "" {
		return TimelineItem{}, errors.New("agent_id is required for message")
	}
	if strings.TrimSpace(req.Body) == "" {
		return TimelineItem{}, errors.New("body is required for message")
	}
	msg, err := s.store.InsertMessage(ctx, Message{
		ConversationID: req.ConversationID,
		ThreadID:       req.ThreadID,
		AgentID:        req.AgentID,
		PeerAgentID:    req.PeerAgentID,
		Topic:          req.Topic,
		Body:           req.Body,
		Summary:        req.Summary,
		SessionID:      req.SessionID,
		Model:          req.Model,
		CreatedAt:      createdAt,
		TraceRefs:      req.TraceRefs,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return TimelineItem{}, fmt.Errorf("insert message: %w", err)
	}
	return TimelineItem{
		ID:             msg.ID,
		ConversationID: msg.ConversationID,
		ThreadID:       msg.ThreadID,
		Kind:           KindMessage,
		AgentID:        msg.AgentID,
		PeerAgentID:    msg.PeerAgentID,
		CreatedAt:      msg.CreatedAt,
		Body:           msg.Body,
		Summary:        msg.Summary,
		Topic:          msg.Topic,
		TraceRefs:      msg.TraceRefs,
		Metadata:       msg.Metadata,
	}, nil
}

func (s *Service) ingestHandoff(ctx context.Context, req IngestRequest, createdAt time.Time) (TimelineItem, error) {
	if strings.TrimSpace(req.FromAgentID) == "" || strings.TrimSpace(req.ToAgentID) == "" {
		return TimelineItem{}, errors.New("from_agent_id and to_agent_id are required for handoff")
	}
	if strings.TrimSpace(req.TaskKey) == "" || strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		return TimelineItem{}, errors.New("task_key, title and body are required for handoff")
	}
	priority := req.Priority
	if priority == "" {
		priority = PriorityNormal
	}
	handoff, err := s.store.InsertHandoff(ctx, TaskHandoff{
		ConversationID: req.ConversationID,
		ThreadID:       req.ThreadID,
		FromAgentID:    req.FromAgentID,
		ToAgentID:      req.ToAgentID,
		TaskKey:        req.TaskKey,
		Title:          req.Title,
		Body:           req.Body,
		Status:         HandoffStatusPending,
		Priority:       priority,
		TraceMessageID: req.TraceMessageID,
		CreatedAt:      createdAt,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return TimelineItem{}, fmt.Errorf("insert handoff: %w", err)
	}
	return TimelineItem{
		ID:             handoff.ID,
		ConversationID: handoff.ConversationID,
		ThreadID:       handoff.ThreadID,
		Kind:           KindHandoff,
		AgentID:        handoff.FromAgentID,
		PeerAgentID:    handoff.ToAgentID,
		CreatedAt:      handoff.CreatedAt,
		Body:           handoff.Body,
		Summary:        handoff.Title,
		TaskKey:        handoff.TaskKey,
		HandoffID:      handoff.ID,
		Priority:       handoff.Priority,
		Metadata:       handoff.Metadata,
	}, nil
}

func (s *Service) ingestAck(ctx context.Context, req IngestRequest, createdAt time.Time) (TimelineItem, error) {
	if strings.TrimSpace(req.HandoffID) == "" || strings.TrimSpace(req.AckAgentID) == "" {
		return TimelineItem{}, errors.New("handoff_id and ack_agent_id are required for ack")
	}
	if req.State == "" {
		return TimelineItem{}, errors.New("state is required for ack")
	}
	handoff, err := s.store.GetHandoff(ctx, req.HandoffID)
	if err != nil {
		return TimelineItem{}, fmt.Errorf("resolve handoff: %w", err)
	}
	if handoff.ConversationID != req.ConversationID {
		return TimelineItem{}, fmt.Errorf("ack conversation_id %q does not match handoff conversation %q", req.ConversationID, handoff.ConversationID)
	}
	ack, err := s.store.InsertAck(ctx, TaskAck{
		ConversationID: req.ConversationID,
		HandoffID:      req.HandoffID,
		AckAgentID:     req.AckAgentID,
		State:          req.State,
		Note:           req.Note,
		CreatedAt:      createdAt,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return TimelineItem{}, fmt.Errorf("insert ack: %w", err)
	}
	return TimelineItem{
		ID:             ack.ID,
		ConversationID: ack.ConversationID,
		Kind:           KindAck,
		AgentID:        ack.AckAgentID,
		CreatedAt:      ack.CreatedAt,
		Body:           ack.Note,
		HandoffID:      ack.HandoffID,
		AckState:       ack.State,
		Metadata:       ack.Metadata,
	}, nil
}

func deriveConversation(req IngestRequest, createdAt time.Time) Conversation {
	conv := Conversation{
		ID:        req.ConversationID,
		Title:     "IGRIS ↔ BERU",
		Kind:      "igris_beru",
		Status:    ConversationStatusActive,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Metadata:  nil,
	}
	if req.Conversation != nil {
		if req.Conversation.Title != "" {
			conv.Title = req.Conversation.Title
		}
		if req.Conversation.Kind != "" {
			conv.Kind = req.Conversation.Kind
		}
		if req.Conversation.Status != "" {
			conv.Status = req.Conversation.Status
		}
		if !req.Conversation.CreatedAt.IsZero() {
			conv.CreatedAt = req.Conversation.CreatedAt.UTC()
		}
		if req.Conversation.Metadata != nil {
			conv.Metadata = req.Conversation.Metadata
		}
	}
	agents := []string{req.AgentID, req.PeerAgentID, req.FromAgentID, req.ToAgentID, req.AckAgentID}
	hasIGRIS := false
	hasBERU := false
	for _, agent := range agents {
		switch strings.ToUpper(strings.TrimSpace(agent)) {
		case "IGRIS":
			hasIGRIS = true
		case "BERU":
			hasBERU = true
		}
	}
	if hasIGRIS && hasBERU {
		conv.Title = "IGRIS ↔ BERU"
		conv.Kind = "igris_beru"
	}
	return conv
}

func MergeItems(items ...[]TimelineItem) []TimelineItem {
	var merged []TimelineItem
	for _, batch := range items {
		merged = append(merged, batch...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged
}
