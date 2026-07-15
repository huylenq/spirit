package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Reactive-run isolation over the same ACP subprocess (W7, spec Decisions 2 and
// 11): a recommend-level watch firing runs in a session/fork of the persistent
// Lulu session — the fork deep-copies conversation history, so the run is
// grounded in what Lulu already knows, while its prompt, stream, and any
// permission traffic never touch the persistent thread. One subprocess serves
// both: the demultiplexed wire routes each session's updates independently
// (per-session sinks below), so a user turn and a reactive run cannot
// interleave.
//
// Forks are created with an EMPTY mcpServers list: a reactive run has no tools
// at all, which makes "no fleet mutation" structural rather than prompt-level —
// there is nothing to call. Evidence is inlined by the daemon (bounded), and
// the run is exactly one prompt.

// errACPForkUnsupported reports that the agent did not advertise the fork
// capability; callers degrade (recommend → notify), never hard-fail.
var errACPForkUnsupported = fmt.Errorf("acp: agent does not advertise session/fork")

// ForkCapable reports whether the connected agent advertises session/fork.
// Requires the handshake to have happened (ensureReady) to be meaningful.
func (c *acpClient) ForkCapable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps.ForkSessions
}

// ForkSession forks the main Lulu session into a disposable branch and returns
// the new session id. The fork gets no MCP servers — reactive runs are
// tool-less by design. There is no ACP method to delete a session; forks are
// simply abandoned after the run (bounded by the per-watch LLM budgets).
func (c *acpClient) ForkSession() (string, error) {
	if err := c.ensureReady(); err != nil {
		return "", err
	}
	c.mu.Lock()
	capable := c.caps.ForkSessions
	sid := c.sessionID
	c.mu.Unlock()
	if !capable {
		return "", errACPForkUnsupported
	}
	if sid == "" {
		return "", fmt.Errorf("acp: no main session to fork")
	}
	res, err := c.callTimeout("session/fork", map[string]any{
		"sessionId":  sid,
		"cwd":        os.Getenv("HOME"),
		"mcpServers": []any{}, // deliberately none: the reactive run must have nothing to call
	}, 60*time.Second)
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &result); err != nil || result.SessionID == "" {
		return "", fmt.Errorf("acp: session/fork returned no session id")
	}
	return result.SessionID, nil
}

// PromptSession sends one prompt to a non-main session (a reactive fork) and
// returns the accumulated assistant text. Unlike user turns (deliberately
// unbounded), callers pass a bounded ctx; on cancellation the turn is asked to
// cancel and the call returns an error. Stream events for the session are
// routed to a private sink and never reach the main sink or any TUI client.
func (c *acpClient) PromptSession(ctx context.Context, sessionID, text string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("acp: PromptSession requires a session id")
	}

	var textMu sync.Mutex
	var full strings.Builder
	c.setSessionSink(sessionID, func(evt CopilotStreamData) {
		if evt.Type == "text_delta" {
			textMu.Lock()
			full.WriteString(evt.Content)
			textMu.Unlock()
		}
	})
	defer c.clearSessionSink(sessionID)

	promptID := c.nextID.Add(1)
	ch := make(chan *acpMessage, 1)
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = map[int64]chan *acpMessage{}
	}
	c.pending[promptID] = ch
	c.pendingMu.Unlock()

	if err := c.writeMessage(acpRequest{
		JSONRPC: "2.0",
		ID:      promptID,
		Method:  "session/prompt",
		Params: map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]string{{"type": "text", "text": text}},
		},
	}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, promptID)
		c.pendingMu.Unlock()
		return "", fmt.Errorf("send fork prompt: %w", err)
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return "", fmt.Errorf("fork prompt: %s", msg.Error.Message)
		}
		textMu.Lock()
		defer textMu.Unlock()
		return strings.TrimSpace(full.String()), nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, promptID)
		c.pendingMu.Unlock()
		c.notify("session/cancel", map[string]any{"sessionId": sessionID}) //nolint:errcheck
		return "", fmt.Errorf("fork prompt: %w", ctx.Err())
	}
}

// --- per-session sink registry ---

// setSessionSink registers a dedicated stream consumer for one session id.
// dispatchUpdate routes that session's updates there instead of the main sink.
func (c *acpClient) setSessionSink(sessionID string, f func(CopilotStreamData)) {
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	if c.sessionSinks == nil {
		c.sessionSinks = map[string]func(CopilotStreamData){}
	}
	c.sessionSinks[sessionID] = f
}

func (c *acpClient) clearSessionSink(sessionID string) {
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	delete(c.sessionSinks, sessionID)
}

// sessionSinkFor returns the dedicated sink for a session id, or nil (main path).
func (c *acpClient) sessionSinkFor(sessionID string) func(CopilotStreamData) {
	if sessionID == "" {
		return nil
	}
	c.sinkMu.Lock()
	defer c.sinkMu.Unlock()
	return c.sessionSinks[sessionID]
}
