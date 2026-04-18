package mcp

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kiosvantra/metronous/internal/version"
	"go.uber.org/zap"
)

const (
	// MCPProtocolVersion is the supported MCP spec version.
	MCPProtocolVersion = "2024-11-05"

	// ServerName is the name reported during MCP initialization.
	ServerName = "metronous"

	ingestAuthHeader = "X-Metronous-Auth"
	ingestAuthEnvVar = "METRONOUS_INGEST_TOKEN"
)

// ServerVersion matches the CLI version.
var ServerVersion = version.Version

// Server is a minimal MCP server that communicates over stdio.
// It handles JSON-RPC 2.0 messages and dispatches tool calls to registered handlers.
type Server struct {
	mu       sync.RWMutex // guards tools, handlers, and dataDir
	outMu    sync.Mutex   // guards out — separate from mu to prevent deadlock
	tools    map[string]ToolDefinition
	handlers map[string]ToolHandler
	logger   *zap.Logger
	in       io.Reader
	out      io.Writer
	dataDir  string // used to derive instance-scoped port file path

	// dashboard holds the HTTP handler for the web dashboard.
	// When non-nil, ServeDaemon starts a second listener on dashboardPort.
	dashboard     http.Handler
	dashboardPort int
}

// SetDashboard configures the embedded web dashboard handler and port.
// When set, ServeDaemon will start a second HTTP listener for the browser UI.
func (s *Server) SetDashboard(handler http.Handler, port int) {
	s.dashboard = handler
	s.dashboardPort = port
}

// NewServer creates a new MCP server reading from in and writing to out.
// Typically in=os.Stdin and out=os.Stdout.
func NewServer(in io.Reader, out io.Writer, logger *zap.Logger) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Server{
		tools:    make(map[string]ToolDefinition),
		handlers: make(map[string]ToolHandler),
		logger:   logger,
		in:       in,
		out:      out,
	}
}

// NewStdioServer creates an MCP server connected to os.Stdin / os.Stdout.
func NewStdioServer(logger *zap.Logger) *Server {
	return NewServer(os.Stdin, os.Stdout, logger)
}

// SetDataDir sets the data directory used to derive the instance-scoped port
// file path (e.g. {data-dir}/mcp.port).  Must be called before ServeWithHealth.
func (s *Server) SetDataDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataDir = dir
}

// RegisterTool registers an MCP tool with its definition and handler.
// If a tool with the same name already exists, it is overwritten.
func (s *Server) RegisterTool(def ToolDefinition, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[def.Name] = def
	s.handlers[def.Name] = handler
}

// HasTool reports whether a tool with the given name is registered.
func (s *Server) HasTool(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tools[name]
	return ok
}

// ListTools returns a copy of all registered tool definitions.
func (s *Server) ListTools() []ToolDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]ToolDefinition, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, t)
	}
	return tools
}

// ServeStdio blocks and processes MCP messages from stdin until ctx is cancelled
// or EOF is received on the input stream.
func (s *Server) ServeStdio(ctx context.Context) error {
	newScanner := func() *bufio.Scanner {
		sc := bufio.NewScanner(s.in)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
		return sc
	}
	scanner := newScanner()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				// bufio.ErrTooLong poisons the scanner — it will never return true again.
				// Replace the scanner with a fresh one so we can keep reading, but first
				// send an error response so the client knows the message was rejected.
				if err == bufio.ErrTooLong {
					s.logger.Error("message exceeds maximum size (1MB), replacing scanner", zap.Error(err))
					oversizedResp := newErrorResponse(nil, ErrCodeParseError, "message exceeds maximum size of 1MB")
					if writeErr := s.writeResponse(oversizedResp); writeErr != nil {
						s.logger.Error("failed to write oversized message error response", zap.Error(writeErr))
					}
					// Replace poisoned scanner with a fresh one.
					scanner = newScanner()
					continue
				}
				return fmt.Errorf("read from stdin: %w", err)
			}
			// EOF — client disconnected.
			return nil
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		s.logger.Debug("received message", zap.ByteString("raw", line))

		resp, hasResponse := s.handleMessage(ctx, line)
		if !hasResponse {
			continue
		}

		if err := s.writeResponse(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
}

