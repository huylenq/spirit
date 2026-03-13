package daemon

import "encoding/json"

// Types for parsing Claude CLI `--output-format stream-json --verbose` output.
//
// The CLI emits one JSON line per event. The actual format is turn-level, not
// token-level: each assistant message arrives as a complete JSON object with
// the full content array (text blocks, tool_use blocks, thinking blocks).
//
// Example line types:
//   {"type":"system","subtype":"init",...}
//   {"type":"assistant","message":{"content":[{"type":"text","text":"..."}],...}}
//   {"type":"result","result":"...","duration_ms":...}

// cliStreamLine is the top-level line from `claude -p --output-format stream-json --verbose`.
type cliStreamLine struct {
	Type    string          `json:"type"`              // "system", "assistant", "user", "result", "rate_limit_event"
	Message json.RawMessage `json:"message,omitempty"` // for assistant/user types
	Result  string          `json:"result,omitempty"`  // for result type
	IsError bool            `json:"is_error,omitempty"`
}

// cliMessage is the assistant/user message object.
type cliMessage struct {
	Role    string            `json:"role"` // "assistant", "user"
	Content []cliContentBlock `json:"content"`
}

// cliContentBlock describes a content block within a message.
type cliContentBlock struct {
	Type     string `json:"type"`               // "text", "tool_use", "thinking", "tool_result"
	Text     string `json:"text,omitempty"`      // for text blocks
	Thinking string `json:"thinking,omitempty"`  // for thinking blocks
	ID       string `json:"id,omitempty"`        // for tool_use blocks
	Name     string `json:"name,omitempty"`      // for tool_use blocks
}

// copilotStreamParser maps CLI stream-json lines to CopilotStreamData events for the TUI.
type copilotStreamParser struct{}

// Parse processes a single line of stream-json output and returns zero or more
// CopilotStreamData events to push to subscribers.
func (p *copilotStreamParser) Parse(line []byte) []CopilotStreamData {
	var top cliStreamLine
	if err := json.Unmarshal(line, &top); err != nil {
		return nil
	}

	switch top.Type {
	case "result":
		return []CopilotStreamData{{Type: "done"}}

	case "assistant":
		return p.parseAssistantMessage(top.Message)

	default:
		// system, user, rate_limit_event — ignore
		return nil
	}
}

func (p *copilotStreamParser) parseAssistantMessage(raw json.RawMessage) []CopilotStreamData {
	if raw == nil {
		return nil
	}

	var msg cliMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}

	var events []CopilotStreamData
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, CopilotStreamData{
					Type:    "text_delta",
					Content: block.Text,
				})
			}

		case "thinking":
			if block.Thinking != "" {
				events = append(events, CopilotStreamData{
					Type:    "thought",
					Content: block.Thinking,
				})
			}

		case "tool_use":
			events = append(events, CopilotStreamData{
				Type:    "tool_call",
				Content: block.Name,
				ToolID:  block.ID,
				Status:  "in_progress",
			})
			// Immediately mark completed since we receive the full turn
			events = append(events, CopilotStreamData{
				Type:   "tool_update",
				ToolID: block.ID,
				Status: "completed",
			})
		}
	}

	return events
}
