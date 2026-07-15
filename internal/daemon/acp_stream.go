package daemon

import (
	"encoding/json"
	"fmt"
	"log"
)

// Full ACP session/update coverage. Beyond the message/thought/tool/plan chunks
// the TUI renders, Hermes emits three stateful updates the client consumes even
// when no prompt is streaming: usage_update (context pressure),
// available_commands_update (advertised slash commands), and session_info_update
// (title refresh / internal-head rotation provenance). Unknown update types are
// logged and skipped — the unstable ACP protocol requires that tolerance.

type acpUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type acpContentBlock struct {
	Type string `json:"type"` // "text", "thinking"
	Text string `json:"text,omitempty"`
}

// dispatchUpdate routes one session/update notification. Stream chunks go to the
// active sink; usage/commands/rotation are applied to client state regardless of
// whether a prompt is in flight.
func (c *acpClient) dispatchUpdate(params json.RawMessage) {
	var up acpUpdateParams
	if err := json.Unmarshal(params, &up); err != nil {
		log.Printf("acp: bad session/update envelope: %v", err)
		return
	}

	var head struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(up.Update, &head); err != nil {
		log.Printf("acp: bad session/update body: %v", err)
		return
	}

	switch head.SessionUpdate {
	case "agent_message_chunk":
		var body struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(up.Update, &body) //nolint:errcheck
		for _, evt := range parseContentBlocks(body.Content, "text_delta", "thought") {
			c.emit(evt)
		}

	case "agent_thought_chunk":
		var body struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(up.Update, &body) //nolint:errcheck
		// A thought chunk's blocks are plain text; surface them as thoughts.
		for _, evt := range parseContentBlocks(body.Content, "thought", "thought") {
			c.emit(evt)
		}

	case "tool_call":
		var body struct {
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			Status     string `json:"status"`
		}
		json.Unmarshal(up.Update, &body) //nolint:errcheck
		c.emit(CopilotStreamData{
			Type:    "tool_call",
			Content: body.Title,
			ToolID:  body.ToolCallID,
			Kind:    body.Kind,
			Status:  body.Status,
		})

	case "tool_call_update":
		var body struct {
			ToolCallID string          `json:"toolCallId"`
			Status     string          `json:"status"`
			Content    json.RawMessage `json:"content"`
		}
		json.Unmarshal(up.Update, &body) //nolint:errcheck
		evt := CopilotStreamData{Type: "tool_update", ToolID: body.ToolCallID, Status: body.Status}
		if body.Content != nil {
			var blocks []struct {
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(body.Content, &blocks) == nil && len(blocks) > 0 {
				evt.Content = blocks[0].Content.Text
			}
		}
		c.emit(evt)

	case "plan":
		c.emit(CopilotStreamData{Type: "plan", Content: string(up.Update)})

	case "usage_update":
		c.handleUsageUpdate(up.Update)

	case "available_commands_update":
		c.handleCommandsUpdate(up.Update)

	case "session_info_update":
		c.handleSessionInfoUpdate(up.SessionID, up.Update)

	case "current_mode_update":
		c.handleCurrentModeUpdate(up.Update)

	default:
		log.Printf("acp: unhandled session/update %q", head.SessionUpdate)
	}
}

// parseContentBlocks decodes an ACP content payload (a single block or an array)
// into stream events, mapping the "text" and "thinking" block types to the given
// event types.
func parseContentBlocks(content json.RawMessage, textType, thinkType string) []CopilotStreamData {
	if len(content) == 0 {
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
		if b.Text == "" {
			continue
		}
		switch b.Type {
		case "text":
			events = append(events, CopilotStreamData{Type: textType, Content: b.Text})
		case "thinking":
			events = append(events, CopilotStreamData{Type: thinkType, Content: b.Text})
		}
	}
	return events
}

// --- stateful updates ---

type acpUsageUpdate struct {
	Size int64    `json:"size"`
	Used int64    `json:"used"`
	Cost *float64 `json:"cost"`
}

// handleUsageUpdate stores the latest context-pressure numbers and surfaces them
// as a `usage` chunk (the protocol/TUI already understand this chunk type).
func (c *acpClient) handleUsageUpdate(raw json.RawMessage) {
	var u acpUsageUpdate
	if err := json.Unmarshal(raw, &u); err != nil {
		return
	}
	c.mu.Lock()
	c.usage = &acpUsage{Size: u.Size, Used: u.Used}
	c.mu.Unlock()
	c.emit(CopilotStreamData{Type: "usage", Content: formatUsage(u)})
}

func formatUsage(u acpUsageUpdate) string {
	if u.Size <= 0 {
		return ""
	}
	pct := float64(u.Used) / float64(u.Size) * 100
	return fmt.Sprintf("%s / %s ctx (%.0f%%)", formatTokens(u.Used), formatTokens(u.Size), pct)
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

type acpCommandsUpdate struct {
	AvailableCommands []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"availableCommands"`
}

// handleCommandsUpdate records Hermes's advertised slash commands so surfaces can
// offer them as input suggestions (consumed by a later wave).
func (c *acpClient) handleCommandsUpdate(raw json.RawMessage) {
	var u acpCommandsUpdate
	if err := json.Unmarshal(raw, &u); err != nil {
		return
	}
	cmds := make([]copilotCommand, 0, len(u.AvailableCommands))
	for _, cm := range u.AvailableCommands {
		cmds = append(cmds, copilotCommand{Name: cm.Name, Description: cm.Description})
	}
	c.mu.Lock()
	c.commands = cmds
	c.mu.Unlock()
}

// handleCurrentModeUpdate records a mode switch Hermes announces mid-session and
// surfaces it as a `mode` stream chunk so the Lulu panel title reflects the current
// autonomy ceiling.
func (c *acpClient) handleCurrentModeUpdate(raw json.RawMessage) {
	var u struct {
		CurrentModeID string `json:"currentModeId"`
	}
	if err := json.Unmarshal(raw, &u); err != nil || u.CurrentModeID == "" {
		return
	}
	c.mu.Lock()
	c.modes.CurrentModeID = u.CurrentModeID
	c.mu.Unlock()
	c.emit(CopilotStreamData{Type: "mode", Content: u.CurrentModeID})
}

// handleSessionInfoUpdate logs a title/metadata refresh and, defensively, rotates
// the persisted session UUID if the enclosing session id ever diverges.
//
// Wire reality (Hermes v0.18.2): the ACP session id is Hermes's stable public
// handle — it does NOT change on compaction. Compaction rotates only the internal
// Hermes head, surfaced for observability under _meta.hermes.sessionProvenance;
// the id Spirit persists and resumes via session/load stays valid. The rotate
// guard below is therefore belt-and-suspenders, correct in the general case but a
// no-op against current Hermes.
func (c *acpClient) handleSessionInfoUpdate(updateSessionID string, raw json.RawMessage) {
	var u struct {
		Title string          `json:"title"`
		Meta  json.RawMessage `json:"_meta"`
	}
	json.Unmarshal(raw, &u) //nolint:errcheck
	c.rotateSessionIfChanged(updateSessionID)
	log.Printf("acp: session_info_update (title=%q)", u.Title)
}

// rotateSessionIfChanged updates and re-persists the session UUID if a server
// update ever carries a different session id than the one we hold.
func (c *acpClient) rotateSessionIfChanged(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	old := c.sessionID
	if old == "" || id == old {
		c.mu.Unlock()
		return
	}
	c.sessionID = id
	c.mu.Unlock()

	c.persistSessionID(id)
	log.Printf("acp: session rotated %s -> %s (persisted)", old, id)
}
