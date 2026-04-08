//go:build linux

package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kiosvantra/metronous/internal/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// shimPortFilePath returns the path to the daemon port file.
func shimPortFilePath() string {
	return filepath.Join(defaultDataDir(), "mcp.port")
}

// readShimPort reads the daemon port from the port file.
func readShimPort() (int, error) {
	path := shimPortFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read port file %s: %w", path, err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse port file: %w", err)
	}
	return port, nil
}

// shimLockFilePath returns the path to the shim start-lock file.
func shimLockFilePath() string {
	return filepath.Join(defaultDataDir(), "shim.lock")
}

// shimCheckHealth performs a GET /health request to the daemon on the given
// port and returns nil only if the daemon responds with HTTP 200.
func shimCheckHealth(port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon health check returned %d", resp.StatusCode)
	}
	return nil
}

// ensureDaemonRunning starts the daemon if the port file is missing, then
// waits up to 5 seconds for it to become available.
//
// A POSIX flock on shimLockFilePath serialises concurrent shim processes so
// that at most one of them spawns a daemon, eliminating the race where several
// shims each see a missing port file and each start their own server instance.
// The port file is always checked under the lock so that the port returned is
// consistent with the daemon state at the time of inspection.
func ensureDaemonRunning() (int, error) {
	// Acquire an exclusive lock before inspecting/starting the daemon.
	// NOTE: we do NOT take a fast-path before the lock because doing so creates
	// a TOCTOU window: between reading the port file and returning the value the
	// daemon could have died and restarted on a different port.  Always checking
	// under the lock eliminates that window.
	lockPath := shimLockFilePath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return 0, fmt.Errorf("create data dir: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("open shim lock: %w", err)
	}
	defer lockFile.Close()

	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		return 0, fmt.Errorf("acquire shim lock: %w", err)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN) //nolint:errcheck

	// Re-check after acquiring the lock: another process may have started the
	// daemon while we were waiting.  Verify the port is reachable before
	// trusting it; a stale file from a crashed daemon must not be accepted.
	if port, err := readShimPort(); err == nil {
		if checkErr := shimCheckHealth(port); checkErr == nil {
			return port, nil
		}
		// Port file exists but daemon is not responding — remove stale file
		// and fall through to start a fresh daemon.
		_ = os.Remove(shimPortFilePath())
	}

	// Start daemon as a detached background process.
	binaryPath, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("get executable path: %w", err)
	}
	dataDir := defaultDataDir()

	cmd := exec.Command(binaryPath, "server", "--data-dir", dataDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start daemon: %w", err)
	}
	// The daemon runs in its own session (Setsid: true) and outlives this shim.
	// Release the process entry so the OS reaps the zombie without a goroutine
	// that would leak for the lifetime of this shim process.
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal: log and continue — the daemon is already running.
		fmt.Fprintf(os.Stderr, "metronous: release daemon process: %v\n", err)
	}

	// Poll for port file up to 5 seconds.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if port, err := readShimPort(); err == nil {
			if err := shimCheckHealth(port); err != nil {
				// Port file exists but daemon not healthy; continue polling.
				continue
			}
			return port, nil
		}
	}
	return 0, fmt.Errorf("daemon did not start within 5 seconds (port file: %s)", shimPortFilePath())
}

// shimJSONRPCResponse is a minimal JSON-RPC 2.0 response envelope.
type shimJSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

// shimJSONRPCRequest is a minimal JSON-RPC 2.0 request envelope.
type shimJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// NewMCPShimCommand creates the `metronous mcp` cobra command.
func NewMCPShimCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Stdio↔HTTP shim for the Metronous daemon (used by OpenCode)",
		Long: `Run a stdio JSON-RPC shim that proxies MCP messages to the shared
Metronous daemon over HTTP.

This command is intended to be used as the MCP server command in opencode.json:
  {"mcp": {"metronous": {"command": ["metronous", "mcp"], "type": "local"}}}

The shim reads JSON-RPC messages from stdin, handles MCP lifecycle messages
locally (initialize, tools/list, ping), and proxies tool calls to the daemon
via HTTP. If the daemon is not running, it will attempt to start it automatically.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPShim(os.Stdin, os.Stdout)
		},
	}
}

// runMCPShim implements the stdio↔HTTP proxy loop.
func runMCPShim(in io.Reader, out io.Writer) error {
	port, err := ensureDaemonRunning()
	if err != nil {
		return fmt.Errorf("ensure daemon running: %w", err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	authToken := os.Getenv("METRONOUS_INGEST_TOKEN")

	// Registered tools — we report what the daemon supports.
	registeredTools := []map[string]interface{}{
		{
			"name":        "ingest",
			"description": "Ingest a telemetry event from an AI agent session",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	encoder := json.NewEncoder(out)

	writeResp := func(resp shimJSONRPCResponse) error {
		return encoder.Encode(resp)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req shimJSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeResp(shimJSONRPCResponse{
				JSONRPC: "2.0",
				Error:   map[string]interface{}{"code": -32700, "message": "parse error: " + err.Error()},
			})
			continue
		}

		// Notifications (no ID) are acknowledged silently.
		if req.ID == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			_ = writeResp(shimJSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
					"serverInfo":      map[string]interface{}{"name": mcp.ServerName, "version": mcp.ServerVersion},
				},
			})

		case "ping":
			_ = writeResp(shimJSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{},
			})

		case "tools/list":
			_ = writeResp(shimJSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"tools": registeredTools},
			})

		case "tools/call":
			result, callErr := shimForwardToolCall(baseURL, authToken, req.Params)
			if callErr != nil {
				_ = writeResp(shimJSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"content": []map[string]interface{}{{"type": "text", "text": "error: " + callErr.Error()}},
						"isError": true,
					},
				})
			} else {
				_ = writeResp(shimJSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  result,
				})
			}

		default:
			_ = writeResp(shimJSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method},
			})
		}
	}

	// EOF — clean exit.
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

// shimForwardToolCall parses the tools/call params and POSTs to the daemon /ingest endpoint.
// When METRONOUS_INGEST_TOKEN is set, it is sent as a transport-level header so the
// daemon can authenticate the shim without changing the ingest payload schema.
func shimForwardToolCall(baseURL, authToken string, rawParams json.RawMessage) (interface{}, error) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if rawParams != nil {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, fmt.Errorf("parse tool call params: %w", err)
		}
	}

	if params.Name != "ingest" {
		return nil, fmt.Errorf("tool not found: %s", params.Name)
	}

	body, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("marshal arguments: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/ingest", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("X-Metronous-Auth", authToken)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("post to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the error body as plain text so the caller gets a useful message
		// even when the body is not valid JSON.
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var result interface{}
	// Limit response body to 1MB to prevent memory exhaustion.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode daemon response: %w", err)
	}

	// Wrap in MCP tool result format.
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": fmt.Sprintf("%v", result)}},
		"isError": false,
	}, nil
}
