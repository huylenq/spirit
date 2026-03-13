package daemon

import (
	"testing"
)

func TestCopilotStreamParser_TextResponse(t *testing.T) {
	p := &copilotStreamParser{}

	// assistant message with text content
	events := p.Parse([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello world!"}]}}`))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Content != "Hello world!" {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

func TestCopilotStreamParser_ToolUse(t *testing.T) {
	p := &copilotStreamParser{}

	// assistant message with text + tool_use content blocks
	events := p.Parse([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"tool_123","name":"Read"}]}}`))
	if len(events) != 3 {
		t.Fatalf("expected 3 events (text + tool_call + tool_update), got %d", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Content != "Let me check." {
		t.Errorf("event[0] unexpected: %+v", events[0])
	}
	if events[1].Type != "tool_call" || events[1].Content != "Read" || events[1].ToolID != "tool_123" || events[1].Status != "in_progress" {
		t.Errorf("event[1] unexpected: %+v", events[1])
	}
	if events[2].Type != "tool_update" || events[2].ToolID != "tool_123" || events[2].Status != "completed" {
		t.Errorf("event[2] unexpected: %+v", events[2])
	}
}

func TestCopilotStreamParser_Thinking(t *testing.T) {
	p := &copilotStreamParser{}

	events := p.Parse([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think about this..."}]}}`))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "thought" || events[0].Content != "Let me think about this..." {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

func TestCopilotStreamParser_Result(t *testing.T) {
	p := &copilotStreamParser{}

	events := p.Parse([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"Hello!","duration_ms":1234}`))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "done" {
		t.Errorf("expected done, got %s", events[0].Type)
	}
}

func TestCopilotStreamParser_IgnoredTypes(t *testing.T) {
	p := &copilotStreamParser{}

	for _, line := range []string{
		`{"type":"system","subtype":"init","cwd":"/tmp"}`,
		`{"type":"system","subtype":"hook_started","hook_id":"abc"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`,
		`{"type":"rate_limit_event","rate_limit_info":{}}`,
	} {
		events := p.Parse([]byte(line))
		if len(events) != 0 {
			t.Errorf("expected 0 events for %s, got %d: %+v", line, len(events), events)
		}
	}
}

func TestCopilotStreamParser_MalformedJSON(t *testing.T) {
	p := &copilotStreamParser{}

	events := p.Parse([]byte(`{invalid json}`))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for malformed JSON, got %d", len(events))
	}
}

func TestCopilotStreamParser_MultiTurnConversation(t *testing.T) {
	p := &copilotStreamParser{}

	// Simulate a multi-turn response: thinking → text+tool → text → done
	lines := []string{
		// system init (ignored)
		`{"type":"system","subtype":"init","cwd":"/tmp"}`,
		// turn 1: thinking + text + tool_use
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Planning..."},{"type":"text","text":"Let me check. "},{"type":"tool_use","id":"tool_1","name":"Bash"}]}}`,
		// user turn with tool result (ignored)
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":"file listing"}]}}`,
		// turn 2: final text
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done!"}]}}`,
		// result
		`{"type":"result","subtype":"success","result":"Done!","duration_ms":500}`,
	}

	var allEvents []CopilotStreamData
	for _, line := range lines {
		allEvents = append(allEvents, p.Parse([]byte(line))...)
	}

	expected := []struct {
		typ     string
		content string
	}{
		{"thought", "Planning..."},
		{"text_delta", "Let me check. "},
		{"tool_call", "Bash"},
		{"tool_update", ""},
		{"text_delta", "Done!"},
		{"done", ""},
	}

	if len(allEvents) != len(expected) {
		t.Fatalf("expected %d events, got %d: %+v", len(expected), len(allEvents), allEvents)
	}

	for i, exp := range expected {
		if allEvents[i].Type != exp.typ {
			t.Errorf("event[%d]: expected type %q, got %q", i, exp.typ, allEvents[i].Type)
		}
		if exp.content != "" && allEvents[i].Content != exp.content {
			t.Errorf("event[%d]: expected content %q, got %q", i, exp.content, allEvents[i].Content)
		}
	}
}

func TestCopilotStreamParser_EmptyTextIgnored(t *testing.T) {
	p := &copilotStreamParser{}

	// text block with empty string should be ignored
	events := p.Parse([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":""}]}}`))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for empty text, got %d", len(events))
	}
}
