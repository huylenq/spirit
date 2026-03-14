package ui

import "time"

// CopilotMessage represents a single message in the copilot chat.
type CopilotMessage struct {
	Role    string    // "user", "copilot", "tool_call", "tool_result", "thought", "plan", "error"
	Content string
	ToolID  string    // for tool_call/tool_result correlation
	Status  string    // tool status: pending, in_progress, completed, failed
	Kind    string    // tool kind: read, edit, execute, etc.
	Time    time.Time
}

// CopilotStreamMsg is a single streaming chunk from the copilot backend.
type CopilotStreamMsg struct {
	Type    string `json:"type"`    // "text_delta", "thought", "tool_call", "tool_update", "plan", "usage", "done", "error", "confirm"
	Content string `json:"content"`
	ToolID  string `json:"tool_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// CopilotToolConfirm holds info about a tool awaiting user approval.
type CopilotToolConfirm struct {
	ToolID   string
	ToolName string
}

// copilotStreamingFrames is the animated cursor shown while the copilot is streaming.
// A radar-sweep effect: the dot orbits through quadrants.
var copilotStreamingFrames = []string{"◴", "◷", "◶", "◵"}

// CopilotModel manages copilot conversation state and scroll position.
type CopilotModel struct {
	messages     []CopilotMessage
	scrollOff    int  // scroll offset from bottom (0 = at bottom)
	streaming    bool
	pendingTool  *CopilotToolConfirm
	usageInfo    string
	spinnerFrame int // incremented by app-level spinner tick
}

// NewCopilotModel creates a new empty copilot model.
func NewCopilotModel() CopilotModel {
	return CopilotModel{}
}

// AddUserMessage appends a user message with the current time.
func (c *CopilotModel) AddUserMessage(content string) {
	c.messages = append(c.messages, CopilotMessage{
		Role:    "user",
		Content: content,
		Time:    time.Now(),
	})
}

// AddInfoMessage appends a short system info message (e.g. "preamble: on").
func (c *CopilotModel) AddInfoMessage(content string) {
	c.messages = append(c.messages, CopilotMessage{
		Role:    "info",
		Content: content,
		Time:    time.Now(),
	})
}

// HandleStreamMsg processes a streaming chunk and updates state accordingly.
func (c *CopilotModel) HandleStreamMsg(msg CopilotStreamMsg) {
	switch msg.Type {
	case "text_delta":
		if n := len(c.messages); n > 0 && c.messages[n-1].Role == "copilot" {
			c.messages[n-1].Content += msg.Content
		} else {
			c.messages = append(c.messages, CopilotMessage{
				Role:    "copilot",
				Content: msg.Content,
				Time:    time.Now(),
			})
		}

	case "thought":
		if n := len(c.messages); n > 0 && c.messages[n-1].Role == "thought" {
			c.messages[n-1].Content += msg.Content
		} else {
			c.messages = append(c.messages, CopilotMessage{
				Role:    "thought",
				Content: msg.Content,
				Time:    time.Now(),
			})
		}

	case "tool_call":
		c.messages = append(c.messages, CopilotMessage{
			Role:    "tool_call",
			Content: msg.Content,
			ToolID:  msg.ToolID,
			Status:  msg.Status,
			Kind:    msg.Kind,
			Time:    time.Now(),
		})

	case "tool_update":
		for i := len(c.messages) - 1; i >= 0; i-- {
			if c.messages[i].ToolID == msg.ToolID {
				if msg.Status != "" {
					c.messages[i].Status = msg.Status
				}
				if msg.Content != "" {
					c.messages[i].Content = msg.Content
				}
				break
			}
		}

	case "plan":
		// Replace existing plan or create new one
		replaced := false
		for i := len(c.messages) - 1; i >= 0; i-- {
			if c.messages[i].Role == "plan" {
				c.messages[i].Content = msg.Content
				replaced = true
				break
			}
		}
		if !replaced {
			c.messages = append(c.messages, CopilotMessage{
				Role:    "plan",
				Content: msg.Content,
				Time:    time.Now(),
			})
		}

	case "usage":
		c.usageInfo = msg.Content

	case "done":
		c.streaming = false

	case "error":
		c.messages = append(c.messages, CopilotMessage{
			Role:    "error",
			Content: msg.Content,
			Time:    time.Now(),
		})
		c.streaming = false

	case "confirm":
		c.pendingTool = &CopilotToolConfirm{
			ToolID:   msg.ToolID,
			ToolName: msg.Content,
		}
	}
}

// Messages returns the message list.
func (c *CopilotModel) Messages() []CopilotMessage {
	return c.messages
}

// Streaming reports whether the copilot is currently generating a response.
func (c *CopilotModel) Streaming() bool {
	return c.streaming
}

// PendingTool returns the tool confirmation awaiting user approval, if any.
func (c *CopilotModel) PendingTool() *CopilotToolConfirm {
	return c.pendingTool
}

// ClearPendingTool removes the pending tool confirmation.
func (c *CopilotModel) ClearPendingTool() {
	c.pendingTool = nil
}

// SetStreaming sets the streaming state.
func (c *CopilotModel) SetStreaming(v bool) {
	c.streaming = v
}

// ScrollUp increases the scroll offset (moves viewport toward older messages).
func (c *CopilotModel) ScrollUp(n int) {
	c.scrollOff += n
	maxOff := len(c.messages) - 1
	if maxOff < 0 {
		maxOff = 0
	}
	if c.scrollOff > maxOff {
		c.scrollOff = maxOff
	}
}

// ScrollDown decreases the scroll offset (moves viewport toward newer messages).
func (c *CopilotModel) ScrollDown(n int) {
	c.scrollOff -= n
	if c.scrollOff < 0 {
		c.scrollOff = 0
	}
}

// ScrollOffset returns the current scroll offset from bottom.
func (c *CopilotModel) ScrollOffset() int {
	return c.scrollOff
}

// UsageInfo returns the last usage string, or "".
func (c *CopilotModel) UsageInfo() string {
	return c.usageInfo
}

// TickSpinner advances the spinner frame counter. Called from the app-level spinner tick.
func (c *CopilotModel) TickSpinner() {
	c.spinnerFrame++
}

// StreamingCursor returns the current animated cursor character for the streaming state.
func (c *CopilotModel) StreamingCursor() string {
	return copilotStreamingFrames[c.spinnerFrame%len(copilotStreamingFrames)]
}

// ResetScroll resets the scroll offset to bottom (most recent messages).
func (c *CopilotModel) ResetScroll() {
	c.scrollOff = 0
}

// LoadHistory replaces the message list with historical messages (called on TUI reconnect).
func (c *CopilotModel) LoadHistory(msgs []CopilotMessage) {
	c.messages = msgs
	c.scrollOff = 0
}
