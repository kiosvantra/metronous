package timeline_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store/sqlite"
	"github.com/kiosvantra/metronous/internal/timeline"
	"go.uber.org/zap"
)

func newTimelineHandler(t *testing.T) (*timeline.Service, *timeline.Handler) {
	t.Helper()
	store, err := sqlite.NewTimelineStore(":memory:")
	if err != nil {
		t.Fatalf("NewTimelineStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := timeline.NewService(store, timeline.NewBroker())
	handler := timeline.NewHandler(service, zap.NewNop(), func() string { return "secret-token" })
	handler.Register(http.NewServeMux())
	return service, handler
}

func TestTimelineHandlerIngestAndList(t *testing.T) {
	service, handler := newTimelineHandler(t)
	_ = service
	mux := http.NewServeMux()
	handler.Register(mux)

	payload := map[string]any{
		"kind":            "message",
		"conversation_id": "conv-1",
		"agent_id":        "IGRIS",
		"peer_agent_id":   "BERU",
		"body":            "Need a portal MVP.",
		"summary":         "Kickoff",
		"trace_refs":      []map[string]any{{"kind": "report", "ref": "brief-1"}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/timeline/ingest", bytes.NewReader(body))
	req.Header.Set("X-Metronous-Auth", "secret-token")
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/timeline/ingest status=%d body=%s", resp.Code, resp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/timeline/items?conversation_id=conv-1", nil)
	listResp := httptest.NewRecorder()
	mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET /api/timeline/items status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	var items []timeline.TimelineItem
	if err := json.Unmarshal(listResp.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(items) != 1 || items[0].Kind != timeline.KindMessage || items[0].AgentID != "IGRIS" {
		t.Fatalf("unexpected items: %+v", items)
	}

	msgReq := httptest.NewRequest(http.MethodGet, "/api/timeline/messages?conversation_id=conv-1", nil)
	msgResp := httptest.NewRecorder()
	mux.ServeHTTP(msgResp, msgReq)
	if msgResp.Code != http.StatusOK {
		t.Fatalf("GET /api/timeline/messages status=%d body=%s", msgResp.Code, msgResp.Body.String())
	}
	var messages []timeline.Message
	if err := json.Unmarshal(msgResp.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].TraceRefs) != 1 {
		t.Fatalf("unexpected messages: %+v", messages)
	}
}

func TestTimelineHandlerRejectsUnauthenticatedIngest(t *testing.T) {
	_, handler := newTimelineHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/timeline/ingest", strings.NewReader(`{"kind":"message","conversation_id":"conv-1","agent_id":"IGRIS","body":"hi"}`))
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestTimelineStreamSendsSnapshotAndLiveEvents(t *testing.T) {
	service, handler := newTimelineHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/timeline/stream?conversation_id=conv-1", nil).WithContext(ctx)
	resp := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(resp, req)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(resp.Body.String(), "event: snapshot") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(resp.Body.String(), "event: snapshot") {
		cancel()
		<-done
		t.Fatalf("missing snapshot event: %s", resp.Body.String())
	}

	_, err := service.Ingest(context.Background(), timeline.IngestRequest{Kind: timeline.KindMessage, ConversationID: "conv-1", AgentID: "IGRIS", Body: "hello", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("service.Ingest: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(resp.Body.String(), "event: timeline_item") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(resp.Body.String(), "event: timeline_item") {
		t.Fatalf("missing timeline event: %s", resp.Body.String())
	}

	scanner := bufio.NewScanner(strings.NewReader(resp.Body.String()))
	var foundItem bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: {") && strings.Contains(line, `"conversation_id":"conv-1"`) {
			foundItem = true
			break
		}
	}
	if !foundItem {
		t.Fatalf("stream body missing conversation payload: %s", resp.Body.String())
	}
}
