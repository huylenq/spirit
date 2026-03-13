package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Copilot chat styles
var (
	copilotUserStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
	copilotTextStyle    = lipgloss.NewStyle()
	copilotToolStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
	copilotThoughtStyle = lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)
	copilotErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Bold(true)
	copilotHeartbeatStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Italic(true) // indigo
	copilotPlanStyle      = lipgloss.NewStyle().Foreground(ColorMuted)
	copilotConfirmStyle = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "#fef3c7", Dark: "#422006"}).
				Foreground(lipgloss.AdaptiveColor{Light: "#92400e", Dark: "#fbbf24"}).
				Bold(true).
				Padding(0, 1)
)

// toolStatusIcon returns a status indicator glyph for tool call messages.
func toolStatusIcon(status string) string {
	switch status {
	case "pending":
		return "\u25CB" // ○
	case "in_progress":
		return "\u25D0" // ◐
	case "completed":
		return "\u25CF" // ●
	case "failed":
		return "\u2717" // ✗
	default:
		return "\u25CB" // ○
	}
}

// copilotRenderLines converts messages into styled terminal lines.
func copilotRenderLines(messages []CopilotMessage, contentWidth int, streaming bool, streamCursor string, pendingTool *CopilotToolConfirm) []string {
	var allLines []string
	for i, msg := range messages {
		var rendered string
		switch msg.Role {
		case "user":
			lines := wrapText("> "+msg.Content, contentWidth)
			rendered = copilotUserStyle.Render(lines)

		case "heartbeat":
			lines := wrapText("\u2764 "+msg.Content, contentWidth) // ❤ heartbeat prefix
			rendered = copilotHeartbeatStyle.Render(lines)

		case "copilot":
			text := msg.Content
			// Append animated streaming cursor to last copilot message
			if streaming && i == len(messages)-1 {
				text += streamCursor
			}
			lines := wrapText(text, contentWidth)
			rendered = copilotTextStyle.Render(lines)

		case "tool_call":
			icon := toolStatusIcon(msg.Status)
			line := icon + " \u2699 " + msg.Content // ⚙
			rendered = copilotToolStyle.Render(ansi.Truncate(line, contentWidth, "\u2026"))

		case "thought":
			// Collapse to first line
			first := msg.Content
			if idx := strings.Index(first, "\n"); idx >= 0 {
				first = first[:idx] + "\u2026"
			}
			line := "\U0001F4AD " + first // 💭
			rendered = copilotThoughtStyle.Render(ansi.Truncate(line, contentWidth, "\u2026"))

		case "plan":
			lines := wrapText(msg.Content, contentWidth)
			rendered = copilotPlanStyle.Render(lines)

		case "error":
			line := "\u2717 " + msg.Content // ✗
			lines := wrapText(line, contentWidth)
			rendered = copilotErrorStyle.Render(lines)

		default:
			rendered = msg.Content
		}

		msgLines := strings.Split(rendered, "\n")
		allLines = append(allLines, msgLines...)
	}

	// Append pending tool confirmation bar if present
	if pendingTool != nil {
		confirmLine := copilotConfirmStyle.Render(
			"\u26A0 " + pendingTool.ToolName + " \u2014 allow? [y/n]", // ⚠ ... —
		)
		allLines = append(allLines, confirmLine)
	}

	return allLines
}

// RenderCopilotOverlay renders the copilot as a bordered floating overlay box.
// width is the desired outer box width (including border + padding).
// maxHeight is the maximum outer box height (including border).
// The overlay is fit-to-content: it shrinks when there are few messages,
// and grows upward (from the bottom) until hitting maxHeight.
// adjustMode replaces the title with a keybinding hint for resize/reposition mode.
func RenderCopilotOverlay(messages []CopilotMessage, inputView string, width, maxHeight int, scrollOff int, streaming bool, streamCursor string, pendingTool *CopilotToolConfirm, focused bool, adjustMode bool) string {
	// Text content width: outer - border(2) - padding(2)
	contentWidth := max(width-4, 4)

	// Title line — dim when unfocused; hint when in adjust mode
	titleStyle := CopilotTitleStyle
	if !focused {
		titleStyle = CopilotTitleDimStyle
	}
	titleText := "Copilot"
	if adjustMode {
		titleText = "↑↓←→ move · ⇧←→ width · ⇧↑↓ height · r reset · esc done"
	}
	title := titleStyle.Render(titleText)

	inputHeight := 0
	if inputView != "" {
		inputHeight = 1
	}

	// Max chat lines: maxHeight - border(2) - title(1) - input
	maxChatH := max(maxHeight-2-1-inputHeight, 1)

	overlayStyle := CopilotOverlayStyle
	if !focused {
		overlayStyle = CopilotOverlayDimStyle
	}

	// Empty state (or streaming with only a user message, no copilot response yet)
	if len(messages) == 0 && pendingTool == nil && !streaming {
		placeholder := copilotUserStyle.Render("Ask the copilot anything...")
		body := title + "\n" + placeholder
		if inputView != "" {
			body += "\n" + inputView
		}
		return overlayStyle.Width(width - 2).Render(body)
	}

	allLines := copilotRenderLines(messages, contentWidth, streaming, streamCursor, pendingTool)

	// If streaming but no copilot message has arrived yet, show the animated cursor
	if streaming && len(allLines) > 0 {
		lastMsg := messages[len(messages)-1]
		if lastMsg.Role == "user" || lastMsg.Role == "heartbeat" {
			allLines = append(allLines, copilotTextStyle.Render(streamCursor))
		}
	}

	// Fit-to-content: natural height capped at max
	chatH := max(min(len(allLines), maxChatH), 1)

	// Apply scroll offset and take last chatH lines
	end := max(len(allLines)-scrollOff, 0)
	start := max(end-chatH, 0)
	visible := allLines[start:end]

	body := title + "\n" + strings.Join(visible, "\n")
	if inputView != "" {
		body += "\n" + inputView
	}

	return overlayStyle.Width(width - 2).Render(body)
}

// wrapText performs simple word wrapping to fit within maxWidth.
func wrapText(text string, maxWidth int) string {
	if maxWidth < 1 {
		maxWidth = 1
	}
	var result []string
	for _, line := range strings.Split(text, "\n") {
		if ansi.StringWidth(line) <= maxWidth {
			result = append(result, line)
			continue
		}
		// Word-wrap long lines
		remaining := line
		for remaining != "" {
			if ansi.StringWidth(remaining) <= maxWidth {
				result = append(result, remaining)
				break
			}
			// Find a break point
			cutAt := 0
			lastSpace := -1
			w := 0
			for i, r := range remaining {
				rw := 1
				if r >= 0x1100 { // rough CJK check
					rw = 2
				}
				if w+rw > maxWidth {
					cutAt = i
					break
				}
				if r == ' ' {
					lastSpace = i
				}
				w += rw
				cutAt = i + len(string(r))
			}
			if lastSpace > 0 && lastSpace > cutAt/2 {
				result = append(result, remaining[:lastSpace])
				remaining = strings.TrimLeft(remaining[lastSpace:], " ")
			} else {
				result = append(result, remaining[:cutAt])
				remaining = remaining[cutAt:]
			}
		}
	}
	return strings.Join(result, "\n")
}
