// Package mcp implements the Model Context Protocol (MCP) JSON-RPC 2.0 server
// over stdio transport. This is a minimal, spec-compliant implementation
// sufficient for the Metronous MVP without requiring external MCP libraries
// (which require Go 1.23+).
//
// MCP Specification reference: https://spec.modelcontextprotocol.io/
package mcp

import (
	"context"
	"encoding/json"
)

// JSON-RPC 2.0 types.

// Request represents an incoming JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents an outgoing JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// MCP-specific types.

// ToolDefinition describes an MCP tool that this server exposes.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema defines the JSON Schema for a tool's input parameters.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property describes a single JSON Schema property.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
}

// CallToolRequest represents the params of a tools/call request.
type CallToolRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// CallToolResult is the result body for a tools/call response.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single content piece in a tool result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextContent creates a text ContentItem.
func TextContent(text string) ContentItem {
	return ContentItem{Type: "text", Text: text}
}

// InitializeResult is returned by the initialize method.
type InitializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    Capability `json:"capabilities"`
	ServerInfo      ServerInfo `json:"serverInfo"`
}

// Capability describes what this server supports.
type Capability struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability indicates the server supports tools.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo contains metadata about this MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ListToolsResult is returned by the tools/list method.
type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolHandler is the function signature for MCP tool handlers.
type ToolHandler func(ctx context.Context, req CallToolRequest) (*CallToolResult, error)

// newErrorResponse constructs a JSON-RPC 2.0 error response.
func newErrorResponse(id json.RawMessage, code int, message string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
}

// newSuccessResponse constructs a JSON-RPC 2.0 success response.
func newSuccessResponse(id json.RawMessage, result interface{}) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}
