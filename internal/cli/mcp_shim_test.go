//go:build linux

package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestShimForwardToolCallSetsAuthHeader verifies that shimForwardToolCall sends
// the configured METRONOUS_INGEST_TOKEN as a transport-level header without
// modifying the ingest payload schema.
func TestShimForwardToolCallSetsAuthHeader(t *testing.T) {
	const expectedToken = "test-token"

	var sawHeader string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		sawHeader = r.Header.Get("X-Metronous-Auth")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok": true}`)
	}))
	defer ts.Close()

	params := struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		Name:      "ingest",
		Arguments: map[string]interface{}{"agent_id": "a1"},
	}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	if _, err := shimForwardToolCall(ts.URL, expectedToken, raw); err != nil {
		t.Fatalf("shimForwardToolCall: %v", err)
	}

	if sawHeader != expectedToken {
		t.Fatalf("X-Metronous-Auth header = %q, want %q", sawHeader, expectedToken)
	}
}

// TestShimForwardToolCallNoAuthHeaderWhenTokenEmpty verifies that when the
// auth token is empty the shim does not send the header, keeping behaviour
// backward compatible.
func TestShimForwardToolCallNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var sawHeader string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("X-Metronous-Auth")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok": true}`)
	}))
	defer ts.Close()

	params := struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		Name:      "ingest",
		Arguments: map[string]interface{}{"agent_id": "a1"},
	}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	if _, err := shimForwardToolCall(ts.URL, "", raw); err != nil {
		t.Fatalf("shimForwardToolCall: %v", err)
	}

	if sawHeader != "" {
		t.Fatalf("X-Metronous-Auth header = %q, want empty", sawHeader)
	}
}
