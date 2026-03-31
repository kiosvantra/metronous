package web

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/kiosvantra/metronous/internal/store"
)

// corsMiddleware adds permissive CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// StartServer registers all routes and blocks on ListenAndServe.
func StartServer(bs store.BenchmarkStore, es store.EventStore, workDir string, port int) error {
	mux := http.NewServeMux()

	// Serve embedded index.html at root.
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("embed sub-fs: %w", err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	// API routes.
	mux.HandleFunc("GET /api/overview", handleOverview(bs, workDir))
	mux.HandleFunc("GET /api/compare", handleCompare(bs, workDir))
	mux.HandleFunc("GET /api/trend", handleTrend(bs))

	// Tracking routes.
	mux.Handle("GET /api/sessions", corsMiddleware(handleSessions(es)))
	mux.Handle("GET /api/sessions/events", corsMiddleware(handleSessionEvents(es)))

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Dashboard available at http://localhost%s\n", addr)

	return http.ListenAndServe(addr, corsMiddleware(mux))
}
