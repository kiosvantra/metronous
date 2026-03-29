package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const (
	// MCPProtocolVersion is the supported MCP spec version.
	MCPProtocolVersion = "2024-11-05"

	// ServerName is the name reported during MCP initialization.
	ServerName = "metronous"

	// ServerVersion matches the CLI version.
	ServerVersion = "0.1.0"
)

// Server is a minimal MCP server that communicates over stdio.
// It handles JSON-RPC 2.0 messages and dispatches tool calls to registered handlers.
type Server struct {
	mu       sync.RWMutex
	tools    map[string]ToolDefinition
	handlers map[string]ToolHandler
	logger   *zap.Logger
	in       io.Reader
	out      io.Writer
	dataDir  string // used to derive instance-scoped port file path
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
func (s *Server) writeResponse(resp Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
	if err := checkAndClaimPIDFile(pidPath); err != nil {
		return err
	}
	defer func() {
		if removeErr := removePIDFile(pidPath); removeErr != nil {
			s.logger.Warn("could not remove pid file", zap.Error(removeErr))
		}
	}()

	// ── HTTP health server ────────────────────────────────────────────────────
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		// Non-fatal: log the error and proceed with stdio-only mode.
		s.logger.Warn("could not start health HTTP server; continuing in stdio-only mode",
			zap.Error(err))
		return s.ServeStdio(ctx)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	s.logger.Info("health HTTP server listening",
		zap.Int("port", port),
		zap.String("addr", listener.Addr().String()),
	)

	// Persist the port so other processes (e.g. the OpenCode plugin) can find it.
	// The path is instance-scoped to data-dir to avoid collisions between instances.
	portPath := s.portFilePath()
	if portErr := writePortFile(portPath, port); portErr != nil {
		s.logger.Warn("could not write mcp.port file", zap.Error(portErr))
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
	body, _ := json.Marshal(healthResponse{
		Status:  "ok",
		Name:    ServerName,
		Version: ServerVersion,
	})
	_, _ = w.Write(body)
}

// ingestHandler returns an http.HandlerFunc that accepts POST /ingest requests
// containing a JSON ingest payload and dispatches them to the registered "ingest"
// tool handler.  This allows the OpenCode plugin to send telemetry via HTTP while
// OpenCode itself owns the stdio pipe for the MCP protocol.
func (s *Server) ingestHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var arguments map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&arguments); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
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
		handlerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		result, err := handler(handlerCtx, req)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			body, _ := json.Marshal(map[string]string{"error": err.Error()})
			_, _ = w.Write(body)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		body, _ := json.Marshal(result)
		_, _ = w.Write(body)
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

// isProcessAlive returns true if the process with the given PID is running.
// Uses signal 0 (no-op) for cross-platform process existence check.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without actually sending a signal.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// checkAndClaimPIDFile reads any existing PID file at path and determines
// whether another live instance is using it.  If the existing process is alive
// it returns an error.  Otherwise (stale or missing) it writes our PID and
// returns nil.
func checkAndClaimPIDFile(path string) error {
	data, err := os.ReadFile(path)
	if err == nil {
		// File exists — check whether that process is still running.
		pidStr := strings.TrimSpace(string(data))
		if existingPID, parseErr := strconv.Atoi(pidStr); parseErr == nil {
			if existingPID != os.Getpid() && isProcessAlive(existingPID) {
				return fmt.Errorf("another Metronous instance is running (pid %d); stop it first or remove %s", existingPID, path)
			}
		}
		// Stale PID (process gone) — fall through and overwrite.
	} else if !os.IsNotExist(err) {
		// Unexpected read error — log but don't block startup.
		return fmt.Errorf("read pid file: %w", err)
	}

	// No live instance found — claim ownership.
	return writePIDFile(path)
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
