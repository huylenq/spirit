// Package mcpserver implements `spirit mcp`: a Model Context Protocol server
// speaking JSON-RPC 2.0 over newline-delimited stdio. Each tool wraps a daemon RPC
// over the Unix socket — the same operations the agent CLI exposes — and the tool
// schemas ARE the operation contract (spec Decision 3): typed, self-describing, and
// receipt-bearing (side-effect tools return an ActionReceipt, Decision 5).
//
// Hermes registers this server via the ACP `mcp_servers` array at session open, so
// Lulu gets Spirit's operation surface as typed tools without prose injection.
//
// The server is hand-rolled rather than pulling in a Go MCP dependency: the surface
// it needs (initialize / tools/list / tools/call / ping) is small and stable, and
// the repo already speaks JSON-RPC 2.0 for the ACP wire. Hermes drives it with the
// official Python MCP SDK over the stdio transport (UTF-8, one JSON message per line,
// no embedded newlines), which this matches.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
)

// protocolVersion is what the server advertises when a client omits one. When the
// client sends a version we echo theirs — the tools/list + tools/call surface is
// version-stable, so we accept whatever the negotiating client asks for.
const protocolVersion = "2025-06-18"

const (
	serverName    = "spirit"
	serverVersion = "1.0.0"
)

// JSON-RPC 2.0 framing types.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC standard error codes used here.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Server dispatches MCP requests against a daemon API. Construct with New.
type Server struct {
	api   daemonAPI
	tools []tool
}

// New builds a server bound to a daemon API (a *daemon.Client in production, a fake
// in tests).
func New(api daemonAPI) *Server {
	return &Server{api: api, tools: buildTools()}
}

// Serve reads newline-delimited JSON-RPC requests from in and writes responses to
// out until EOF. Nothing but JSON-RPC frames may be written to out — logs go to the
// caller's log.Writer (stderr), never stdout, which is the protocol channel.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp, ok := s.handle(line)
		if !ok {
			continue // notification — no response
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	return scanner.Err()
}

// handle parses and dispatches one request line. The bool is false when the message
// is a notification (no id) and therefore takes no response.
func (s *Server) handle(line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, codeParseError, "parse error"), true
	}
	// Notifications carry no id and expect no reply (e.g. notifications/initialized).
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return okResponse(req.ID, s.handleInitialize(req.Params)), true
	case "notifications/initialized", "initialized":
		return rpcResponse{}, false
	case "ping":
		return okResponse(req.ID, map[string]any{}), true
	case "tools/list":
		return okResponse(req.ID, s.handleToolsList()), true
	case "tools/call":
		result, rerr := s.handleToolsCall(req.Params)
		if rerr != nil {
			return rpcResponse{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: rerr}, true
		}
		return okResponse(req.ID, result), true
	default:
		if isNotification {
			log.Printf("mcp: ignoring notification %s", req.Method)
			return rpcResponse{}, false
		}
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method), true
	}
}

func (s *Server) handleInitialize(params json.RawMessage) map[string]any {
	version := protocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.ProtocolVersion != "" {
			version = p.ProtocolVersion // accept the client's negotiated version
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"instructions": "Spirit operation surface: inspect and control Claude Code / Codex sessions across tmux panes. Read-only tools return data; side-effect tools return an ActionReceipt with the target, delivery outcome, and observed post-action session state. All tools take an explicit session_id — get ids from list_sessions.",
	}
}

// toolDescriptor is the tools/list entry shape MCP clients expect.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (s *Server) handleToolsList() map[string]any {
	descs := make([]toolDescriptor, 0, len(s.tools))
	for _, t := range s.tools {
		descs = append(descs, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return map[string]any{"tools": descs}
}

// toolCallResult is the MCP tools/call result: content blocks plus an isError flag
// distinguishing a tool-execution failure (daemon rejected the op) from a JSON-RPC
// protocol error.
type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *Server) handleToolsCall(params json.RawMessage) (*toolCallResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params"}
	}
	for _, t := range s.tools {
		if t.Name == p.Name {
			payload, isErr := t.Handler(s.api, p.Arguments)
			return textResult(payload, isErr), nil
		}
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool: " + p.Name}
}

// textResult marshals a payload to pretty JSON in a single text content block. This
// is the universally-accepted content form; structuredContent is intentionally
// avoided (it requires a declared outputSchema in strict SDKs).
func textResult(payload any, isError bool) *toolCallResult {
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return &toolCallResult{
			Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("marshal error: %v", err)}},
			IsError: true,
		}
	}
	return &toolCallResult{
		Content: []contentBlock{{Type: "text", Text: string(text)}},
		IsError: isError,
	}
}

func okResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Error: &rpcError{Code: code, Message: msg}}
}

// idOrNull returns the request id, or a JSON null for responses to id-less input
// (JSON-RPC requires an id field on every response).
func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}
