package web

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/kiosvantra/metronous/internal/runner"
	"github.com/kiosvantra/metronous/internal/store"
)

// corsMiddleware adds permissive CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewHandler builds the dashboard HTTP handler without starting a listener.
// Use this to embed the dashboard into another server (e.g. the daemon).
// The runner parameter is optional — pass nil to disable on-demand benchmark runs.
func NewHandler(bs store.BenchmarkStore, es store.EventStore, r *runner.Runner, workDir string) (http.Handler, error) {
	mux := http.NewServeMux()

	// Serve embedded index.html at root.
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("embed sub-fs: %w", err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	// API routes.
	mux.HandleFunc("GET /api/overview", handleOverview(bs, workDir))
	mux.HandleFunc("GET /api/compare", handleCompare(bs, workDir))
	mux.HandleFunc("GET /api/trend", handleTrend(bs))

	// Benchmark on-demand.
	mux.HandleFunc("POST /api/benchmark/run", handleBenchmarkRun(r))

	// Tracking routes.
	mux.Handle("GET /api/sessions", corsMiddleware(handleSessions(es)))
	mux.Handle("GET /api/sessions/events", corsMiddleware(handleSessionEvents(es)))

	return corsMiddleware(mux), nil
}

// StartServer registers all routes and blocks on ListenAndServe.
// The runner parameter is optional — pass nil to disable on-demand benchmark runs.
func StartServer(bs store.BenchmarkStore, es store.EventStore, r *runner.Runner, workDir string, port int) error {
	handler, err := NewHandler(bs, es, r, workDir)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Dashboard available at http://localhost%s\n", addr)

	return http.ListenAndServe(addr, handler)
}
