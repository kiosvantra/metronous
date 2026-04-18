package timeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kiosvantra/metronous/internal/mcp"
	"go.uber.org/zap"
)

type Handler struct {
	service        *Service
	logger         *zap.Logger
	resolveToken   func() string
	streamLimit    int
	keepAliveEvery time.Duration
}

func NewHandler(service *Service, logger *zap.Logger, resolveToken func() string) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if resolveToken == nil {
		resolveToken = func() string { return "" }
	}
	return &Handler{service: service, logger: logger, resolveToken: resolveToken, streamLimit: 50, keepAliveEvery: 20 * time.Second}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/timeline/ingest", h.handleIngest)
	mux.HandleFunc("/api/timeline/messages", h.handleMessages)
	mux.HandleFunc("/api/timeline/conversations", h.handleConversations)
	mux.HandleFunc("/api/timeline/items", h.handleItems)
	mux.HandleFunc("/api/timeline/stream", h.handleStream)
}

func (h *Handler) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := mcp.AuthenticateHTTPRequest(w, r, h.resolveToken(), h.logger); err != nil {
		return
	}
	var req IngestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	item, err := h.service.Ingest(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": item.ID, "kind": item.Kind})
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := MessageQuery{
		ConversationID: r.URL.Query().Get("conversation_id"),
		ThreadID:       r.URL.Query().Get("thread_id"),
		AgentID:        r.URL.Query().Get("agent_id"),
		Limit:          parseLimit(r, 50),
		Before:         parseTime(r.URL.Query().Get("before")),
	}
	items, err := h.service.ListMessages(r.Context(), query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := h.service.ListConversations(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var kind TimelineKind
	if rawKind := r.URL.Query().Get("kind"); rawKind != "" {
		kind = TimelineKind(rawKind)
	}
	items, err := h.service.ListItems(r.Context(), TimelineQuery{
		ConversationID: r.URL.Query().Get("conversation_id"),
		Kind:           kind,
		Limit:          parseLimit(r, 50),
		Before:         parseTime(r.URL.Query().Get("before")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID == "" {
		http.Error(w, "conversation_id is required", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	snapshot, err := h.service.Snapshot(r.Context(), conversationID, h.streamLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeSSE(w, "snapshot", snapshot); err != nil {
		return
	}
	flusher.Flush()

	ctx := r.Context()
	events, unsubscribe := h.service.Broker().Subscribe(conversationID, 16)
	defer unsubscribe()
	keepAlive := time.NewTicker(h.keepAliveEvery)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			if err := writeSSE(w, "keepalive", map[string]any{"conversation_id": conversationID}); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, "timeline_item", event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseLimit(r *http.Request, fallback int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func writeSSE(w http.ResponseWriter, event string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

func (h *Handler) StreamContext(r *http.Request) context.Context { return r.Context() }
