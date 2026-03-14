package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/copilot"
)

const maxCopilotHistory = 200 // 100 exchanges (user + copilot per exchange)

// handleCopilotChat starts a streaming copilot prompt in the background.
// Returns an immediate "streaming" ack; actual tokens arrive via the subscribe connection.
func (d *Daemon) handleCopilotChat(data json.RawMessage) *Response {
	var req CopilotChatData
	if err := json.Unmarshal(data, &req); err != nil || req.Message == "" {
		r := errResponse("invalid copilot_chat request")
		return &r
	}

	// Build context preamble from live daemon state (if enabled)
	fullPrompt := req.Message
	if d.copilotPreamble.Load() {
		fullPrompt = d.buildCopilotPreamble() + "\n\n" + req.Message
	}

	// Cancel any existing copilot prompt
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	d.copilotCancel = cancel
	d.copilotMu.Unlock()

	// Run streaming in background; results push to subscribers
	go func() {
		defer d.clearCopilotCancel()
		output, err := d.runCopilotPromptStreaming(ctx, fullPrompt)
		if err != nil {
			return // error + done already sent by runCopilotPromptStreaming
		}
		// Keep in-memory history for live TUI display during this session.
		// On TUI reopen, handleCopilotHistory reads from OpenClaw's JSONL instead.
		now := time.Now()
		d.appendCopilotHistory(
			CopilotHistoryMsg{Role: "user", Content: req.Message, Time: now},
			CopilotHistoryMsg{Role: "copilot", Content: output, Time: now},
		)
	}()

	r := resultResponse(map[string]string{"status": "streaming"})
	return &r
}

// handleCopilotHistory returns the copilot conversation for TUI restore on open.
// Reads from OpenClaw's JSONL session transcript (source of truth); falls back to
// in-memory history if the ACP session file is unavailable.
func (d *Daemon) handleCopilotHistory() *Response {
	if msgs := readACPHistory(); len(msgs) > 0 {
		r := resultResponse(CopilotHistoryData{Messages: msgs})
		return &r
	}
	// Fallback: in-memory (e.g. ACP session not yet written to disk)
	d.copilotHistoryMu.RLock()
	msgs := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(msgs, d.copilotHistory)
	d.copilotHistoryMu.RUnlock()
	r := resultResponse(CopilotHistoryData{Messages: msgs})
	return &r
}

// handleCopilotClearHistory wipes the in-memory history and resets the ACP session
// so OpenClaw starts a fresh conversation (triggered by /new in the TUI).
func (d *Daemon) handleCopilotClearHistory() *Response {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = nil
	d.copilotHistoryMu.Unlock()

	// Kill the ACP subprocess so the next prompt starts a fresh OpenClaw session
	d.acpClient.Stop()

	// Remove the session mapping from OpenClaw's sessions.json so that
	// readACPHistory() won't load stale history if the TUI is reopened
	// before a new prompt creates a fresh session.
	clearACPSessionMapping()

	r := resultResponse(map[string]string{"status": "cleared"})
	return &r
}

// runCopilotPromptStreaming sends a prompt via the ACP client (openclaw acp subprocess),
// streaming events to subscribers in real-time. Returns the full accumulated text
// response for history persistence. Always sends a "done" event as the final stream event.
func (d *Daemon) runCopilotPromptStreaming(ctx context.Context, prompt string) (string, error) {
	output, err := d.acpClient.Prompt(ctx, prompt, func(evt CopilotStreamData) {
		d.pushCopilotStream(evt)
	})
	if err != nil {
		d.pushCopilotStream(CopilotStreamData{Type: "error", Content: err.Error()})
	}
	d.pushCopilotStream(CopilotStreamData{Type: "done"})
	return output, err
}

// buildCopilotPreamble assembles live session context for the copilot prompt.
// Only includes data the agent can't easily fetch itself (daemon-only state).
func (d *Daemon) buildCopilotPreamble() string {
	sessions := d.currentSessions()
	return copilot.BuildSessionsPreamble(sessions)
}

// handleCopilotCancel cancels any in-flight copilot prompt.
func (d *Daemon) handleCopilotCancel() *Response {
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
		d.copilotCancel = nil
	}
	d.copilotMu.Unlock()
	r := resultResponse(map[string]string{"status": "cancelled"})
	return &r
}

// handleCopilotTogglePreamble toggles injection of live session context into copilot prompts.
func (d *Daemon) handleCopilotTogglePreamble() *Response {
	newVal := !d.copilotPreamble.Load()
	d.copilotPreamble.Store(newVal)
	state := "off"
	if newVal {
		state = "on"
	}
	r := resultResponse(map[string]string{"preamble": state})
	return &r
}

// handleCopilotStatus returns copilot readiness and stats.
func (d *Daemon) handleCopilotStatus() *Response {
	events := d.copilotJournal.RecentEventsOrEmpty(999)

	r := resultResponse(CopilotStatusData{
		Ready:       true,
		EventsToday: len(events),
	})
	return &r
}

