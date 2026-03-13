package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/copilot"
)

const maxCopilotHistory = 200 // 100 exchanges (user + copilot per exchange)

// chatHistoryPath returns the path to the persisted chat history file.
func (d *Daemon) chatHistoryPath() string {
	return filepath.Join(d.copilotWorkspace.Dir, "chat_history.json")
}

// saveCopilotHistory writes the current in-memory history to disk (caller holds copilotHistoryMu).
func (d *Daemon) saveCopilotHistory() {
	data, err := json.Marshal(d.copilotHistory)
	if err != nil {
		log.Printf("copilot: marshal history: %v", err)
		return
	}
	if err := os.WriteFile(d.chatHistoryPath(), data, 0o644); err != nil {
		log.Printf("copilot: write history: %v", err)
	}
}

// loadCopilotHistory reads chat history from disk into d.copilotHistory. Called once at startup.
func (d *Daemon) loadCopilotHistory() {
	data, err := os.ReadFile(d.chatHistoryPath())
	if err != nil {
		return // file not found on first run — silent
	}
	var msgs []CopilotHistoryMsg
	if err := json.Unmarshal(data, &msgs); err != nil {
		log.Printf("copilot: parse history: %v", err)
		return
	}
	d.copilotHistoryMu.Lock()
	d.copilotHistory = msgs
	d.copilotHistoryMu.Unlock()
}

// handleCopilotChat starts a streaming copilot prompt in the background.
// Returns an immediate "streaming" ack; actual tokens arrive via the subscribe connection.
func (d *Daemon) handleCopilotChat(data json.RawMessage) *Response {
	var req CopilotChatData
	if err := json.Unmarshal(data, &req); err != nil || req.Message == "" {
		r := errResponse("invalid copilot_chat request")
		return &r
	}

	// Build context preamble from live daemon state
	preamble := d.buildCopilotPreamble()
	fullPrompt := preamble + "\n\n" + req.Message

	// Cancel any existing copilot prompt (including heartbeat)
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
			d.pushCopilotStream(CopilotStreamData{Type: "error", Content: err.Error()})
			d.pushCopilotStream(CopilotStreamData{Type: "done"})
			return
		}
		// Persist full response to history
		now := time.Now()
		d.appendCopilotHistory(
			CopilotHistoryMsg{Role: "user", Content: req.Message, Time: now},
			CopilotHistoryMsg{Role: "copilot", Content: output, Time: now},
		)
	}()

	r := resultResponse(map[string]string{"status": "streaming"})
	return &r
}

// handleCopilotHistory returns the full in-memory copilot conversation so the TUI
// can restore it after a close/reopen (history lives as long as the daemon does).
func (d *Daemon) handleCopilotHistory() *Response {
	d.copilotHistoryMu.RLock()
	msgs := make([]CopilotHistoryMsg, len(d.copilotHistory))
	copy(msgs, d.copilotHistory)
	d.copilotHistoryMu.RUnlock()
	r := resultResponse(CopilotHistoryData{Messages: msgs})
	return &r
}

// handleCopilotClearHistory wipes the in-memory history and deletes the disk file.
func (d *Daemon) handleCopilotClearHistory() *Response {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = nil
	os.Remove(d.chatHistoryPath())
	d.copilotHistoryMu.Unlock()
	r := resultResponse(map[string]string{"status": "cleared"})
	return &r
}

// runCopilotPromptStreaming invokes the Claude CLI with stream-json output,
// parsing and pushing events to subscribers in real-time. Returns the full
// accumulated text response for history persistence.
func (d *Daemon) runCopilotPromptStreaming(ctx context.Context, prompt string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}

	mcpConfig := fmt.Sprintf(`{"mcpServers":{"cmc":{"command":"%s","args":["mcp-serve"]}}}`, exe)

	args := []string{
		"-p", prompt,
		"--model", "sonnet",
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "10",
		"--mcp-config", mcpConfig,
		"--allowedTools", "mcp__cmc__*",
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = d.copilotWorkspace.Dir
	cmd.Stderr = os.Stderr
	// Clear CLAUDECODE to avoid "nested sessions" rejection when the daemon
	// was started from within a Claude Code session.
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	var fullText strings.Builder
	parser := &copilotStreamParser{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // large buffer for tool results

	sentDone := false
	for scanner.Scan() {
		events := parser.Parse(scanner.Bytes())
		for _, evt := range events {
			if evt.Type == "text_delta" {
				fullText.WriteString(evt.Content)
			}
			if evt.Type == "done" {
				sentDone = true
			}
			d.pushCopilotStream(evt)
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.Canceled {
			if !sentDone {
				d.pushCopilotStream(CopilotStreamData{Type: "done"})
			}
			return "", fmt.Errorf("cancelled")
		}
		if ctx.Err() == context.DeadlineExceeded {
			if !sentDone {
				d.pushCopilotStream(CopilotStreamData{Type: "done"})
			}
			return "", fmt.Errorf("timed out after 3 minutes")
		}
		if !sentDone {
			d.pushCopilotStream(CopilotStreamData{Type: "done"})
		}
		return "", fmt.Errorf("claude: %w", err)
	}

	// Ensure done is sent even if result line was missing
	if !sentDone {
		d.pushCopilotStream(CopilotStreamData{Type: "done"})
	}

	return strings.TrimSpace(fullText.String()), nil
}

// buildCopilotPreamble assembles context from live daemon state for the copilot prompt.
func (d *Daemon) buildCopilotPreamble() string {
	sessions := d.currentSessions()
	events := d.copilotJournal.RecentEventsOrEmpty(50)
	memory := d.copilotMemory.ReadLongTermOrEmpty()
	digest := claude.ReadCachedDigest()

	var digestStr string
	if digest != nil {
		digestStr = digest.Summary
	}

	return copilot.BuildContextPreamble(memory, events, sessions, digestStr)
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

// handleCopilotStatus returns copilot readiness and stats.
func (d *Daemon) handleCopilotStatus() *Response {
	events := d.copilotJournal.RecentEventsOrEmpty(999)
	memContent := d.copilotMemory.ReadLongTermOrEmpty()

	r := resultResponse(CopilotStatusData{
		Ready:       true,
		EventsToday: len(events),
		MemoryBytes: len(memContent),
	})
	return &r
}

// clearCopilotCancel cancels and nils copilotCancel under the mutex.
// Call this when a copilot prompt (user-initiated or heartbeat) finishes.
func (d *Daemon) clearCopilotCancel() {
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
		d.copilotCancel = nil
	}
	d.copilotMu.Unlock()
}

// appendCopilotHistory appends messages, trims to max, and persists to disk.
func (d *Daemon) appendCopilotHistory(msgs ...CopilotHistoryMsg) {
	d.copilotHistoryMu.Lock()
	d.copilotHistory = append(d.copilotHistory, msgs...)
	if len(d.copilotHistory) > maxCopilotHistory {
		d.copilotHistory = d.copilotHistory[len(d.copilotHistory)-maxCopilotHistory:]
	}
	d.saveCopilotHistory()
	d.copilotHistoryMu.Unlock()
}

// filterEnv returns a copy of environ with the named key removed.
func filterEnv(environ []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environ))
	for _, e := range environ {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			continue
		}
		out = append(out, e)
	}
	return out
}
