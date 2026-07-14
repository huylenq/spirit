package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/copilot"
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
		// Persist the exchange for TUI display across reopens and daemon restarts.
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
// Served from the daemon's in-memory history, which is loaded from chat_history.json
// at startup so it survives both TUI reopen and daemon restart. Kept cheap (no
// subprocess) because the TUI fetches this eagerly on every launch.
func (d *Daemon) handleCopilotHistory() *Response {
	d.copilotHistoryMu.RLock()
	msgs := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(msgs, d.copilotHistory)
	d.copilotHistoryMu.RUnlock()
	r := resultResponse(CopilotHistoryData{Messages: msgs})
	return &r
}

// handleCopilotClearHistory wipes display history and resets the Hermes session so
// the next prompt starts a fresh conversation (triggered by /new in the TUI).
func (d *Daemon) handleCopilotClearHistory() *Response {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = nil
	d.copilotHistoryMu.Unlock()
	os.Remove(copilotHistoryFile()) //nolint:errcheck

	// Kill the ACP subprocess and forget the Hermes session UUID so the next
	// prompt starts a brand-new conversation instead of resuming the old one.
	d.acpClient.ResetSession()

	r := resultResponse(map[string]string{"status": "cleared"})
	return &r
}

// runCopilotPromptStreaming sends a prompt via the ACP client (hermes acp subprocess),
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

// appendCopilotHistory appends messages, trims to max, and persists to disk so the
// conversation survives TUI reopen and daemon restart.
func (d *Daemon) appendCopilotHistory(msgs ...CopilotHistoryMsg) {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = append(d.copilotHistory, msgs...)
	if len(d.copilotHistory) > maxCopilotHistory {
		d.copilotHistory = d.copilotHistory[len(d.copilotHistory)-maxCopilotHistory:]
	}
	snapshot := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(snapshot, d.copilotHistory)
	d.copilotHistoryMu.Unlock()

	saveCopilotHistory(snapshot)
}

// copilotHistoryFile is the on-disk store for copilot display history
// (~/.spirit/copilot/chat_history.json).
func copilotHistoryFile() string {
	return filepath.Join(claude.StatusDir(), "copilot", "chat_history.json")
}

// loadCopilotHistory reads persisted display history at daemon startup.
func loadCopilotHistory() []CopilotHistoryMsg {
	data, err := os.ReadFile(copilotHistoryFile())
	if err != nil {
		return nil
	}
	var msgs []CopilotHistoryMsg
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil
	}
	return msgs
}

// saveCopilotHistory writes display history to disk (best-effort).
func saveCopilotHistory(msgs []CopilotHistoryMsg) {
	path := copilotHistoryFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o644) //nolint:errcheck
}
