package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/huylenq/claude-mission-control/internal/copilot"
	"github.com/huylenq/claude-mission-control/internal/daemon"
)

// --- JSON-RPC 2.0 types ---

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP tool definition ---

type mcpTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema object
	Handler     func(params json.RawMessage) (any, error)
}

// --- MCP content types ---

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpTextContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// --- Entry point ---

func runMCPServe() error {
	// 1. Connect to daemon socket (RPC only, no subscribe)
	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()

	// 2. Load copilot memory
	ws := copilot.NewWorkspace()
	memory := copilot.NewMemory(ws.Dir)

	// 3. Build tool registry
	tools := buildToolRegistry(client, memory)

	// 4. Run stdio JSON-RPC loop
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 256*1024), 1*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(jsonRPCResponse{ //nolint:errcheck
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &jsonRPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		// Notifications have no ID and expect no response
		if req.ID == nil {
			continue
		}

		resp := handleMCPRequest(req, tools)
		enc.Encode(resp) //nolint:errcheck
	}
	return scanner.Err()
}

// --- Request dispatcher ---

func handleMCPRequest(req jsonRPCRequest, tools []mcpTool) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return handleInitialize(req)
	case "tools/list":
		return handleToolsList(req, tools)
	case "tools/call":
		return handleToolsCall(req, tools)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func handleInitialize(req jsonRPCRequest) jsonRPCResponse {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "cmc",
			"version": "1.0",
		},
	}
	data, _ := json.Marshal(result)
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func handleToolsList(req jsonRPCRequest, tools []mcpTool) jsonRPCResponse {
	type toolDef struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

	defs := make([]toolDef, len(tools))
	for i, t := range tools {
		defs[i] = toolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	result := map[string]any{"tools": defs}
	data, _ := json.Marshal(result)
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func handleToolsCall(req jsonRPCRequest, tools []mcpTool) jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	// Find matching tool
	for _, t := range tools {
		if t.Name != params.Name {
			continue
		}

		result, err := t.Handler(params.Arguments)
		if err != nil {
			errResult := mcpToolResult{
				Content: []mcpTextContent{{Type: "text", Text: "error: " + err.Error()}},
				IsError: true,
			}
			data, _ := json.Marshal(errResult)
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
		}

		resultJSON, _ := json.Marshal(result)
		toolResult := mcpToolResult{
			Content: []mcpTextContent{{Type: "text", Text: string(resultJSON)}},
		}
		data, _ := json.Marshal(toolResult)
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &jsonRPCError{Code: -32602, Message: fmt.Sprintf("Unknown tool: %s", params.Name)},
	}
}

// --- Tool registry ---

func buildToolRegistry(client *daemon.Client, memory *copilot.Memory) []mcpTool {
	return []mcpTool{
		// === Read-only tools ===
		{
			Name:        "sessions_list",
			Description: "List active Claude Code sessions with optional status filter",
			InputSchema: mustJSON(`{"type":"object","properties":{"status":{"type":"string","description":"Filter: agent_turn or user_turn"}}}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					Status string `json:"status"`
				}
				json.Unmarshal(params, &p) //nolint:errcheck
				return client.Sessions(p.Status)
			},
		},
		{
			Name:        "session_get",
			Description: "Get details for a single session by ID",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				sessions, err := client.Sessions("")
				if err != nil {
					return nil, err
				}
				for _, s := range sessions {
					if s.SessionID == p.SessionID {
						return s, nil
					}
				}
				return nil, fmt.Errorf("session not found: %s", p.SessionID)
			},
		},
		{
			Name:        "transcript",
			Description: "Get user messages from a session transcript",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.Transcript(p.SessionID)
			},
		},
		{
			Name:        "raw_transcript",
			Description: "Get parsed transcript entries (all roles) for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.TranscriptEntries(p.SessionID)
			},
		},
		{
			Name:        "diff_stats",
			Description: "Get file diff statistics (adds/deletes per file) for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.DiffStats(p.SessionID)
			},
		},
		{
			Name:        "diff_hunks",
			Description: "Get file diff hunks (actual content changes) for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.DiffHunks(p.SessionID)
			},
		},
		{
			Name:        "summary",
			Description: "Get the AI-generated summary for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.Summary(p.SessionID)
			},
		},
		{
			Name:        "hook_events",
			Description: "Get debug hook events for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.HookEvents(p.SessionID)
			},
		},
		{
			Name:        "backlog_list",
			Description: "List backlog items for a working directory",
			InputSchema: mustJSON(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory path"}},"required":["cwd"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					CWD string `json:"cwd"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.BacklogList(p.CWD)
			},
		},
		{
			Name:        "memory_read",
			Description: "Read the copilot's long-term memory (MEMORY.md)",
			InputSchema: mustJSON(`{"type":"object","properties":{}}`),
			Handler: func(_ json.RawMessage) (any, error) {
				content, err := memory.ReadLongTerm()
				if err != nil {
					return map[string]string{"content": "(empty)"}, nil
				}
				if content == "" {
					return map[string]string{"content": "(empty)"}, nil
				}
				return map[string]string{"content": content}, nil
			},
		},
		{
			Name:        "memory_search",
			Description: "Search the copilot's long-term memory",
			InputSchema: mustJSON(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"max_results":{"type":"integer","description":"Max results (default 10)"}},"required":["query"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					Query      string `json:"query"`
					MaxResults int    `json:"max_results"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				if p.MaxResults <= 0 {
					p.MaxResults = 10
				}
				results, err := memory.Search(p.Query, p.MaxResults)
				if err != nil {
					return nil, err
				}
				return results, nil
			},
		},
		{
			Name:        "daily_log_read",
			Description: "Read the copilot's daily activity log for a given date",
			InputSchema: mustJSON(`{"type":"object","properties":{"date":{"type":"string","description":"Date in YYYY-MM-DD format (defaults to today)"}}}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					Date string `json:"date"`
				}
				json.Unmarshal(params, &p) //nolint:errcheck
				if p.Date == "" {
					p.Date = time.Now().Format("2006-01-02")
				}
				content, err := memory.ReadDailyLog(p.Date)
				if err != nil {
					return map[string]string{"content": "(no log for " + p.Date + ")"}, nil
				}
				if content == "" {
					return map[string]string{"content": "(no log for " + p.Date + ")"}, nil
				}
				return map[string]string{"content": content}, nil
			},
		},

		// === Write tools ===
		{
			Name:        "memory_append",
			Description: "Append a fact to the copilot's long-term memory",
			InputSchema: mustJSON(`{"type":"object","properties":{"fact":{"type":"string","description":"Fact to remember"}},"required":["fact"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					Fact string `json:"fact"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				if err := memory.AppendLongTerm(p.Fact); err != nil {
					return nil, err
				}
				return map[string]string{"status": "ok"}, nil
			},
		},
		{
			Name:        "backlog_create",
			Description: "Create a new backlog item",
			InputSchema: mustJSON(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory path"},"body":{"type":"string","description":"Backlog item body text"}},"required":["cwd","body"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					CWD  string `json:"cwd"`
					Body string `json:"body"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.BacklogCreate(p.CWD, p.Body)
			},
		},
		{
			Name:        "backlog_update",
			Description: "Update an existing backlog item",
			InputSchema: mustJSON(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory path"},"id":{"type":"string","description":"Backlog item ID"},"body":{"type":"string","description":"Updated body text"}},"required":["cwd","id","body"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					CWD  string `json:"cwd"`
					ID   string `json:"id"`
					Body string `json:"body"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.BacklogUpdate(p.CWD, p.ID, p.Body)
			},
		},
		{
			Name:        "backlog_delete",
			Description: "Delete a backlog item",
			InputSchema: mustJSON(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory path"},"id":{"type":"string","description":"Backlog item ID"}},"required":["cwd","id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					CWD string `json:"cwd"`
					ID  string `json:"id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return nil, client.BacklogDelete(p.CWD, p.ID)
			},
		},
		{
			Name:        "synthesize",
			Description: "Trigger AI summary synthesis for a session",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"session_id":{"type":"string","description":"Session ID"}},"required":["pane_id","session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				summary, fromCache, err := client.Synthesize(p.PaneID, p.SessionID)
				if err != nil {
					return nil, err
				}
				return map[string]any{"summary": summary, "fromCache": fromCache}, nil
			},
		},
		{
			Name:        "synthesize_all",
			Description: "Trigger AI summary synthesis for all sessions (except one)",
			InputSchema: mustJSON(`{"type":"object","properties":{"skip_pane_id":{"type":"string","description":"Pane ID to skip (e.g. the most recently active)"}}}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SkipPaneID string `json:"skip_pane_id"`
				}
				json.Unmarshal(params, &p) //nolint:errcheck
				return client.SynthesizeAll(p.SkipPaneID)
			},
		},
		{
			Name:        "bookmark",
			Description: "Bookmark a session for later",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"session_id":{"type":"string","description":"Session ID"}},"required":["pane_id","session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.Later(p.PaneID, p.SessionID)
			},
		},
		{
			Name:        "queue_message",
			Description: "Queue a message for delivery when the session becomes idle",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"session_id":{"type":"string","description":"Session ID"},"message":{"type":"string","description":"Message to queue"}},"required":["pane_id","session_id","message"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					SessionID string `json:"session_id"`
					Message   string `json:"message"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.Queue(p.PaneID, p.SessionID, p.Message)
			},
		},

		// === Action tools (require confirmation via ACP) ===
		{
			Name:        "send_message",
			Description: "Send a message to a Claude Code session (delivers immediately to tmux pane)",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"message":{"type":"string","description":"Message to send"}},"required":["session_id","message"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
					Message   string `json:"message"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.Send(p.SessionID, p.Message)
			},
		},
		{
			Name:        "kill_session",
			Description: "Terminate a Claude Code session (SIGTERM + kill pane)",
			InputSchema: mustJSON(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.Kill(p.SessionID)
			},
		},
		{
			Name:        "spawn_session",
			Description: "Create a new tmux window and launch a Claude Code session",
			InputSchema: mustJSON(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory"},"tmux_session":{"type":"string","description":"Tmux session name (optional)"},"message":{"type":"string","description":"Initial message to send (optional)"}},"required":["cwd"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					CWD         string `json:"cwd"`
					TmuxSession string `json:"tmux_session"`
					Message     string `json:"message"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return client.Spawn(p.CWD, p.TmuxSession, p.Message)
			},
		},
		{
			Name:        "commit",
			Description: "Send /commit-commands:commit to a session (commit only)",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"session_id":{"type":"string","description":"Session ID"},"pid":{"type":"integer","description":"Process ID"}},"required":["pane_id","session_id","pid"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					SessionID string `json:"session_id"`
					PID       int    `json:"pid"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.CommitOnly(p.PaneID, p.SessionID, p.PID)
			},
		},
		{
			Name:        "commit_done",
			Description: "Send /commit-commands:commit and auto-kill on commit completion",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"session_id":{"type":"string","description":"Session ID"},"pid":{"type":"integer","description":"Process ID"}},"required":["pane_id","session_id","pid"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					SessionID string `json:"session_id"`
					PID       int    `json:"pid"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.CommitAndDone(p.PaneID, p.SessionID, p.PID)
			},
		},
		{
			Name:        "bookmark_kill",
			Description: "Bookmark a session and kill the pane",
			InputSchema: mustJSON(`{"type":"object","properties":{"pane_id":{"type":"string","description":"Tmux pane ID"},"pid":{"type":"integer","description":"Process ID"},"session_id":{"type":"string","description":"Session ID"}},"required":["pane_id","pid","session_id"]}`),
			Handler: func(params json.RawMessage) (any, error) {
				var p struct {
					PaneID    string `json:"pane_id"`
					PID       int    `json:"pid"`
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, fmt.Errorf("invalid params: %w", err)
				}
				return map[string]string{"status": "ok"}, client.LaterKill(p.PaneID, p.PID, p.SessionID)
			},
		},
	}
}

// mustJSON converts a string to json.RawMessage. Panics on invalid JSON (caught at init time).
func mustJSON(s string) json.RawMessage {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		panic(fmt.Sprintf("invalid JSON in tool schema: %s: %v", s, err))
	}
	return raw
}
