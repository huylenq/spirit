package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/copilot"
)

// handleCopilotChat invokes Claude CLI with the copilot prompt and returns the full response.
// This is a synchronous (blocking) RPC — the TUI shows "thinking..." while waiting.
func (d *Daemon) handleCopilotChat(data json.RawMessage) *Response {
	var req CopilotChatData
	if err := json.Unmarshal(data, &req); err != nil || req.Message == "" {
		r := errResponse("invalid copilot_chat request")
		return &r
	}

	// Build context preamble from live daemon state
	preamble := d.buildCopilotPreamble()
	fullPrompt := preamble + "\n\n" + req.Message

	// Cancel any existing copilot prompt
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	d.copilotCancel = cancel
	d.copilotMu.Unlock()
	defer cancel()

	// Run claude CLI synchronously
	output, err := d.runCopilotPrompt(ctx, fullPrompt)
	if err != nil {
		r := errResponse(fmt.Sprintf("copilot: %v", err))
		return &r
	}

	r := resultResponse(map[string]string{"response": output})
	return &r
}

// runCopilotPrompt invokes the Claude CLI as a subprocess and returns its output.
func (d *Daemon) runCopilotPrompt(ctx context.Context, prompt string) (string, error) {
	// Build MCP config JSON so copilot can call cmc tools
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}

	mcpConfig := fmt.Sprintf(`{"mcpServers":{"cmc":{"command":"%s","args":["mcp-serve"]}}}`, exe)

	args := []string{
		"-p", prompt,
		"--model", "sonnet",
		"--output-format", "text",
		"--max-turns", "10",
		"--mcp-config", mcpConfig,
		"--allowedTools", "mcp__cmc__*",
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = d.copilotWorkspace.Dir
	cmd.Stderr = os.Stderr // claude logs to stderr

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.Canceled {
			return "", fmt.Errorf("cancelled")
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timed out after 3 minutes")
		}
		return "", fmt.Errorf("claude: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
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
