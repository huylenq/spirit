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
// Returns the rendered lines and the line index where the last user message starts
// (used by float mode to compute the default scroll offset).
// Also appends a streaming cursor on a new line when the last message is a user
// message or heartbeat (i.e., no copilot response has arrived yet).
func copilotRenderLines(messages []CopilotMessage, contentWidth int, streaming bool, streamCursor string, pendingTool *CopilotToolConfirm) ([]string, int) {
	var allLines []string
	lastPairStart := 0
	for i, msg := range messages {
		if msg.Role == "user" {
			lastPairStart = len(allLines)
		}
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

	// Streaming cursor on new line when no copilot response has arrived yet
	if streaming && len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		if lastMsg.Role == "user" || lastMsg.Role == "heartbeat" {
			allLines = append(allLines, copilotTextStyle.Render(streamCursor))
		}
	}

	// Append pending tool confirmation bar if present
	if pendingTool != nil {
		confirmLine := copilotConfirmStyle.Render(
			"\u26A0 " + pendingTool.ToolName + " \u2014 allow? [y/n]", // ⚠ ... —
		)
		allLines = append(allLines, confirmLine)
	}

	return allLines, lastPairStart
}

// copilotScrollWindow applies scroll offset to allLines and returns the visible slice.
func copilotScrollWindow(allLines []string, chatH, scrollOff int) []string {
	end := max(len(allLines)-scrollOff, 0)
	start := max(end-chatH, 0)
	return allLines[start:end]
}

// copilotTitle returns the styled title line.
func copilotTitle(focused bool, adjustMode bool) string {
	titleStyle := CopilotTitleStyle
	if !focused {
		titleStyle = CopilotTitleDimStyle
	}
	titleText := "Copilot"
	if adjustMode {
		titleText = "↑↓←→ move · ⇧←→ width · ⇧↑↓ height · r reset · esc done"
	}
	return titleStyle.Render(titleText)
}

// copilotAssembleBody joins title, visible lines, and optional input into a body string.
func copilotAssembleBody(title string, visible []string, inputView string) string {
	body := title + "\n" + strings.Join(visible, "\n")
	if inputView != "" {
		body += "\n" + inputView
	}
	return body
}

// RenderCopilotOverlay renders the copilot as a bordered floating overlay box.
// Always applies float-mode "last pair" default scroll offset.
func RenderCopilotOverlay(messages []CopilotMessage, inputView string, width, maxHeight int, scrollOff int, streaming bool, streamCursor string, pendingTool *CopilotToolConfirm, focused bool, adjustMode bool) string {
	contentWidth := max(width-4, 4) // outer - border(2) - padding(2)

	title := copilotTitle(focused, adjustMode)

	inputHeight := 0
	if inputView != "" {
		inputHeight = 1
	}

	maxChatH := max(maxHeight-2-1-inputHeight, 1) // maxHeight - border(2) - title(1) - input

	overlayStyle := CopilotOverlayStyle
	if !focused {
		overlayStyle = CopilotOverlayDimStyle
	}

	// Empty state
	if len(messages) == 0 && pendingTool == nil && !streaming {
		placeholder := copilotUserStyle.Render("Ask the copilot anything...")
		return overlayStyle.Width(width - 2).Render(copilotAssembleBody(title, []string{placeholder}, inputView))
	}

	allLines, lastPairStart := copilotRenderLines(messages, contentWidth, streaming, streamCursor, pendingTool)

	// Fit-to-content: natural height capped at max
	chatH := max(min(len(allLines), maxChatH), 1)

	// Float scroll: default view shows only the last pair
	effectiveScrollOff := scrollOff
	if lastPairStart > 0 {
		floatBase := len(allLines) - lastPairStart - chatH
		if floatBase > 0 {
			effectiveScrollOff += floatBase
		}
	}

	visible := copilotScrollWindow(allLines, chatH, effectiveScrollOff)
	return overlayStyle.Width(width - 2).Render(copilotAssembleBody(title, visible, inputView))
}

// RenderCopilotPanel renders the copilot as a docked right-side panel (full height).
func RenderCopilotPanel(messages []CopilotMessage, inputView string, width, height int, scrollOff int, streaming bool, streamCursor string, pendingTool *CopilotToolConfirm, focused bool) string {
	contentWidth := max(width-3, 4) // panel - left border(1) - padding(2)

	title := copilotTitle(focused, false)

	inputHeight := 0
	if inputView != "" {
		inputHeight = 1
	}

	chatH := max(height-1-inputHeight, 1) // total - title(1) - input

	panelStyle := CopilotDockedStyle
	if !focused {
		panelStyle = CopilotDockedDimStyle
	}

	if len(messages) == 0 && pendingTool == nil && !streaming {
		placeholder := copilotUserStyle.Render("Ask the copilot anything...")
		lines := make([]string, chatH)
		lines[chatH-1] = placeholder
		return panelStyle.Width(width - 1).Height(height).Render(copilotAssembleBody(title, lines, inputView))
	}

	allLines, _ := copilotRenderLines(messages, contentWidth, streaming, streamCursor, pendingTool)

	visible := copilotScrollWindow(allLines, chatH, scrollOff)

	// Pad to fill height (push content to bottom) — pre-allocate to avoid O(n²) prepend
	if pad := chatH - len(visible); pad > 0 {
		padded := make([]string, pad, chatH)
		visible = append(padded, visible...)
	}

	return panelStyle.Width(width - 1).Height(height).Render(copilotAssembleBody(title, visible, inputView))
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
