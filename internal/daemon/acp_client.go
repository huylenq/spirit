package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const acpSessionKey = "agent:development:copilot"

// acpClient manages a long-lived ACP subprocess (openclaw acp) for copilot communication.
// It speaks JSON-RPC 2.0 over newline-delimited stdio.
//
// The client is lazy: the subprocess starts on the first Prompt() call.
// Only one Prompt() runs at a time (enforced externally by copilotMu in the Daemon).
type acpClient struct {
	mu        sync.Mutex // protects all mutable fields below
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	scanner   *bufio.Scanner // persistent scanner over stdout
	nextID    atomic.Int64
	sessionID string
	alive     bool
}

// --- JSON-RPC types ---

type acpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type acpResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *acpError `json:"error,omitempty"`
}

// acpMessage is the generic inbound message (response or notification or agent-to-client request).
type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`     // present for responses and agent-to-client requests
	Method  string          `json:"method,omitempty"` // present for notifications and agent-to-client requests
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpError       `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type acpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- session/update types ---

type acpUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type acpSessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"` // "agent_message_chunk", "tool_call", "tool_call_update", "plan"
	Content       json.RawMessage `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
}

type acpContentBlock struct {
	Type string `json:"type"` // "text", "thinking"
	Text string `json:"text,omitempty"`
}

// --- lifecycle ---

// ensureReady starts the ACP subprocess and performs the handshake if not already running.
// Caller must NOT hold c.mu.
func (c *acpClient) ensureReady() error {
	c.mu.Lock()
	if c.alive && c.sessionID != "" {
		c.mu.Unlock()
		return nil
	}

	// Kill any stale process
	c.stopLocked()

	cmd := exec.Command("openclaw", "acp", "--session", acpSessionKey)
	cmd.Stderr = log.Writer() // route ACP bridge errors to daemon log

	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		c.mu.Unlock()
		return fmt.Errorf("acp stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		c.mu.Unlock()
		return fmt.Errorf("start openclaw acp: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	c.alive = true
	c.mu.Unlock()

	// Handshake outside the lock — scanner reads are single-threaded here
	if err := c.handshake(); err != nil {
		c.mu.Lock()
		c.stopLocked()
		c.mu.Unlock()
		return fmt.Errorf("acp handshake: %w", err)
	}

	log.Printf("acp: connected (session=%s)", c.sessionID)
	return nil
}

// sendLocked writes a JSON-RPC message to stdin. Caller must hold c.mu.
func (c *acpClient) sendLocked(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// send writes a JSON-RPC message to stdin with locking.
func (c *acpClient) send(msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendLocked(msg)
}

// handshake performs initialize + session/new. Called once after starting the subprocess.
func (c *acpClient) handshake() error {
	// 1. initialize
	initID := c.nextID.Add(1)
	if err := c.send(acpRequest{
		JSONRPC: "2.0",
		ID:      initID,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs": map[string]bool{
					"readTextFile":  false,
					"writeTextFile": false,
				},
			},
			"clientInfo": map[string]any{
				"name":    "cmc-copilot",
				"title":   "CMC Copilot",
				"version": "1.0.0",
			},
		},
	}); err != nil {
		return fmt.Errorf("send initialize: %w", err)
	}

	if err := c.waitForResponse(initID); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	// 2. session/new
	newID := c.nextID.Add(1)
	if err := c.send(acpRequest{
		JSONRPC: "2.0",
		ID:      newID,
		Method:  "session/new",
		Params: map[string]any{
			"cwd":        os.Getenv("HOME"),
			"mcpServers": []any{},
		},
	}); err != nil {
		return fmt.Errorf("send session/new: %w", err)
	}

	for c.scanner.Scan() {
		var msg acpMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &msg); err != nil {
			log.Printf("acp handshake: parse error: %v", err)
			continue
		}
		if msg.ID != nil && *msg.ID == newID {
			if msg.Error != nil {
				return fmt.Errorf("session/new: %s", msg.Error.Message)
			}
			var result struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(msg.Result, &result); err != nil {
				return fmt.Errorf("parse session/new: %w", err)
			}
			c.mu.Lock()
			c.sessionID = result.SessionID
			c.mu.Unlock()
			return nil
		}
		// Skip notifications during handshake
	}
	return fmt.Errorf("connection closed during handshake")
}

// waitForResponse reads until we get a response with the given ID.
func (c *acpClient) waitForResponse(id int64) error {
	for c.scanner.Scan() {
		var msg acpMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &msg); err != nil {
			log.Printf("acp: parse error: %v", err)
			continue
		}
		if msg.ID != nil && *msg.ID == id {
			if msg.Error != nil {
				return fmt.Errorf("error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return nil
		}
	}
	return fmt.Errorf("connection closed waiting for response %d", id)
}

// Prompt sends a message and streams CopilotStreamData events via onUpdate.
// Blocks until the prompt turn completes or the context is cancelled.
// Returns the full accumulated text for history persistence.
//
// Only one Prompt() may run at a time (caller must serialize via copilotMu).
func (c *acpClient) Prompt(ctx context.Context, text string, onUpdate func(CopilotStreamData)) (string, error) {
	if err := c.ensureReady(); err != nil {
		return "", err
	}

	promptID := c.nextID.Add(1)
	if err := c.send(acpRequest{
		JSONRPC: "2.0",
		ID:      promptID,
		Method:  "session/prompt",
		Params: map[string]any{
			"sessionId": c.sessionID,
			"prompt": []map[string]string{
				{"type": "text", "text": text},
			},
		},
	}); err != nil {
		c.mu.Lock()
		c.alive = false
		c.mu.Unlock()
		return "", fmt.Errorf("send prompt: %w", err)
	}

	// Background goroutine: on cancellation, send session/cancel then force-kill after 5s
	cancelDone := make(chan struct{})
	defer close(cancelDone)
	go func() {
		select {
		case <-ctx.Done():
			c.send(acpNotification{ //nolint:errcheck
				JSONRPC: "2.0",
				Method:  "session/cancel",
				Params: map[string]any{
					"sessionId": c.sessionID,
				},
			})
			// Give subprocess 5s to respond, then force-kill to unblock scanner
			killTimer := time.NewTimer(5 * time.Second)
			defer killTimer.Stop()
			select {
			case <-cancelDone:
				return
			case <-killTimer.C:
				log.Printf("acp: force-killing after cancel timeout")
				c.mu.Lock()
				c.stopLocked()
				c.mu.Unlock()
			}
		case <-cancelDone:
		}
	}()

	var fullText strings.Builder
	for c.scanner.Scan() {
		if ctx.Err() != nil {
			return "", fmt.Errorf("cancelled")
		}

		var msg acpMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &msg); err != nil {
			log.Printf("acp: parse error: %v", err)
			continue
		}

		// Response to our prompt — turn is done
		if msg.ID != nil && *msg.ID == promptID {
			if msg.Error != nil {
				return "", fmt.Errorf("prompt: %s", msg.Error.Message)
			}
			return strings.TrimSpace(fullText.String()), nil
		}

		// Agent-to-client request (e.g. session/request_permission)
		if msg.ID != nil && msg.Method != "" {
			c.handleAgentRequest(msg)
			continue
		}

		// Notification — stream to TUI
		if msg.Method == "session/update" {
			events := parseACPUpdate(msg.Params)
			for _, evt := range events {
				if evt.Type == "text_delta" {
					fullText.WriteString(evt.Content)
				}
				onUpdate(evt)
			}
		}
	}

	// Scanner stopped — subprocess likely died
	c.mu.Lock()
	c.alive = false
	c.mu.Unlock()

	if ctx.Err() != nil {
		return "", fmt.Errorf("cancelled")
	}
	return "", fmt.Errorf("acp: connection closed")
}

// handleAgentRequest responds to agent-to-client requests.
// Auto-approves permission requests; rejects unknown methods.
func (c *acpClient) handleAgentRequest(msg acpMessage) {
	switch msg.Method {
	case "session/request_permission":
		c.send(acpResponse{ //nolint:errcheck
			JSONRPC: "2.0",
			ID:      *msg.ID,
			Result:  map[string]bool{"approved": true},
		})
	default:
		log.Printf("acp: unknown agent request: %s", msg.Method)
		c.send(acpResponse{ //nolint:errcheck
			JSONRPC: "2.0",
			ID:      *msg.ID,
			Error:   &acpError{Code: -32601, Message: "not supported: " + msg.Method},
		})
	}
}

// stopLocked kills the ACP subprocess. Caller must hold c.mu.
func (c *acpClient) stopLocked() {
	if !c.alive {
		return
	}
	c.alive = false
	c.sessionID = ""
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		// ProcessState is nil if Wait hasn't been called yet
		if c.cmd.ProcessState == nil {
			c.cmd.Process.Kill() //nolint:errcheck
		}
		c.cmd.Wait() //nolint:errcheck
	}
}

// Stop kills the ACP subprocess (exported for daemon shutdown).
func (c *acpClient) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

// --- ACP update → CopilotStreamData conversion ---

func parseACPUpdate(params json.RawMessage) []CopilotStreamData {
	var up acpUpdateParams
	if err := json.Unmarshal(params, &up); err != nil {
		return nil
	}

	var su acpSessionUpdate
	if err := json.Unmarshal(up.Update, &su); err != nil {
		return nil
	}

	switch su.SessionUpdate {
	case "agent_message_chunk":
		return parseAgentChunk(su.Content)

	case "tool_call":
		return []CopilotStreamData{{
			Type:    "tool_call",
			Content: su.Title,
			ToolID:  su.ToolCallID,
			Kind:    su.Kind,
			Status:  su.Status,
		}}

	case "tool_call_update":
		evt := CopilotStreamData{
			Type:   "tool_update",
			ToolID: su.ToolCallID,
			Status: su.Status,
		}
		if su.Content != nil {
			var blocks []struct {
				Type    string `json:"type"`
				Content struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(su.Content, &blocks) == nil && len(blocks) > 0 {
				evt.Content = blocks[0].Content.Text
			}
		}
		return []CopilotStreamData{evt}

	case "plan":
		planJSON, _ := json.Marshal(su)
		return []CopilotStreamData{{
			Type:    "plan",
			Content: string(planJSON),
		}}

	default:
		return nil
	}
}

func parseAgentChunk(content json.RawMessage) []CopilotStreamData {
	if content == nil {
		return nil
	}

	var blocks []acpContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		var block acpContentBlock
		if err := json.Unmarshal(content, &block); err != nil {
			return nil
		}
		blocks = []acpContentBlock{block}
	}

	var events []CopilotStreamData
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				events = append(events, CopilotStreamData{
					Type:    "text_delta",
					Content: b.Text,
				})
			}
		case "thinking":
			if b.Text != "" {
				events = append(events, CopilotStreamData{
					Type:    "thought",
					Content: b.Text,
				})
			}
		}
	}
	return events
}