// clearCopilotCancel cancels and nils copilotCancel under the mutex.
// Call this when a copilot prompt finishes.
func (d *Daemon) clearCopilotCancel() {
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
		d.copilotCancel = nil
	}
	d.copilotMu.Unlock()
}

// appendCopilotHistory appends messages and trims to max (in-memory only).
// Used for live TUI display during this daemon session; OpenClaw's JSONL is authoritative.
func (d *Daemon) appendCopilotHistory(msgs ...CopilotHistoryMsg) {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = append(d.copilotHistory, msgs...)
	if len(d.copilotHistory) > maxCopilotHistory {
		d.copilotHistory = d.copilotHistory[len(d.copilotHistory)-maxCopilotHistory:]
	}
	d.copilotHistoryMu.Unlock()
}

// clearACPSessionMapping removes the copilot entry from OpenClaw's sessions.json.
// This prevents readACPHistory from loading stale history after /new clears the session.
func clearACPSessionMapping() {
	parts := strings.SplitN(acpSessionKey, ":", 3)
	if len(parts) != 3 {
		return
	}
	agentID := parts[1]

	home, _ := os.UserHomeDir()
	sessionsFile := filepath.Join(home, ".openclaw", "agents", agentID, "sessions", "sessions.json")

	data, err := os.ReadFile(sessionsFile)
	if err != nil {
		return
	}
	var sessionMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &sessionMap); err != nil {
		return
	}
	if _, ok := sessionMap[acpSessionKey]; !ok {
		return
	}
	delete(sessionMap, acpSessionKey)
	updated, err := json.Marshal(sessionMap)
	if err != nil {
		return
	}
	os.WriteFile(sessionsFile, updated, 0644) //nolint:errcheck
}

// readACPHistory reads the current copilot conversation from OpenClaw's JSONL session
// transcript. This is the source of truth: the actual messages sent between cmc and
// the OpenClaw agent, persisted by OpenClaw automatically.
func readACPHistory() []CopilotHistoryMsg {
	// Parse agent ID from "agent:development:copilot"
	parts := strings.SplitN(acpSessionKey, ":", 3)
	if len(parts) != 3 {
		return nil
	}
	agentID := parts[1]

	home, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(home, ".openclaw", "agents", agentID, "sessions")

	// sessions.json maps session key → current session UUID
	sessionsData, err := os.ReadFile(filepath.Join(sessionsDir, "sessions.json"))
	if err != nil {
		return nil
	}
	var sessionMap map[string]struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sessionsData, &sessionMap); err != nil {
		log.Printf("readACPHistory: parse sessions.json: %v", err)
		return nil
	}
	entry, ok := sessionMap[acpSessionKey]
	if !ok || entry.SessionID == "" {
		return nil
	}

	jsonlData, err := os.ReadFile(filepath.Join(sessionsDir, entry.SessionID+".jsonl"))
	if err != nil {
		return nil
	}
	return parseACPSessionHistory(jsonlData)
}

// parseACPSessionHistory parses OpenClaw's JSONL session transcript into display messages.
// Only user and assistant turns are kept; tool results, metadata events, and heartbeats
// are discarded.
func parseACPSessionHistory(data []byte) []CopilotHistoryMsg {
	var msgs []CopilotHistoryMsg
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &record); err != nil || record.Type != "message" {
			continue
		}
		role := record.Message.Role
		if role != "user" && role != "assistant" {
			continue
		}
		text := extractACPMessageText(role, record.Message.Content)
		if text == "" {
			continue
		}
		displayRole := role
		if role == "assistant" {
			displayRole = "copilot"
		}
		ts, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		msgs = append(msgs, CopilotHistoryMsg{Role: displayRole, Content: text, Time: ts})
	}
	return msgs
}

// extractACPMessageText extracts displayable text from an ACP message content array.
//
// User messages are double-wrapped:
//  1. ACP sender envelope: "Sender (untrusted metadata):\n```json\n...\n```\n[timestamp]\n\n<body>"
//  2. cmc preamble: "<live-sessions ...>...</live-sessions>\n\n<actual user text>"
//
// Assistant messages: concatenate text blocks; skip thinking blocks.
func extractACPMessageText(role string, contentRaw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(contentRaw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type != "text" {
			continue // skip thinking blocks for assistant; only text blocks matter
		}
		text := block.Text
		if role == "user" {
			// Strip ACP sender envelope: everything up to and including the closing ``` + newline
			if idx := strings.Index(text, "\n```\n"); idx != -1 {
				text = strings.TrimSpace(text[idx+5:])
				// Skip the [Day timestamp] line that follows the envelope
				if nl := strings.Index(text, "\n"); nl != -1 {
					text = strings.TrimSpace(text[nl+1:])
				}
			}
			// Strip cmc preamble: "<live-sessions ...>...</live-sessions>\n\n<user text>"
			if idx := strings.Index(text, "</live-sessions>"); idx != -1 {
				text = strings.TrimSpace(text[idx+len("</live-sessions>"):])
			}
			// Discard heartbeat-only messages (sent by copilot heartbeat system)
			if text == "#" {
				continue
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
