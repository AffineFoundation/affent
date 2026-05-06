// Package mcp is a minimal Model Context Protocol client. It speaks
// stdio JSON-RPC to one MCP server process and exposes the server's
// tools/* surface as affent.Tool entries.
//
// What's implemented:
//
//   - stdio transport (newline-delimited JSON-RPC 2.0)
//   - initialize / initialized handshake
//   - tools/list, tools/call
//   - graceful shutdown via Close
//
// What's not (and probably doesn't need to be for v0):
//
//   - resources/*, prompts/*, sampling (server -> client requests)
//   - HTTP/SSE transport
//   - progress / cancellation notifications
//
// The MCP spec lives at https://spec.modelcontextprotocol.io.
package mcp

import "encoding/json"

// ServerSpec describes how to reach a single MCP server. Two transports
// are supported:
//
//   - stdio: set Command (and optionally Args / Env / Cwd). The server
//     is launched as a child process and JSON-RPC flows over its
//     stdin/stdout. Mirrors the claude_desktop_config.json shape.
//
//   - streamable-http: set URL (and optionally Headers). JSON-RPC flows
//     over POSTs to the endpoint; per-request responses arrive either
//     as application/json or as text/event-stream (SSE). Server-side
//     session is tracked via the Mcp-Session-Id header. Spec rev
//     2025-03-26.
//
// URL and Command are mutually exclusive — Start picks the transport
// from whichever is set.
type ServerSpec struct {
	// Name is a short label like "fs" or "git". Becomes the prefix on
	// every tool the server exports (e.g. "fs_read_file") so tool names
	// don't collide across servers.
	Name string `json:"name"`

	// --- stdio transport ---

	// Command is the executable to launch (e.g. "npx", "uvx",
	// "/usr/local/bin/mcp-server-foo"). Required for stdio.
	Command string `json:"command,omitempty"`
	// Args are passed verbatim after Command.
	Args []string `json:"args,omitempty"`
	// Env are extra "KEY=VALUE" pairs layered on top of the parent
	// process env. Optional.
	Env []string `json:"env,omitempty"`
	// Cwd is the working directory for the child. Defaults to the
	// parent's cwd.
	Cwd string `json:"cwd,omitempty"`

	// --- streamable-http transport ---

	// URL is the streamable-http endpoint, e.g.
	// "http://127.0.0.1:8123/mcp". Required for HTTP.
	URL string `json:"url,omitempty"`
	// Headers are extra HTTP headers sent on every request. Useful for
	// auth tokens, version pinning, etc.
	Headers map[string]string `json:"headers,omitempty"`
}

// ToolDescriptor is one tool returned by tools/list.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// CallResult is what tools/call returns. We flatten the standard MCP
// "content" array (a list of typed blocks) into a single text string
// for affent's plain-text tool result contract; binary / image content
// gets a placeholder.
type CallResult struct {
	Text    string
	IsError bool
}

// rpcRequest / rpcResponse are the on-the-wire JSON-RPC 2.0 shapes.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"` // omit for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	// Notifications from the server arrive with no ID; we route by
	// Method instead.
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// initializeParams / initializeResult are the only handshake messages
// we actually parse fields from.
type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      clientInfo     `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolsCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

type contentBlock struct {
	Type string `json:"type"` // "text" | "image" | "resource" | ...
	Text string `json:"text,omitempty"`
	// image / resource fields ignored; we just note their presence.
	MimeType string          `json:"mimeType,omitempty"`
	Data     string          `json:"data,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}
