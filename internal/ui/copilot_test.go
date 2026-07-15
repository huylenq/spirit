package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCopilotTitleShowsEffectiveModelBeforeSessionID(t *testing.T) {
	title := copilotTitle(true, false, "openai-codex:gpt-5.4", "dont_ask", "8f2c51d1-89da-4605-b0a9-3990112917b0", 120)

	if !strings.Contains(title, "Lulu") {
		t.Fatalf("title %q does not contain Lulu", title)
	}
	if !strings.Contains(title, "8f2c51d1-89da-4605-b0a9-3990112917b0") {
		t.Fatalf("title %q does not contain the Hermes session ID", title)
	}
	if !strings.Contains(title, "openai-codex:gpt-5.4") {
		t.Fatalf("title %q does not contain the effective Hermes model", title)
	}
	if !strings.Contains(title, "dont_ask") {
		t.Fatalf("title %q does not contain the effective session mode", title)
	}
	if strings.Contains(title, "Copilot") {
		t.Fatalf("title %q still contains the old Copilot label", title)
	}
}

func TestCopilotAssistantMessagesRenderMarkdown(t *testing.T) {
	messages := []CopilotMessage{{
		Role:    "copilot",
		Content: "## Heading\n\n- **bold** item\n\n```go\nfmt.Println(\"x\")\n```",
	}}
	lines, _ := copilotRenderLines(messages, 60, false, "")
	rendered := strings.Join(lines, "\n")
	plain := ansi.Strip(rendered)

	for _, want := range []string{"Heading", "bold", `fmt.Println("x")`} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered markdown %q does not contain %q", plain, want)
		}
	}
	for _, raw := range []string{"**", "```"} {
		if strings.Contains(plain, raw) {
			t.Fatalf("rendered markdown %q still contains syntax %q", plain, raw)
		}
	}
	if !strings.Contains(plain, "• bold item") || !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("assistant message was not rendered as styled markdown: %q", rendered)
	}
}

func TestCopilotRelayTabCompletesSlashCommand(t *testing.T) {
	relay := NewCopilotRelayModel()
	relay.Activate()

	input := relay.TextInput()
	input.SetValue("/n")
	relay.CompleteSuggestion()

	if got := relay.Value(); got != "/new" {
		t.Fatalf("tab completion = %q, want /new", got)
	}
}

func TestCopilotRelayTabCompletesModelCommand(t *testing.T) {
	relay := NewCopilotRelayModel()
	relay.Activate()
	relay.TextInput().SetValue("/m")
	relay.CompleteSuggestion()

	if got := relay.Value(); got != "/model" {
		t.Fatalf("tab completion = %q, want /model", got)
	}
}

func TestCopilotStreamUpdatesHermesSessionID(t *testing.T) {
	model := NewCopilotModel()
	model.HandleStreamMsg(CopilotStreamMsg{Type: "session", Content: "session-123"})

	if got := model.SessionID(); got != "session-123" {
		t.Fatalf("session ID = %q, want session-123", got)
	}
}

func TestCopilotScrollUpUsesContentLinesInsteadOfMessageCount(t *testing.T) {
	model := NewCopilotModel()
	model.LoadHistory([]CopilotMessage{{
		Role:    "copilot",
		Content: strings.Repeat("line\n", 80),
	}})

	model.ScrollUp(50)

	if got := model.ScrollOffset(); got != 50 {
		t.Fatalf("scroll offset = %d, want 50 lines of scrollback", got)
	}
}
