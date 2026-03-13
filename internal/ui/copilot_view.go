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
	copilotPlanStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
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

// RenderCopilotChat renders the copilot chat panel content.
// Messages are rendered bottom-up (most recent at bottom), scrollable via scrollOff.
func RenderCopilotChat(messages []CopilotMessage, width, height int, scrollOff int, streaming bool, pendingTool *CopilotToolConfirm) string {
	if width < 4 {
		width = 4
	}
	contentWidth := width - 2 // side padding

	if len(messages) == 0 && pendingTool == nil {
		placeholder := copilotUserStyle.Render("Ask the copilot anything about your sessions...")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, placeholder)
	}

	// Render all messages into lines
	var allLines []string
	for i, msg := range messages {
		var rendered string
		switch msg.Role {
		case "user":
			lines := wrapText("> "+msg.Content, contentWidth)
			rendered = copilotUserStyle.Render(lines)

		case "copilot":
			text := msg.Content
			// Append streaming cursor to last copilot message
			if streaming && i == len(messages)-1 {
				text += "\u258C" // ▌
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

	// Apply scroll offset and take last `height` lines
	totalLines := len(allLines)
	end := totalLines - scrollOff
	if end < 0 {
		end = 0
	}
	start := end - height
	if start < 0 {
		start = 0
	}

	visible := allLines[start:end]

	// Pad with empty lines if we have fewer lines than height
	for len(visible) < height {
		visible = append([]string{""}, visible...)
	}

	return strings.Join(visible, "\n")
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
