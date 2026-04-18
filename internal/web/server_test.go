package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerServesTimelineIndex(t *testing.T) {
	srv := NewServer()
	mux := http.NewServeMux()
	srv.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/timeline", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "IGRIS ↔ BERU Portal") {
		t.Fatalf("unexpected body: %s", resp.Body.String())
	}
}
