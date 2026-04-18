package web

import (
	"io/fs"
	"net/http"
)

type Server struct {
	handler http.Handler
}

func NewServer() *Server {
	staticFS, _ := fs.Sub(StaticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()
	mux.HandleFunc("/timeline", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "index.html")
	})
	mux.Handle("/timeline/", http.StripPrefix("/timeline/", fileServer))
	return &Server{handler: mux}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("/timeline", s.handler)
	mux.Handle("/timeline/", s.handler)
}