// handleMessage decodes a raw JSON-RPC message and dispatches it.
// Returns (response, true) if a response should be sent, or (_, false) for notifications.
func (s *Server) handleMessage(ctx context.Context, raw []byte) (Response, bool) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return newErrorResponse(nil, ErrCodeParseError, "parse error: "+err.Error()), true
	}

	// Notifications have no ID; don't send a response.
	if req.ID == nil && req.Method != "" {
		s.handleNotification(req)
		return Response{}, false
	}

	if req.JSONRPC != "2.0" {
		return newErrorResponse(req.ID, ErrCodeInvalidRequest, "invalid jsonrpc version"), true
	}

	return s.dispatch(ctx, req), true
}

// handleNotification processes notifications (fire-and-forget, no response).
func (s *Server) handleNotification(req Request) {
	s.logger.Debug("received notification", zap.String("method", req.Method))
	// Most MCP lifecycle notifications (initialized, cancelled, etc.) can be ignored for MVP.
}

// dispatch routes a JSON-RPC request to the appropriate MCP method handler.
func (s *Server) dispatch(ctx context.Context, req Request) Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		// Should not reach here (notifications have no ID) but handle gracefully.
		return newSuccessResponse(req.ID, nil)
	case "ping":
		return newSuccessResponse(req.ID, map[string]interface{}{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		s.logger.Warn("method not found", zap.String("method", req.Method))
		return newErrorResponse(req.ID, ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleInitialize responds to the MCP initialization handshake.
func (s *Server) handleInitialize(req Request) Response {
	result := InitializeResult{
		ProtocolVersion: MCPProtocolVersion,
		Capabilities: Capability{
			Tools: &ToolsCapability{ListChanged: false},
		},
		ServerInfo: ServerInfo{
			Name:    ServerName,
			Version: ServerVersion,
		},
	}
	return newSuccessResponse(req.ID, result)
}

// handleToolsList returns the list of registered tools.
func (s *Server) handleToolsList(req Request) Response {
	return newSuccessResponse(req.ID, ListToolsResult{Tools: s.ListTools()})
}

// handleToolsCall dispatches a tool/call request to the registered handler.
func (s *Server) handleToolsCall(ctx context.Context, req Request) Response {
	var callReq CallToolRequest
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &callReq); err != nil {
			return newErrorResponse(req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
	}

	s.mu.RLock()
	handler, ok := s.handlers[callReq.Name]
	s.mu.RUnlock()

	if !ok {
		return newErrorResponse(req.ID, ErrCodeMethodNotFound,
			fmt.Sprintf("tool not found: %s", callReq.Name))
	}

	// Add timeout to handler execution (30 seconds max)
	handlerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := handler(handlerCtx, callReq)
	if err != nil {
		return newSuccessResponse(req.ID, &CallToolResult{
			Content: []ContentItem{TextContent("error: " + err.Error())},
			IsError: true,
		})
	}

	return newSuccessResponse(req.ID, result)
}

// writeResponse serializes and writes a response to the output stream.
// MCP stdio uses newline-delimited JSON.
//
// outMu is used (not mu) so that writing a response never deadlocks with
// callers that hold mu for tool registration or handler lookup.
func (s *Server) writeResponse(resp Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	s.outMu.Lock()
	defer s.outMu.Unlock()

	if _, err := fmt.Fprintf(s.out, "%s\n", b); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// ServeWithHealth starts an HTTP health/status server on a dynamic port in the
// background, writes the assigned port to {data-dir}/mcp.port, then serves
// the MCP protocol over stdio (blocking until ctx is cancelled or EOF).
//
// The HTTP endpoint is intentionally minimal — it exists only so that external
// tools can discover that the Metronous server is running.  All MCP data still
// flows through stdio.
func (s *Server) ServeWithHealth(ctx context.Context) error {
	// ── Single-instance enforcement via PID file ───────────────────────────────
	pidPath := s.pidFilePath()
	if err := AcquirePIDFile(pidPath); err != nil {
		return err
	}
	defer func() {
		if removeErr := removePIDFile(pidPath); removeErr != nil {
			s.logger.Warn("could not remove pid file", zap.Error(removeErr))
		}
	}()

	// ── HTTP health server ────────────────────────────────────────────────────

	// Clean up any stale port file left by the previous instance.  The old
	// process was already terminated by acquirePIDFile, so if the port file
	// still exists it is definitely stale.
	portPath := s.portFilePath()
	if _, statErr := os.Stat(portPath); statErr == nil {
		_ = os.Remove(portPath)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start health HTTP server: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	s.logger.Info("health HTTP server listening",
		zap.Int("port", port),
		zap.String("addr", listener.Addr().String()),
	)

	// Persist the port so other processes (e.g. the OpenCode plugin) can find it.
	// portPath was already set above for the stale-file cleanup.
	if portErr := writePortFile(portPath, port); portErr != nil {
		_ = listener.Close()
		return fmt.Errorf("write mcp.port: %w", portErr)
	}

	// Remove port file on ALL exit paths (normal EOF or context cancellation).
	defer func() {
		if removeErr := removePortFile(portPath); removeErr != nil {
			s.logger.Warn("could not remove mcp.port file", zap.Error(removeErr))
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/status", healthHandler) // alias
	mux.HandleFunc("/ingest", s.ingestHandler(ctx))

	httpSrv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		if err := httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Warn("health HTTP server stopped", zap.Error(err))
		}
	}()

	// Shut down the HTTP server when the context is cancelled.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("health HTTP server shutdown error", zap.Error(err))
		}
	}()

	// ── MCP stdio (primary transport) ─────────────────────────────────────────
	return s.ServeStdio(ctx)
}

// ServeDaemon is like ServeWithHealth but does NOT serve stdio. It is intended
// for use when Metronous runs as a long-lived system service (e.g. systemd),
// where stdin is /dev/null and there is no interactive MCP client connected
// directly. Shim processes (metronous mcp) connect to the HTTP endpoint instead.
//
// The function blocks until ctx is cancelled.
func (s *Server) ServeDaemon(outerCtx context.Context) error {
	ctx, cancel := context.WithCancel(outerCtx)
	defer cancel()
	// ── Single-instance enforcement via PID file ───────────────────────────────
	pidPath := s.pidFilePath()
	if err := AcquirePIDFile(pidPath); err != nil {
		return err
	}
	defer func() {
		if removeErr := removePIDFile(pidPath); removeErr != nil {
			s.logger.Warn("could not remove pid file", zap.Error(removeErr))
		}
	}()

	// ── HTTP health server ─────────────────────────────────────────────────────
	portPath := s.portFilePath()
	if _, statErr := os.Stat(portPath); statErr == nil {
		_ = os.Remove(portPath)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start health HTTP server: %w", err)
	}
	// Close listener immediately on any error before returning
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			s.logger.Warn("could not close listener", zap.Error(closeErr))
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	s.logger.Info("daemon HTTP server listening",
		zap.Int("port", port),
		zap.String("addr", listener.Addr().String()),
	)

	if portErr := writePortFile(portPath, port); portErr != nil {
		return fmt.Errorf("write mcp.port: %w", portErr)
	}
	defer func() {
		if removeErr := removePortFile(portPath); removeErr != nil {
			s.logger.Warn("could not remove mcp.port file", zap.Error(removeErr))
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/status", healthHandler)
	// Use background context for ingestHandler to avoid using cancelled ctx during shutdown
	mux.HandleFunc("/ingest", s.ingestHandler(context.Background()))

	httpSrv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	serveDone := make(chan error, 1)
	go func() {
		err := httpSrv.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			s.logger.Warn("daemon HTTP server stopped unexpectedly", zap.Error(err))
			cancel() // unblock ctx.Done() so the shutdown goroutine runs
		}
		serveDone <- err
	}()

	// ── Dashboard HTTP server (fixed port, browser-facing) ────────────────────
	var dashSrv *http.Server
	if s.dashboard != nil && s.dashboardPort > 0 {
		dashAddr := fmt.Sprintf("127.0.0.1:%d", s.dashboardPort)
		dashSrv = &http.Server{
			Addr:         dashAddr,
			Handler:      s.dashboard,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 60 * time.Second, // benchmark run can take time
		}
		go func() {
			s.logger.Info("dashboard available",
				zap.Int("port", s.dashboardPort),
				zap.String("url", fmt.Sprintf("http://localhost:%d", s.dashboardPort)),
			)
			if err := dashSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Warn("dashboard server stopped unexpectedly", zap.Error(err))
			}
		}()
	}

	// Shut down both HTTP servers when the context is cancelled.
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("daemon HTTP server shutdown error", zap.Error(err))
		}
		if dashSrv != nil {
			if err := dashSrv.Shutdown(shutdownCtx); err != nil {
				s.logger.Warn("dashboard server shutdown error", zap.Error(err))
			}
		}
	}()

	// Wait for the Serve goroutine to finish (always reached because cancel() unblocks ctx.Done()).
	if sErr := <-serveDone; sErr != nil && sErr != http.ErrServerClosed {
		return fmt.Errorf("serve HTTP server: %w", sErr)
	}
	return nil
}

// healthResponse is the JSON body returned by /health.
type healthResponse struct {
	Status  string `json:"status"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// healthHandler handles GET /health and GET /status requests.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	body, err := json.Marshal(healthResponse{
		Status:  "ok",
		Name:    ServerName,
		Version: ServerVersion,
	})
	if err != nil {
		http.Error(w, "failed to encode health response", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("metronous: write health response: %v", err)
	}
}

// ingestHandler returns an http.HandlerFunc that accepts POST /ingest requests
// containing a JSON ingest payload and dispatches them to the registered "ingest"
// tool handler.  This allows the OpenCode plugin to send telemetry via HTTP while
// OpenCode itself owns the stdio pipe for the MCP protocol.
//
// Authentication:
//   - If METRONOUS_INGEST_TOKEN is set (env var), requests MUST include the matching
//     token in the X-Metronous-Auth header.
//   - Otherwise, if {dataDir}/ingest.key exists and contains a token, requests MUST
//     include that matching token in the X-Metronous-Auth header.
//   - If neither source provides a token, requests are accepted without authentication
//     (legacy mode).
//
// The header value can be either:
//   - Plain token (backward compatibility): exact match against the configured secret
//   - HMAC signature (recommended): "sha256:<hex-signature>" where signature is
//     HMAC-SHA256(secret, request-body)

// resolveIngestToken reads the authentication token on each request to handle
// lazy key creation by the plugin (ingest.key may not exist at daemon start).
func (s *Server) resolveIngestToken() string {
	expectedToken := os.Getenv(ingestAuthEnvVar)
	if expectedToken != "" {
		return expectedToken
	}

	keyPath := s.ingestKeyPath()
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return ""
	}

	expectedToken = strings.TrimSpace(string(data))
	if expectedToken == "" {
		return ""
	}

	// Validate key format: must be exactly 64 hex characters (32 bytes)
	if len(expectedToken) != 64 {
		s.logger.Warn("ignoring invalid ingest key file",
			zap.String("path", keyPath),
			zap.Int("length", len(expectedToken)))
		return ""
	}
	if _, err := hex.DecodeString(expectedToken); err != nil {
		s.logger.Warn("ignoring invalid ingest key file",
			zap.String("path", keyPath),
			zap.Error(err))
		return ""
	}

	return expectedToken
}

func (s *Server) ingestHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Resolve token per-request to handle lazy key creation by plugin
		expectedToken := s.resolveIngestToken()

		// Authenticate if token is configured
		if expectedToken != "" {
			provided := r.Header.Get(ingestAuthHeader)
			if provided == "" {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}

			// Support both plain token and HMAC signature formats
			valid := false
			var bodyStr string

			if strings.HasPrefix(provided, "sha256:") {
				// HMAC signature mode - read body and verify
				sig := provided[7:] // strip "sha256:" prefix
				// Decode hex signature to raw bytes for comparison
				providedSig, err := hex.DecodeString(sig)
				if err != nil || len(providedSig) != sha256.Size {
					s.logger.Warn("invalid HMAC signature format in ingest request",
						zap.String("remote_addr", r.RemoteAddr))
					http.Error(w, "invalid authentication signature", http.StatusUnauthorized)
					return
				}
				// Read body for HMAC verification
				bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
				if err != nil {
					// Distinguish payload-too-large from other read errors
					if _, ok := err.(*http.MaxBytesError); ok {
						http.Error(w, "payload too large (max 1MB)", http.StatusRequestEntityTooLarge)
					} else {
						http.Error(w, "failed to read request body", http.StatusBadRequest)
					}
					return
				}
				bodyStr = string(bodyBytes)
				// Compute expected HMAC and compare raw bytes
				mac := hmac.New(sha256.New, []byte(expectedToken))
				mac.Write([]byte(bodyStr))
				expectedSig := mac.Sum(nil)
				if hmac.Equal(providedSig, expectedSig) {
					valid = true
				}
				if !valid {
					s.logger.Warn("invalid HMAC signature in ingest request",
						zap.String("remote_addr", r.RemoteAddr))
					http.Error(w, "invalid authentication signature", http.StatusUnauthorized)
					return
				}
				// Re-create a reader from the body for subsequent handlers
				r.Body = io.NopCloser(strings.NewReader(bodyStr))
			} else {
				// Plain token mode (backward compatibility)
				if !hmac.Equal([]byte(provided), []byte(expectedToken)) {
					s.logger.Warn("invalid auth token in ingest request",
						zap.String("remote_addr", r.RemoteAddr))
					http.Error(w, "invalid authentication token", http.StatusUnauthorized)
					return
				}
				valid = true
			}

			if !valid {
				http.Error(w, "authentication failed", http.StatusUnauthorized)
				return
			}
		}

		var arguments map[string]interface{}
		// Limit request body to 1MB to prevent memory exhaustion attacks.
		maxBytes := int64(1024 * 1024)
		if r.ContentLength > maxBytes {
			http.Error(w, "payload too large (max 1MB)", http.StatusRequestEntityTooLarge)
			return
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(&arguments); err != nil {
			if _, ok := err.(*http.MaxBytesError); ok {
				http.Error(w, "payload too large (max 1MB)", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			}
			return
		}

		s.mu.RLock()
		handler, ok := s.handlers["ingest"]
		s.mu.RUnlock()

		if !ok {
			http.Error(w, "ingest tool not registered", http.StatusServiceUnavailable)
			return
		}

		req := CallToolRequest{Name: "ingest", Arguments: arguments}
		handlerCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := handler(handlerCtx, req)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			body, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
			if marshalErr != nil {
				http.Error(w, "failed to encode error response", http.StatusInternalServerError)
				return
			}
			if _, err := w.Write(body); err != nil {
				log.Printf("metronous: write ingest error response: %v", err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		body, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			http.Error(w, "failed to encode ingest response", http.StatusInternalServerError)
			return
		}
		if _, err := w.Write(body); err != nil {
			log.Printf("metronous: write ingest response: %v", err)
		}
	}
}

// portFilePath returns the instance-scoped path to the file that stores the
// dynamic HTTP port.  When dataDir is set it uses {dataDir}/mcp.port so that
// multiple instances (each with their own data-dir) do not overwrite each
// other.  Falls back to ~/.metronous/mcp.port when dataDir is empty.
func (s *Server) portFilePath() string {
	s.mu.RLock()
	dir := s.dataDir
	s.mu.RUnlock()

	if dir != "" {
		return filepath.Join(dir, "mcp.port")
	}
	// Fallback: use the home-directory default.
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/mcp.port"
	}
	return filepath.Join(home, ".metronous", "mcp.port")
}

// dataDirPath returns the data directory path, or the default ~/.metronous/data
// if not set.
func (s *Server) dataDirPath() string {
	s.mu.RLock()
	dir := s.dataDir
	s.mu.RUnlock()

	if dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/data"
	}
	return filepath.Join(home, ".metronous", "data")
}

// ingestKeyPath returns the path to the shared secret file for ingest authentication.
func (s *Server) ingestKeyPath() string {
	return filepath.Join(s.dataDirPath(), "ingest.key")
}

// writePortFile persists port to the given path.
func writePortFile(path string, port int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", port)), 0600)
}

// removePortFile deletes the port file at the given path if it exists.
func removePortFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// pidFilePath returns the instance-scoped path to the PID file used for
// single-instance enforcement: {dataDir}/metronous.pid.
func (s *Server) pidFilePath() string {
	s.mu.RLock()
	dir := s.dataDir
	s.mu.RUnlock()

	if dir != "" {
		return filepath.Join(dir, "metronous.pid")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".metronous/metronous.pid"
	}
	return filepath.Join(home, ".metronous", "metronous.pid")
}

// writePIDFile atomically writes the current process PID to path using a
// write-then-rename sequence so that readers never see a partial file.
func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create dir for pid file: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename pid file: %w", err)
	}
	return nil
}

// removePIDFile deletes the PID file at path if it exists.
func removePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AcquirePIDFile atomically claims ownership of the PID file at path for the
// current process.  It uses O_CREAT|O_EXCL to eliminate the TOCTOU race
// between read-check-claim that existed in the previous implementation.
//
// Behaviour:
//   - If the file does not exist, it is created atomically and our PID written.
//   - If the file already exists and contains our own PID, this is a no-op.
//   - If the file already exists and contains a live foreign PID, the function
//     returns an error instead of terminating an unknown process.
//   - If the file already exists but the recorded PID is dead (stale), the
//     file is removed and the function recurses to re-claim atomically.
//
// It is exported so that unit tests (in package mcp_test) can exercise it
// directly.  Internal callers (ServeWithHealth) use the same symbol.
func AcquirePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create dir for pid file: %w", err)
	}

	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		// ── Atomic creation attempt ───────────────────────────────────────────
		// O_EXCL guarantees only one concurrent caller wins this step.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			// We exclusively created the file — write our PID and done.
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			if werr != nil {
				f.Close()
				return fmt.Errorf("write pid file: %w", werr)
			}
			if err := f.Sync(); err != nil {
				f.Close()
				return fmt.Errorf("sync pid file: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close pid file: %w", err)
			}
			return nil
		}

		if !os.IsExist(err) {
			// Unexpected error (permissions, read-only FS, etc.).
			return fmt.Errorf("create pid file: %w", err)
		}

		// ── File already exists — inspect existing PID ────────────────────────
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				// Another goroutine/process deleted the file between our O_EXCL
				// attempt and ReadFile — retry from the top.
				continue
			}
			return fmt.Errorf("read pid file: %w", readErr)
		}

		existingPID, pidErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if pidErr != nil {
			return fmt.Errorf("invalid PID in file %q: %w", path, pidErr)
		}

		// Already us — nothing to do.
		if existingPID == os.Getpid() {
			return nil
		}

		if existingPID > 0 {
			if isProcessAlive(existingPID) {
				return fmt.Errorf("pid file %s is owned by live process %d; refusing to terminate unknown process", path, existingPID)
			}
		}

		// Remove the stale/expired file and retry with atomic creation.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove stale pid file: %w", removeErr)
		}
	}
	return fmt.Errorf("acquire pid file %s: too many retries", path)
}

// ReadPortFile reads the dynamic HTTP port from the instance-scoped port file.
// Returns the port number, or an error if the file does not exist or is malformed.
func (s *Server) ReadPortFile() (int, error) {
	path := s.portFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read port file: %w", err)
	}
	var port int
	if _, err := fmt.Sscanf(string(data), "%d", &port); err != nil {
		return 0, fmt.Errorf("parse port file: %w", err)
	}
	return port, nil
}
