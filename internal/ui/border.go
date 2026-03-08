package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// BottomBorder renders the bottom border line: ╰──...──╯
func BottomBorder(width int) string {
	if width < 2 {
		return ""
	}
	return BorderCharStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")
}

// AddSideBorders wraps each line of content with │ on left and right.
// Lines are padded or truncated to exactly innerWidth visible characters.
func AddSideBorders(content string, innerWidth int) string {
	lines := strings.Split(content, "\n")
	border := BorderCharStyle.Render("│")
	var sb strings.Builder
	for i, line := range lines {
		lineW := lipgloss.Width(line)
		if lineW < innerWidth {
			line += strings.Repeat(" ", innerWidth-lineW)
		} else if lineW > innerWidth {
			line = ansi.Truncate(line, innerWidth, "")
		}
		sb.WriteString(border)
		sb.WriteString(line)
		sb.WriteString(border)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
